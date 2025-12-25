# Architecture

This document describes the architecture and design of the Aqua Service Controller.

## Overview

The Aqua Service Controller migrates running StatefulSets and their Persistent Volumes from a source Kubernetes cluster to a destination cluster with data integrity as the primary constraint.

### Core Strategy: "Orphan & Adopt (Low-Index First)"

Because StatefulSets must scale sequentially (0 → N), we cannot move random pods. The controller dismantles the source cluster and builds up the destination cluster in exact order: `web-0` → `web-1` → `web-n`.

### Infrastructure Requirements

| Requirement | Description |
|-------------|-------------|
| **Topology** | Shared VPC or Peered VPCs (same AWS region) |
| **Storage** | AWS EBS volumes (gp2, gp3, io1, io2) |
| **Connectivity** | Controller needs kubectl access to both clusters |
| **AWS Permissions** | `ec2:DescribeVolumes` permission |

## Custom Resource Definition

The controller reconciles `StatefulSetMigration` resources:

```yaml
apiVersion: migration.aqua.io/v1alpha1
kind: StatefulSetMigration
metadata:
  name: my-migration
spec:
  migrationId: "unique-id"
  
  sourceCluster:
    kubeConfigSecret: source-cluster-kubeconfig
  sourceNamespace: production
  statefulSetName: web
  
  destCluster:
    kubeConfigSecret: dest-cluster-kubeconfig
  destNamespace: production
  
  # Optional
  force: false
  storageClassMapping:
    gp2: gp3
  volumeDetachTimeout: 5m
  podReadyTimeout: 10m
```

### Status & State Machine

The migration progresses through these phases:

```
Pending → PreFlightChecks → FreezingSource → MigratingPods → Finalizing → Completed
                                                    ↓
                                                 Failed
```

| Phase | Description |
|-------|-------------|
| `Pending` | Initial state, awaiting processing |
| `PreFlightChecks` | Validating connectivity, namespaces, conflicts |
| `FreezingSource` | Patching PV reclaim policies, orphaning StatefulSet |
| `MigratingPods` | Pod-by-pod migration loop |
| `Finalizing` | Garbage collection of source resources |
| `Completed` | Migration successful |
| `Failed` | Error occurred, manual intervention required |

## Migration Workflow

### Phase 1: Pre-Flight Checks

Before modifying any resources, the controller validates:

1. **Cluster Connectivity** - Verify API access to both clusters
2. **Namespace Existence** - Ensure destination namespace exists
3. **Conflict Check** - Ensure no StatefulSet with the same name exists in destination
4. **Service Dependency** - Verify the headless service exists in destination (required for StatefulSet)

### Phase 2: Freeze Source

Prepare the source cluster for disassembly without deleting data:

1. **Patch PV Reclaim Policy**
   - List all PVCs for the StatefulSet
   - Find bound PVs
   - Patch all PVs to `persistentVolumeReclaimPolicy: Retain` (**critical safety step**)

2. **Orphan the StatefulSet**
   - Delete the StatefulSet with `propagationPolicy: Orphan`
   - Result: StatefulSet definition removed, but pods remain running and PVCs remain bound

### Phase 3: Migration Loop

The controller iterates from index `i = 0` to `replicas - 1`:

```
┌─────────────────────────────────────────────────────────────────┐
│                     For each pod index i                        │
├─────────────────────────────────────────────────────────────────┤
│  1. Delete pod-i in source cluster                              │
│  2. Wait for pod to disappear from API                          │
│  3. Get EBS volume ID from PV                                   │
│  4. Poll AWS EC2 until volume state == "available"              │
│  5. Create PV in destination (with volumeHandle + nodeAffinity) │
│  6. Create pre-bound PVC in destination                         │
│  7. Create/Scale StatefulSet in destination (replicas: i+1)     │
│  8. Wait for pod-i to be Ready in destination                   │
│  9. Update status.currentIndex                                  │
└─────────────────────────────────────────────────────────────────┘
```

#### Volume Detachment (Critical Step)

The controller polls AWS EC2 directly rather than relying on Kubernetes PV status (which is eventually consistent):

