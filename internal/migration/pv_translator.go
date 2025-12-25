// Package migration provides core migration logic for StatefulSet migrations
package migration

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PVTranslationConfig contains configuration for PV/PVC translation
type PVTranslationConfig struct {
	// DestNamespace is the target namespace in the destination cluster
	DestNamespace string

	// DestPVCName is the name of the PVC in the destination cluster
	// For StatefulSets, this follows the pattern: <volumeClaimTemplate>-<stsName>-<index>
	DestPVCName string

	// StorageClassMapping maps source StorageClass names to destination names
	// If empty or key not found, the original StorageClass name is used
	StorageClassMapping map[string]string

	// PreserveNodeAffinity determines whether to copy node affinity from source PV
	// This is critical for zone-constrained volumes like EBS
	PreserveNodeAffinity bool
}

// TranslationResult contains the translated PV and PVC for the destination cluster
type TranslationResult struct {
	// PV is the PersistentVolume to create in the destination cluster
	PV *corev1.PersistentVolume

	// PVC is the PersistentVolumeClaim to create in the destination cluster
	PVC *corev1.PersistentVolumeClaim

	// VolumeID is the cloud provider volume ID (e.g., AWS EBS volume ID)
	VolumeID string

	// AvailabilityZone is the zone where the volume resides
	AvailabilityZone string
}

// TranslatePV takes a source PV and creates the corresponding PV and PVC objects
// for the destination cluster. This is the core function for storage migration.
func TranslatePV(sourcePV *corev1.PersistentVolume, sourcePVC *corev1.PersistentVolumeClaim, config PVTranslationConfig) (*TranslationResult, error) {
	if sourcePV == nil {
		return nil, fmt.Errorf("source PV cannot be nil")
	}
	if sourcePVC == nil {
		return nil, fmt.Errorf("source PVC cannot be nil")
	}

	// Extract the EBS volume ID from the source PV
	volumeID, err := extractEBSVolumeID(sourcePV)
	if err != nil {
		return nil, fmt.Errorf("failed to extract EBS volume ID: %w", err)
	}

	// Extract availability zone from source PV
	az := extractAvailabilityZone(sourcePV)

	// Determine the destination StorageClass
	destStorageClass := getDestStorageClass(sourcePV.Spec.StorageClassName, config.StorageClassMapping)

	// Generate a unique PV name for the destination cluster
	destPVName := fmt.Sprintf("migrated-%s-%s", config.DestNamespace, config.DestPVCName)

	// Create the destination PV
	destPV := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: destPVName,
			Labels: map[string]string{
				"migration.aqua.io/migrated":        "true",
				"migration.aqua.io/source-pv":       sourcePV.Name,
				"migration.aqua.io/dest-namespace":  config.DestNamespace,
				"migration.aqua.io/dest-pvc":        config.DestPVCName,
			},
			Annotations: map[string]string{
				"migration.aqua.io/source-pv-uid": string(sourcePV.UID),
				"migration.aqua.io/volume-id":     volumeID,
			},
		},
		Spec: corev1.PersistentVolumeSpec{
			// Copy capacity from source
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: sourcePV.Spec.Capacity[corev1.ResourceStorage],
			},
			// Copy access modes from source
			AccessModes: sourcePV.Spec.AccessModes,
			// Set reclaim policy to Retain for safety during migration
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			// Set StorageClass
			StorageClassName: destStorageClass,
			// Pre-bind to the destination PVC
			ClaimRef: &corev1.ObjectReference{
				APIVersion: "v1",
				Kind:       "PersistentVolumeClaim",
				Namespace:  config.DestNamespace,
				Name:       config.DestPVCName,
			},
			// Copy the CSI volume source with the same volume handle
			PersistentVolumeSource: buildPVSource(sourcePV, volumeID),
		},
	}

	// Copy volume mode if set
	if sourcePV.Spec.VolumeMode != nil {
		destPV.Spec.VolumeMode = sourcePV.Spec.VolumeMode
	}

	// Preserve node affinity for topology-constrained volumes
	if config.PreserveNodeAffinity && sourcePV.Spec.NodeAffinity != nil {
		destPV.Spec.NodeAffinity = sourcePV.Spec.NodeAffinity.DeepCopy()
	} else if az != "" {
		// If no node affinity but we have AZ info, create node affinity
		destPV.Spec.NodeAffinity = buildNodeAffinityForZone(az)
	}

	// Create the destination PVC
	destPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.DestPVCName,
			Namespace: config.DestNamespace,
			Labels: map[string]string{
				"migration.aqua.io/migrated":   "true",
				"migration.aqua.io/source-pvc": sourcePVC.Name,
			},
			Annotations: map[string]string{
				"migration.aqua.io/source-pvc-uid": string(sourcePVC.UID),
				"migration.aqua.io/volume-id":      volumeID,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			// Copy access modes from source PVC
			AccessModes: sourcePVC.Spec.AccessModes,
			// Request the same storage size
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: sourcePVC.Spec.Resources.Requests[corev1.ResourceStorage],
				},
			},
			// Pre-bind to the destination PV
			VolumeName: destPVName,
		},
	}

	// Set StorageClass on PVC if specified
	if destStorageClass != "" {
		destPVC.Spec.StorageClassName = &destStorageClass
	}

	// Copy volume mode if set
	if sourcePVC.Spec.VolumeMode != nil {
		destPVC.Spec.VolumeMode = sourcePVC.Spec.VolumeMode
	}

	return &TranslationResult{
		PV:               destPV,
		PVC:              destPVC,
		VolumeID:         volumeID,
		AvailabilityZone: az,
	}, nil
}

