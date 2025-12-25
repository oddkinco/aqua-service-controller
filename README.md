# Aqua Service Controller

[![CI](https://github.com/oddkinco/aqua-service-controller/actions/workflows/ci.yaml/badge.svg)](https://github.com/oddkinco/aqua-service-controller/actions/workflows/ci.yaml)
[![Security](https://github.com/oddkinco/aqua-service-controller/actions/workflows/security.yaml/badge.svg)](https://github.com/oddkinco/aqua-service-controller/actions/workflows/security.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/oddkinco/aqua-service-controller)](https://goreportcard.com/report/github.com/oddkinco/aqua-service-controller)
[![License](https://img.shields.io/github/license/oddkinco/aqua-service-controller)](LICENSE)

A Kubernetes controller for live-migrating StatefulSets and their Persistent Volumes between clusters.

## Overview

Aqua Service Controller enables zero-downtime migration of StatefulSets from one Kubernetes cluster to another, preserving data by re-attaching existing AWS EBS volumes to the destination cluster.

### Key Features

- **Live Migration** - Migrate StatefulSets pod-by-pod with minimal downtime
- **Data Integrity** - EBS volumes are detached and re-attached, not copied
- **Sequential Ordering** - Respects StatefulSet semantics (migrates index 0 → N)
- **Multi-Cluster** - Operates across two clusters using kubeconfig secrets
- **Resumable** - Failed migrations can be resumed from the last successful pod

### Use Cases

- Cluster upgrades (migrate workloads to a new cluster)
- Region/AZ migrations within the same AWS region
- Kubernetes version upgrades
- Infrastructure consolidation

## Prerequisites

- Two Kubernetes clusters in the same AWS region (or peered VPCs)
- AWS EBS volumes (gp2, gp3, io1, io2)
- AWS credentials with `ec2:DescribeVolumes` permission
- kubectl access to both clusters

## Installation

### Using kubectl

```bash
# Install CRD
kubectl apply -f https://raw.githubusercontent.com/oddkinco/aqua-service-controller/main/config/crd/migration.aqua.io_statefulsetmigrations.yaml

# Install controller
kubectl apply -f https://raw.githubusercontent.com/oddkinco/aqua-service-controller/main/config/rbac/role.yaml
kubectl apply -f https://raw.githubusercontent.com/oddkinco/aqua-service-controller/main/config/manager/manager.yaml
```

### Using Helm (coming soon)

```bash
helm repo add aqua https://oddkinco.github.io/aqua-service-controller
helm install aqua-controller aqua/aqua-service-controller
```

## Quick Start

### 1. Create kubeconfig secrets

```bash
# Create secrets containing kubeconfig for each cluster
kubectl create secret generic source-cluster-kubeconfig \
  --from-file=kubeconfig=/path/to/source-cluster.yaml

kubectl create secret generic dest-cluster-kubeconfig \
  --from-file=kubeconfig=/path/to/dest-cluster.yaml
```

### 2. Prepare the destination cluster

```bash
# Create namespace in destination
kubectl --context=dest-cluster create namespace production

# Create the headless service (required for StatefulSet)
kubectl --context=dest-cluster apply -f my-headless-service.yaml -n production
```

### 3. Create a migration

```yaml
apiVersion: migration.aqua.io/v1alpha1
kind: StatefulSetMigration
metadata:
  name: migrate-web
spec:
  migrationId: "web-migration-001"
  
  sourceCluster:
    kubeConfigSecret: source-cluster-kubeconfig
  sourceNamespace: production
  statefulSetName: web
  
  destCluster:
    kubeConfigSecret: dest-cluster-kubeconfig
  destNamespace: production
```

```bash
kubectl apply -f migration.yaml
```

### 4. Monitor progress

```bash
# Watch migration status
kubectl get statefulsetmigration migrate-web -w

# View detailed status
kubectl describe statefulsetmigration migrate-web
```

## Configuration

### StatefulSetMigration Spec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `migrationId` | string | Yes | Unique identifier for this migration |
| `sourceCluster.kubeConfigSecret` | string | Yes | Secret containing source cluster kubeconfig |
| `sourceNamespace` | string | Yes | Namespace in source cluster |
| `statefulSetName` | string | Yes | Name of StatefulSet to migrate |
| `destCluster.kubeConfigSecret` | string | Yes | Secret containing destination cluster kubeconfig |
| `destNamespace` | string | Yes | Namespace in destination cluster |
| `force` | bool | No | Ignore non-critical warnings (default: false) |
| `storageClassMapping` | map | No | Map source StorageClass to destination |
| `volumeDetachTimeout` | duration | No | Timeout for volume detachment (default: 5m) |
| `podReadyTimeout` | duration | No | Timeout for pod readiness (default: 10m) |

### Example with options

```yaml
apiVersion: migration.aqua.io/v1alpha1
kind: StatefulSetMigration
metadata:
  name: migrate-database
spec:
  migrationId: "db-migration-001"
  sourceCluster:
    kubeConfigSecret: old-cluster
  sourceNamespace: databases
  statefulSetName: postgres
  destCluster:
    kubeConfigSecret: new-cluster
  destNamespace: databases
  storageClassMapping:
    gp2: gp3  # Upgrade storage class during migration
  volumeDetachTimeout: 10m
  podReadyTimeout: 15m
```

## CLI Tool

The `storagemover` CLI is included for testing and debugging:

```bash
# Build the CLI
make build-cli

# Inspect a PV
./bin/storagemover inspect-pv --source-kubeconfig=~/.kube/config --name=pvc-xxx

# Test PV translation (dry-run)
./bin/storagemover translate \
  --source-kubeconfig=~/.kube/source.yaml \
  --namespace=default \
  --name=data-web-0 \
  --dest-namespace=production

# Wait for volume detachment
./bin/storagemover wait-detach \
  --volume-id=vol-0123456789abcdef0 \
  --aws-region=us-east-1
```

## Migration Phases

| Phase | Description |
|-------|-------------|
| `Pending` | Migration created, waiting to start |
| `PreFlightChecks` | Validating clusters, namespaces, and resources |
| `FreezingSource` | Setting PV reclaim policy to Retain, orphaning StatefulSet |
| `MigratingPods` | Migrating pods one by one (0 → N) |
| `Finalizing` | Cleaning up source cluster resources |
| `Completed` | Migration finished successfully |
| `Failed` | Error occurred, check `status.lastError` |

## Documentation

- [Architecture](docs/architecture.md) - Detailed design and workflow documentation
- [Contributing](CONTRIBUTING.md) - How to contribute to the project
- [Security](/.github/SECURITY.md) - Security policy and vulnerability reporting

## Development

### Prerequisites

- Go 1.22+
- Docker
- kubectl
- AWS credentials (for EBS operations)

### Build

```bash
# Build controller
make build

# Build CLI
make build-cli

# Build both
make build-all

# Run tests
make test

# Run linter
make lint
```

### Run locally

```bash
# Run controller against current kubeconfig
make run
```

### Docker

```bash
# Build image
make docker-build IMG=my-registry/aqua-service-controller:dev

# Push image
make docker-push IMG=my-registry/aqua-service-controller:dev
```

## Limitations

- **AWS EBS only** - Currently supports AWS EBS volumes (CSI and legacy)
- **Same region** - Source and destination clusters must be in the same AWS region
- **Single volume claim template** - Currently assumes StatefulSets have one volume claim template named "data"
- **Manual service setup** - Headless service must be created in destination before migration

## Roadmap

- [ ] Support for multiple volume claim templates
- [ ] Helm chart
- [ ] GCP Persistent Disk support
- [ ] Azure Disk support
- [ ] Automatic headless service creation
- [ ] Migration pause/resume commands
- [ ] Prometheus metrics

## License

[Apache License 2.0](LICENSE)

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.