```go
func (c *EBSClient) WaitForVolumeDetach(ctx context.Context, volumeID string, cfg WaitForVolumeDetachConfig) error {
    ticker := time.NewTicker(cfg.PollInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            info, err := c.GetVolumeInfo(ctx, volumeID)
            if err != nil {
                return err
            }
            if info.State == types.VolumeStateAvailable {
                return nil // Volume is ready to attach to destination
            }
        }
    }
}
```

#### PV/PVC Translation

When creating PV in the destination cluster, the controller:

1. Copies capacity and access modes from source
2. Sets `persistentVolumeReclaimPolicy: Retain`
3. Pre-binds to destination PVC via `claimRef`
4. **Preserves node affinity** for zone-constrained volumes:

```go
nodeAffinity := &corev1.VolumeNodeAffinity{
    Required: &corev1.NodeSelector{
        NodeSelectorTerms: []corev1.NodeSelectorTerm{
            {
                MatchExpressions: []corev1.NodeSelectorRequirement{
                    {
                        Key:      "topology.kubernetes.io/zone",
                        Operator: corev1.NodeSelectorOpIn,
                        Values:   []string{originalZone}, // Must match volume's AZ
                    },
                },
            },
        },
    },
}
```

### Phase 4: Finalization

1. **Garbage Collection** - Delete orphaned PVCs and PVs in source cluster
   - Because reclaim policy is `Retain`, this deletes K8s objects but leaves EBS volumes intact
2. **Mark Complete** - Set status to `Completed`

## Failure & Recovery

Since we're moving state, "rollback" means migrating back to the source cluster.

### Failure Scenario Example

If migration fails at index 2 (pods 0 and 1 are in destination; 2, 3, 4 are in source):

1. **Controller pauses** - Status set to `Failed` with error message
2. **Operator decision** - Human decides to roll forward (fix error) or roll back
3. **Resume/Rollback** - Either fix the issue and re-reconcile, or manually reverse the migration

### Manual Rollback Procedure

```bash
# 1. Scale down destination StatefulSet
kubectl --context=dest scale sts web --replicas=0 -n production

# 2. Delete destination StatefulSet
kubectl --context=dest delete sts web -n production

# 3. Delete destination PVCs (Retain policy keeps EBS volumes)
kubectl --context=dest delete pvc -l app=web -n production

# 4. Delete destination PVs
kubectl --context=dest delete pv -l migration.aqua.io/migrated=true

# 5. Recreate PVs and PVCs in source cluster
# 6. Recreate StatefulSet in source cluster
```

## Component Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Aqua Service Controller                       │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐  │
│  │   Reconciler     │  │  ClientManager   │  │    EBSClient     │  │
│  │                  │  │                  │  │                  │  │
│  │ - State machine  │  │ - Multi-cluster  │  │ - DescribeVols   │  │
│  │ - Phase logic    │  │ - Secret-based   │  │ - WaitForDetach  │  │
│  │ - Error handling │  │   kubeconfig     │  │ - Volume info    │  │
│  └────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘  │
│           │                     │                     │            │
│           └─────────────────────┼─────────────────────┘            │
│                                 │                                   │
│  ┌──────────────────────────────┴───────────────────────────────┐  │
│  │                      PV Translator                            │  │
│  │                                                               │  │
│  │  - Extract EBS volume ID (CSI + legacy formats)               │  │
│  │  - Preserve node affinity / availability zone                 │  │
│  │  - Storage class mapping                                      │  │
│  │  - Generate destination PV/PVC specs                          │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

## Supported Volume Types

| Type | Support | Notes |
|------|---------|-------|
| AWS EBS (CSI) | ✅ Full | `ebs.csi.aws.com` driver |
| AWS EBS (Legacy) | ✅ Full | `awsElasticBlockStore` |
| Other CSI | ❌ | Not supported |
| NFS/EFS | ❌ | Not applicable (shared storage) |

## Security Considerations

1. **Kubeconfig Secrets** - Store cluster credentials securely; controller reads from Kubernetes Secrets
2. **RBAC** - Controller needs elevated permissions on both clusters
3. **AWS IAM** - Use IRSA (IAM Roles for Service Accounts) on EKS
4. **Finalizers** - Prevent accidental deletion during migration