// extractEBSVolumeID extracts the AWS EBS volume ID from a PV
func extractEBSVolumeID(pv *corev1.PersistentVolume) (string, error) {
	// Check CSI volume source first (modern approach)
	if pv.Spec.CSI != nil {
		if pv.Spec.CSI.Driver == "ebs.csi.aws.com" {
			// The volume handle is the EBS volume ID
			return pv.Spec.CSI.VolumeHandle, nil
		}
		return "", fmt.Errorf("unsupported CSI driver: %s (expected ebs.csi.aws.com)", pv.Spec.CSI.Driver)
	}

	// Check legacy AWS EBS volume source
	if pv.Spec.AWSElasticBlockStore != nil {
		// The VolumeID field contains the full ARN or volume ID
		volumeID := pv.Spec.AWSElasticBlockStore.VolumeID
		// Extract just the volume ID if it's a full path (aws://zone/vol-xxx)
		if strings.Contains(volumeID, "/") {
			parts := strings.Split(volumeID, "/")
			volumeID = parts[len(parts)-1]
		}
		return volumeID, nil
	}

	return "", fmt.Errorf("PV %s does not have an EBS volume source (neither CSI nor AWSElasticBlockStore)", pv.Name)
}

// extractAvailabilityZone extracts the availability zone from a PV's node affinity
func extractAvailabilityZone(pv *corev1.PersistentVolume) string {
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		return ""
	}

	for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		for _, expr := range term.MatchExpressions {
			// Check for the standard topology label
			if expr.Key == "topology.kubernetes.io/zone" && len(expr.Values) > 0 {
				return expr.Values[0]
			}
			// Check for the legacy label
			if expr.Key == "failure-domain.beta.kubernetes.io/zone" && len(expr.Values) > 0 {
				return expr.Values[0]
			}
		}
	}

	return ""
}

// buildNodeAffinityForZone creates a NodeAffinity that constrains the PV to a specific zone
func buildNodeAffinityForZone(zone string) *corev1.VolumeNodeAffinity {
	return &corev1.VolumeNodeAffinity{
		Required: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      "topology.kubernetes.io/zone",
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{zone},
						},
					},
				},
			},
		},
	}
}

// buildPVSource creates the PersistentVolumeSource for the destination PV
func buildPVSource(sourcePV *corev1.PersistentVolume, volumeID string) corev1.PersistentVolumeSource {
	// Prefer CSI (modern approach)
	if sourcePV.Spec.CSI != nil {
		return corev1.PersistentVolumeSource{
			CSI: &corev1.CSIPersistentVolumeSource{
				Driver:       sourcePV.Spec.CSI.Driver,
				VolumeHandle: volumeID,
				FSType:       sourcePV.Spec.CSI.FSType,
				ReadOnly:     sourcePV.Spec.CSI.ReadOnly,
				// Copy volume attributes if present
				VolumeAttributes: copyStringMap(sourcePV.Spec.CSI.VolumeAttributes),
			},
		}
	}

	// Fallback to legacy AWSElasticBlockStore
	if sourcePV.Spec.AWSElasticBlockStore != nil {
		return corev1.PersistentVolumeSource{
			AWSElasticBlockStore: &corev1.AWSElasticBlockStoreVolumeSource{
				VolumeID:  volumeID,
				FSType:    sourcePV.Spec.AWSElasticBlockStore.FSType,
				Partition: sourcePV.Spec.AWSElasticBlockStore.Partition,
				ReadOnly:  sourcePV.Spec.AWSElasticBlockStore.ReadOnly,
			},
		}
	}

	// This shouldn't happen if extractEBSVolumeID succeeded
	return corev1.PersistentVolumeSource{}
}

// getDestStorageClass returns the destination StorageClass name
func getDestStorageClass(sourceStorageClass string, mapping map[string]string) string {
	if mapping != nil {
		if dest, ok := mapping[sourceStorageClass]; ok {
			return dest
		}
	}
	return sourceStorageClass
}

// copyStringMap creates a copy of a string map
func copyStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

// GetPVCNameForStatefulSetPod returns the PVC name for a StatefulSet pod
// StatefulSet PVC naming convention: <volumeClaimTemplateName>-<stsName>-<index>
func GetPVCNameForStatefulSetPod(volumeClaimTemplateName, stsName string, index int) string {
	return fmt.Sprintf("%s-%s-%d", volumeClaimTemplateName, stsName, index)
}

// ValidatePVForMigration performs validation checks on a PV before migration
func ValidatePVForMigration(pv *corev1.PersistentVolume) error {
	if pv == nil {
		return fmt.Errorf("PV is nil")
	}

	// Check that PV is bound
	if pv.Status.Phase != corev1.VolumeBound {
		return fmt.Errorf("PV %s is not bound (phase: %s)", pv.Name, pv.Status.Phase)
	}

	// Check that it's an EBS volume
	if pv.Spec.CSI == nil && pv.Spec.AWSElasticBlockStore == nil {
		return fmt.Errorf("PV %s is not an EBS volume", pv.Name)
	}

	if pv.Spec.CSI != nil && pv.Spec.CSI.Driver != "ebs.csi.aws.com" {
		return fmt.Errorf("PV %s uses unsupported CSI driver: %s", pv.Name, pv.Spec.CSI.Driver)
	}

	return nil
}

// CalculateStorageSize returns the storage size from a PV or PVC
func CalculateStorageSize(pv *corev1.PersistentVolume) resource.Quantity {
	if pv == nil {
		return resource.Quantity{}
	}
	return pv.Spec.Capacity[corev1.ResourceStorage]
}
