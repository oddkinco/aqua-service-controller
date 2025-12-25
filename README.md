# Aqua Service Controller

### **1\. Architectural Overview**

**Objective:** Move a running StatefulSet (STS) and its Persistent Volumes (PVs) from Source Cluster (A) to Destination Cluster (B) with data integrity as the primary constraint.

Core Strategy: "Orphan & Adopt (Low-Index First)."  
Because StatefulSets must scale sequentially (0 → N), we cannot move random pods. We must dismantle Cluster A and build Cluster B in the exact order: web-0 → web-1 → web-n.  
**Infrastructure Assumptions:**

* **Topology:** Shared VPC or Peered VPCs (Same Region).  
* **Storage:** AWS EBS (GP2/GP3).  
* **Connectivity:** The Controller has kubectl access (admin context) to both clusters and ec2:DescribeVolumes permissions on AWS.

### ---

**2\. The Migration Custom Resource Definition (CRD)**

The controller will operate by reconciling a CRD located in a "Management Namespace" (likely on Cluster B or a separate management cluster).

**StatefulSetMigration Spec:**

Go

type StatefulSetMigrationSpec struct {  
    // Unique identifier for the migration  
    MigrationID string \`json:"migrationId"\`

    // Source Cluster details  
    SourceCluster ContextRef \`json:"sourceCluster"\`  
    SourceNamespace string   \`json:"sourceNamespace"\`  
    StatefulSetName string   \`json:"statefulSetName"\`

    // Destination Cluster details  
    DestCluster   ContextRef \`json:"destCluster"\`  
    DestNamespace string     \`json:"destNamespace"\`  
      
    // Configuration  
    Force bool \`json:"force"\` // Ignore non-critical pre-flight warnings  
}

type ContextRef struct {  
    KubeConfigSecret string \`json:"kubeConfigSecret"\` // Secret containing kubeconfig  
}

Status (State Machine):  
Pending → PreFlightChecks → FreezingSource → MigratingPods (Loop) → Finalizing → Completed (or Failed)

### ---

**3\. Detailed Workflow Stages**

#### **Phase 1: Pre-Flight Checks & Validation**

*Before modifying any resources, the controller validates the environment.*

1. **Cluster Connectivity:** Verify API access to both A and B.  
2. **Namespace Existence:** Ensure DestNamespace exists in Cluster B.  
3. **Conflict Check:** Ensure no STS with the same name exists in Cluster B.  
4. **Storage Class Match:** Ensure the StorageClass used in A exists in B (or map it).  
5. **Service Dependency:** Verify the Headless Service associated with the STS is already created in Cluster B (or create it now).  
6. **AWS Permissions:** Verify the controller can read volume states in AWS.

#### **Phase 2: The Freeze (Source Preparation)**

*Objective: Prepare Cluster A for disassembly without deleting data.*

1. **Patch PV Reclaim Policy:**  
   * List all PVCs for the STS in Cluster A.  
   * Find bound PVs.  
   * Patch all PVs to persistentVolumeReclaimPolicy: Retain. (**CRITICAL SAFETY STEP**)  
2. **Orphan the STS:**  
   * Delete the **StatefulSet Object** in Cluster A using DeletePropagationForeground or Orphan.  
   * *Result:* The STS definition is removed, but **Pods remain running** and **PVCs remain bound**.

#### **Phase 3: The Migration Loop (The "Swing")**

*The controller iterates from Index i \= 0 to Replicas \- 1\.*

**Step 3a: Terminate Source Pod**

* Delete Pod web-i in Cluster A.  
* Wait for Pod to disappear from API.

**Step 3b: Ensure Volume Detachment (The Hardest Part)**

* Retrieve the EBS Volume ID associated with the PV for web-i.  
* **AWS Polling Loop:** Call ec2.DescribeVolumes.  
* Block until State \== "available".  
* *Timeout Safety:* If this takes \> 5 mins, pause and alert (manual intervention may be required for "stuck" attachments).

**Step 3c: Hydrate Destination Storage**

* **Create PV in B:** Construct a PV object in Cluster B.  
  * volumeHandle: The AWS Volume ID from Step 3b.  
  * claimRef: namespace: DestNamespace, name: data-web-i.  
* **Create PVC in B:** Construct a PVC object in Cluster B.  
  * volumeName: The name of the PV created above.  
  * This pre-binds the PVC to the existing physical disk.

**Step 3d: Scale Destination**

* **First Iteration (i=0):** Create the StatefulSet definition in Cluster B with replicas: 1\.  
* **Subsequent Iterations:** Patch STS in Cluster B to replicas: i \+ 1\.

**Step 3e: Health Check**

* Wait for web-i in Cluster B to reach Ready status.  
* If Ready, increment i and repeat loop.

#### **Phase 4: Finalization**

1. **Network Cutover:** If using an external LoadBalancer or Ingress, update it to point to Cluster B services.  
2. **Garbage Collection (Source):**  
   * Delete the old PVCs in Cluster A.  
   * Delete the old PV objects in Cluster A.  
   * (Note: Because we set ReclaimPolicy to Retain in Phase 2, this deletes the K8s objects but leaves the EBS volumes alone, which are now attached to Cluster B).

### ---

**4\. Technical Implementation Specifics (Go)**

#### **AWS Volume Polling Logic**

Do not rely on Kubernetes PV status alone. It is often eventually consistent. Query the source of truth.

Go

func (r \*Reconciler) waitForEBSDetach(ctx context.Context, volumeID string) error {  
    ticker := time.NewTicker(5 \* time.Second)  
    defer ticker.Stop()  
      
    for {  
        select {  
        case \<-ctx.Done():  
            return ctx.Err()  
        case \<-ticker.C:  
            resp, err := r.ec2Client.DescribeVolumes(ctx, \&ec2.DescribeVolumesInput{  
                VolumeIds: \[\]string{volumeID},  
            })  
            if err \!= nil {  
                return err  
            }  
              
            if len(resp.Volumes) \> 0 && resp.Volumes\[0\].State \== types.VolumeStateAvailable {  
                return nil // Success, volume is ready to be attached to B  
            }  
            // Log: Still attached... waiting  
        }  
    }  
}

#### **Reconstructing the PV**

When creating the PV in Cluster B, you must replicate the **Node Affinity** if your clusters are topology-aware.

Go

// When creating PV in Cluster B  
nodeAffinity := \&corev1.VolumeNodeAffinity{  
    Required: \&corev1.NodeSelector{  
        NodeSelectorTerms: \[\]corev1.NodeSelectorTerm{  
            {  
                MatchExpressions: \[\]corev1.NodeSelectorRequirement{  
                    {  
                        Key:      "topology.kubernetes.io/zone",  
                        Operator: corev1.NodeSelectorOpIn,  
                        Values:   \[\]string{originalZone}, // Must match volume's AZ  
                    },  
                },  
            },  
        },  
    },  
}

### **5\. Failure & Rollback Strategy**

Since we are moving state, "Rollback" is actually just "Migrating back."

**Scenario: Failure at Index 2 (0 and 1 are in B; 2, 3, 4 are in A)**

1. **Pause:** The controller stops. It reports Status: Failed.  
2. **Operator Decision:** A human must decide to "Roll Forward" (fix the error) or "Roll Back".  
3. **Rollback Procedure:**  
   * Scale STS in Cluster B to 0\.  
   * Delete STS in B.  
   * Reverse the "Hydrate Storage" logic: Delete PV/PVCs in B (Retain policy\!), recreate them in A.  
   * Re-create STS in A with replicas: 5\.

### **6\. Recommended Development Phases**

1. **Phase 1 (The Storage Mover):** Write a standalone Go CLI tool that takes a PVC from Cluster A, detaches it, and creates the valid PV/PVC pair in Cluster B. **Test this extensively.** If this fails, the whole controller fails.  
2. **Phase 2 (The Controller Skeleton):** Build the CRD and the basic state machine logic (Pending \-\> Success) without doing actual work.  
3. **Phase 3 (Integration):** Plug the storage mover logic into the controller loop.

### **7\. Immediate Next Step for You**

I recommend starting with **Phase 1** (The Storage Mover CLI).

Would you like me to generate the **Go code for the PV/PVC Translation function**? This function needs to take a corev1.PersistentVolume from Cluster A and return the corev1.PersistentVolume and corev1.PersistentVolumeClaim structs required for Cluster B.
