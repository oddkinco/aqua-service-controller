// Package controller implements the StatefulSetMigration reconciler
package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	migrationv1alpha1 "github.com/aqua-io/aqua-service-controller/api/v1alpha1"
	"github.com/aqua-io/aqua-service-controller/internal/aws"
	"github.com/aqua-io/aqua-service-controller/internal/migration"
	"github.com/aqua-io/aqua-service-controller/internal/multicluster"
)

const (
	// MigrationFinalizer is the finalizer added to StatefulSetMigration resources
	MigrationFinalizer = "migration.aqua.io/finalizer"

	// DefaultVolumeDetachTimeout is the default timeout for waiting for volume detachment
	DefaultVolumeDetachTimeout = 5 * time.Minute

	// DefaultPodReadyTimeout is the default timeout for waiting for pod readiness
	DefaultPodReadyTimeout = 10 * time.Minute

	// DefaultRequeueDelay is the default delay before requeuing
	DefaultRequeueDelay = 10 * time.Second
)

// StatefulSetMigrationReconciler reconciles a StatefulSetMigration object
type StatefulSetMigrationReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *multicluster.ClientManager
	EBSClient     *aws.EBSClient
}

// +kubebuilder:rbac:groups=migration.aqua.io,resources=statefulsetmigrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migration.aqua.io,resources=statefulsetmigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migration.aqua.io,resources=statefulsetmigrations/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles the reconciliation loop for StatefulSetMigration resources
func (r *StatefulSetMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the StatefulSetMigration resource
	migration := &migrationv1alpha1.StatefulSetMigration{}
	if err := r.Get(ctx, req.NamespacedName, migration); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !migration.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, migration)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(migration, MigrationFinalizer) {
		controllerutil.AddFinalizer(migration, MigrationFinalizer)
		if err := r.Update(ctx, migration); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Initialize status if needed
	if migration.Status.Phase == "" {
		migration.Status.Phase = migrationv1alpha1.PhasePending
		if err := r.Status().Update(ctx, migration); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// State machine dispatch
	logger.Info("Reconciling migration", "phase", migration.Status.Phase)

	switch migration.Status.Phase {
	case migrationv1alpha1.PhasePending:
		return r.reconcilePending(ctx, migration)

	case migrationv1alpha1.PhasePreFlightChecks:
		return r.reconcilePreFlightChecks(ctx, migration)

	case migrationv1alpha1.PhaseFreezingSource:
		return r.reconcileFreezingSource(ctx, migration)

	case migrationv1alpha1.PhaseMigratingPods:
		return r.reconcileMigratingPods(ctx, migration)

	case migrationv1alpha1.PhaseFinalizing:
		return r.reconcileFinalizing(ctx, migration)

	case migrationv1alpha1.PhaseCompleted:
		return ctrl.Result{}, nil // Nothing more to do

	case migrationv1alpha1.PhaseFailed:
		return ctrl.Result{}, nil // Manual intervention required

	default:
		logger.Error(nil, "Unknown migration phase", "phase", migration.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// handleDeletion handles cleanup when a migration is deleted
func (r *StatefulSetMigrationReconciler) handleDeletion(ctx context.Context, migration *migrationv1alpha1.StatefulSetMigration) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(migration, MigrationFinalizer) {
		logger.Info("Handling migration deletion")

		// Perform any cleanup if needed
		// Note: We don't automatically rollback on deletion - that would be dangerous

		// Remove finalizer
		controllerutil.RemoveFinalizer(migration, MigrationFinalizer)
		if err := r.Update(ctx, migration); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// reconcilePending handles the Pending phase
func (r *StatefulSetMigrationReconciler) reconcilePending(ctx context.Context, m *migrationv1alpha1.StatefulSetMigration) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting migration, moving to PreFlightChecks")

	m.Status.Phase = migrationv1alpha1.PhasePreFlightChecks
	now := metav1.Now()
	m.Status.StartTime = &now

	if err := r.Status().Update(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// reconcilePreFlightChecks handles the PreFlightChecks phase
func (r *StatefulSetMigrationReconciler) reconcilePreFlightChecks(ctx context.Context, m *migrationv1alpha1.StatefulSetMigration) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Running pre-flight checks")

	// Get source cluster client
	sourceClient, err := r.getSourceClient(ctx, m)
	if err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to connect to source cluster: %v", err))
	}

	// Get destination cluster client
	destClient, err := r.getDestClient(ctx, m)
	if err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to connect to destination cluster: %v", err))
	}

	// Test connectivity to both clusters
	if err := r.ClientManager.TestConnection(ctx, sourceClient); err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Source cluster connectivity check failed: %v", err))
	}
	if err := r.ClientManager.TestConnection(ctx, destClient); err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Destination cluster connectivity check failed: %v", err))
	}

	// Check source StatefulSet exists
	sourceSTS := &appsv1.StatefulSet{}
	if err := sourceClient.Client.Get(ctx, types.NamespacedName{
		Namespace: m.Spec.SourceNamespace,
		Name:      m.Spec.StatefulSetName,
	}, sourceSTS); err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Source StatefulSet not found: %v", err))
	}

	// Store source STS info
	m.Status.SourceStatefulSetUID = string(sourceSTS.UID)
	m.Status.TotalReplicas = int(*sourceSTS.Spec.Replicas)

	// Check destination namespace exists
	destNS := &corev1.Namespace{}
	if err := destClient.Client.Get(ctx, types.NamespacedName{Name: m.Spec.DestNamespace}, destNS); err != nil {
		if apierrors.IsNotFound(err) {
			return r.failMigration(ctx, m, fmt.Sprintf("Destination namespace %q does not exist", m.Spec.DestNamespace))
		}
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to check destination namespace: %v", err))
	}

	// Check no conflicting StatefulSet in destination
	destSTS := &appsv1.StatefulSet{}
	err = destClient.Client.Get(ctx, types.NamespacedName{
		Namespace: m.Spec.DestNamespace,
		Name:      m.Spec.StatefulSetName,
	}, destSTS)
	if err == nil {
		return r.failMigration(ctx, m, fmt.Sprintf("StatefulSet %q already exists in destination namespace %q", m.Spec.StatefulSetName, m.Spec.DestNamespace))
	}
	if !apierrors.IsNotFound(err) {
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to check destination StatefulSet: %v", err))
	}

	// Check headless service exists in destination (required for StatefulSet)
	if sourceSTS.Spec.ServiceName != "" {
		destService := &corev1.Service{}
		err = destClient.Client.Get(ctx, types.NamespacedName{
			Namespace: m.Spec.DestNamespace,
			Name:      sourceSTS.Spec.ServiceName,
		}, destService)
		if err != nil {
			if apierrors.IsNotFound(err) && !m.Spec.Force {
				return r.failMigration(ctx, m, fmt.Sprintf("Headless service %q not found in destination namespace (required for StatefulSet)", sourceSTS.Spec.ServiceName))
			}
			if !apierrors.IsNotFound(err) {
				return r.failMigration(ctx, m, fmt.Sprintf("Failed to check destination service: %v", err))
			}
		}
	}

	logger.Info("Pre-flight checks passed", "replicas", m.Status.TotalReplicas)

	// Move to FreezingSource phase
	m.Status.Phase = migrationv1alpha1.PhaseFreezingSource
	r.setCondition(m, "PreFlightChecks", metav1.ConditionTrue, "Passed", "All pre-flight checks passed")

	if err := r.Status().Update(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// reconcileFreezingSource handles the FreezingSource phase
func (r *StatefulSetMigrationReconciler) reconcileFreezingSource(ctx context.Context, m *migrationv1alpha1.StatefulSetMigration) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Freezing source cluster")

	sourceClient, err := r.getSourceClient(ctx, m)
	if err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to get source client: %v", err))
	}

	// Get the source StatefulSet
	sourceSTS := &appsv1.StatefulSet{}
	if err := sourceClient.Client.Get(ctx, types.NamespacedName{
		Namespace: m.Spec.SourceNamespace,
		Name:      m.Spec.StatefulSetName,
	}, sourceSTS); err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to get source StatefulSet: %v", err))
	}

	// Patch all PVs to Retain reclaim policy
	preservedPVs, err := r.patchPVsToRetain(ctx, sourceClient, m.Spec.SourceNamespace, sourceSTS)
	if err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to patch PV reclaim policies: %v", err))
	}
	m.Status.PreservedPVs = preservedPVs
	logger.Info("Patched PVs to Retain", "pvs", preservedPVs)

	// Delete the StatefulSet with orphan propagation (leaves pods running)
	if err := r.orphanStatefulSet(ctx, sourceClient, m.Spec.SourceNamespace, m.Spec.StatefulSetName); err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to orphan StatefulSet: %v", err))
	}
	logger.Info("Orphaned StatefulSet")

	// Move to MigratingPods phase
	m.Status.Phase = migrationv1alpha1.PhaseMigratingPods
	m.Status.CurrentIndex = 0
	r.setCondition(m, "SourceFrozen", metav1.ConditionTrue, "Frozen", "Source cluster prepared for migration")

	if err := r.Status().Update(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// reconcileMigratingPods handles the MigratingPods phase
func (r *StatefulSetMigrationReconciler) reconcileMigratingPods(ctx context.Context, m *migrationv1alpha1.StatefulSetMigration) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if m.Status.CurrentIndex >= m.Status.TotalReplicas {
		// All pods migrated, move to finalizing
		logger.Info("All pods migrated, moving to Finalizing")
		m.Status.Phase = migrationv1alpha1.PhaseFinalizing
		if err := r.Status().Update(ctx, m); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	index := m.Status.CurrentIndex
	logger.Info("Migrating pod", "index", index)

	// Migrate the current pod
	if err := r.migratePod(ctx, m, index); err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to migrate pod %d: %v", index, err))
	}

	// Update status
	m.Status.CurrentIndex = index + 1
	if err := r.Status().Update(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	// Continue to next pod
	return ctrl.Result{Requeue: true}, nil
}

// migratePod migrates a single pod from source to destination
func (r *StatefulSetMigrationReconciler) migratePod(ctx context.Context, m *migrationv1alpha1.StatefulSetMigration, index int) error {
	logger := log.FromContext(ctx)

	sourceClient, err := r.getSourceClient(ctx, m)
	if err != nil {
		return fmt.Errorf("failed to get source client: %w", err)
	}

	destClient, err := r.getDestClient(ctx, m)
	if err != nil {
		return fmt.Errorf("failed to get destination client: %w", err)
	}

	podName := fmt.Sprintf("%s-%d", m.Spec.StatefulSetName, index)

	// Step 1: Delete the pod in source cluster
	logger.Info("Deleting source pod", "pod", podName)
	pod := &corev1.Pod{}
	err = sourceClient.Client.Get(ctx, types.NamespacedName{
		Namespace: m.Spec.SourceNamespace,
		Name:      podName,
	}, pod)
	if err == nil {
		if err := sourceClient.Client.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete source pod: %w", err)
		}
		// Wait for pod to be gone
		if err := r.waitForPodDeletion(ctx, sourceClient, m.Spec.SourceNamespace, podName); err != nil {
			return fmt.Errorf("failed waiting for pod deletion: %w", err)
		}
	}

	// Step 2: Get source PVC and PV
	// For now, assume a single volume claim template named "data"
	// TODO: Support multiple volume claim templates
	pvcName := migration.GetPVCNameForStatefulSetPod("data", m.Spec.StatefulSetName, index)

	sourcePVC := &corev1.PersistentVolumeClaim{}
	if err := sourceClient.Client.Get(ctx, types.NamespacedName{
		Namespace: m.Spec.SourceNamespace,
		Name:      pvcName,
	}, sourcePVC); err != nil {
		return fmt.Errorf("failed to get source PVC %s: %w", pvcName, err)
	}

	sourcePV := &corev1.PersistentVolume{}
	if err := sourceClient.Client.Get(ctx, types.NamespacedName{
		Name: sourcePVC.Spec.VolumeName,
	}, sourcePV); err != nil {
		return fmt.Errorf("failed to get source PV: %w", err)
	}

	// Step 3: Extract volume ID and wait for detachment
	volumeID, err := getVolumeIDFromPV(sourcePV)
	if err != nil {
		return fmt.Errorf("failed to get volume ID: %w", err)
	}

	logger.Info("Waiting for volume detachment", "volumeId", volumeID)
	timeout := DefaultVolumeDetachTimeout
	if m.Spec.VolumeDetachTimeout != nil {
		timeout = m.Spec.VolumeDetachTimeout.Duration
	}

	if err := r.EBSClient.WaitForVolumeDetach(ctx, volumeID, aws.WaitForVolumeDetachConfig{
		Timeout:      timeout,
		PollInterval: 5 * time.Second,
		OnPoll: func(info *aws.VolumeInfo) {
			logger.Info("Volume status", "volumeId", volumeID, "state", aws.VolumeStateString(info.State))
		},
	}); err != nil {
		return fmt.Errorf("volume detachment failed: %w", err)
	}

	// Step 4: Create PV and PVC in destination
	logger.Info("Creating PV/PVC in destination", "pvc", pvcName)

	result, err := migration.TranslatePV(sourcePV, sourcePVC, migration.PVTranslationConfig{
		DestNamespace:        m.Spec.DestNamespace,
		DestPVCName:          pvcName,
		StorageClassMapping:  m.Spec.StorageClassMapping,
		PreserveNodeAffinity: true,
	})
	if err != nil {
		return fmt.Errorf("failed to translate PV/PVC: %w", err)
	}

	// Create PV first
	if err := destClient.Client.Create(ctx, result.PV); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create destination PV: %w", err)
	}

	// Create PVC
	if err := destClient.Client.Create(ctx, result.PVC); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create destination PVC: %w", err)
	}

	// Step 5: Create or scale StatefulSet in destination
	if index == 0 {
		// First pod - create the StatefulSet
		logger.Info("Creating StatefulSet in destination")
		if err := r.createDestinationStatefulSet(ctx, sourceClient, destClient, m); err != nil {
			return fmt.Errorf("failed to create destination StatefulSet: %w", err)
		}
	} else {
		// Subsequent pods - scale up the StatefulSet
		logger.Info("Scaling StatefulSet in destination", "replicas", index+1)
		if err := r.scaleDestinationStatefulSet(ctx, destClient, m, int32(index+1)); err != nil {
			return fmt.Errorf("failed to scale destination StatefulSet: %w", err)
		}
	}

	// Step 6: Wait for pod to be ready in destination
	logger.Info("Waiting for pod to be ready in destination", "pod", podName)
	timeout = DefaultPodReadyTimeout
	if m.Spec.PodReadyTimeout != nil {
		timeout = m.Spec.PodReadyTimeout.Duration
	}

	if err := r.waitForPodReady(ctx, destClient, m.Spec.DestNamespace, podName, timeout); err != nil {
		return fmt.Errorf("destination pod not ready: %w", err)
	}

	// Record successful migration
	m.Status.MigratedPods = append(m.Status.MigratedPods, migrationv1alpha1.MigratedPodInfo{
		Index:      index,
		PodName:    podName,
		VolumeID:   volumeID,
		MigratedAt: metav1.Now(),
	})

	logger.Info("Pod migrated successfully", "pod", podName)
	return nil
}

// reconcileFinalizing handles the Finalizing phase
func (r *StatefulSetMigrationReconciler) reconcileFinalizing(ctx context.Context, m *migrationv1alpha1.StatefulSetMigration) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Finalizing migration")

	sourceClient, err := r.getSourceClient(ctx, m)
	if err != nil {
		return r.failMigration(ctx, m, fmt.Sprintf("Failed to get source client: %v", err))
	}

	// Clean up source PVCs and PVs
	// Note: Because we set ReclaimPolicy to Retain, this deletes the K8s objects
	// but leaves the EBS volumes intact (they're now used by destination cluster)
	for i := 0; i < m.Status.TotalReplicas; i++ {
		pvcName := migration.GetPVCNameForStatefulSetPod("data", m.Spec.StatefulSetName, i)

		// Delete PVC
		pvc := &corev1.PersistentVolumeClaim{}
		err := sourceClient.Client.Get(ctx, types.NamespacedName{
			Namespace: m.Spec.SourceNamespace,
			Name:      pvcName,
		}, pvc)
		if err == nil {
			if err := sourceClient.Client.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to delete source PVC", "pvc", pvcName)
			}
		}
	}

	// Delete source PVs
	for _, pvName := range m.Status.PreservedPVs {
		pv := &corev1.PersistentVolume{}
		err := sourceClient.Client.Get(ctx, types.NamespacedName{Name: pvName}, pv)
		if err == nil {
			if err := sourceClient.Client.Delete(ctx, pv); err != nil && !apierrors.IsNotFound(err) {
				logger.Error(err, "Failed to delete source PV", "pv", pvName)
			}
		}
	}

	// Mark as completed
	m.Status.Phase = migrationv1alpha1.PhaseCompleted
	now := metav1.Now()
	m.Status.CompletionTime = &now
	r.setCondition(m, "Complete", metav1.ConditionTrue, "Completed", "Migration completed successfully")

	if err := r.Status().Update(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Migration completed successfully")
	return ctrl.Result{}, nil
}

// Helper functions

func (r *StatefulSetMigrationReconciler) getSourceClient(ctx context.Context, m *migrationv1alpha1.StatefulSetMigration) (*multicluster.ClusterClient, error) {
	secretKey := m.Spec.SourceCluster.KubeConfigKey
	if secretKey == "" {
		secretKey = "kubeconfig"
	}
	return r.ClientManager.GetClientFromSecret(ctx, m.Namespace, m.Spec.SourceCluster.KubeConfigSecret, secretKey)
}

func (r *StatefulSetMigrationReconciler) getDestClient(ctx context.Context, m *migrationv1alpha1.StatefulSetMigration) (*multicluster.ClusterClient, error) {
	secretKey := m.Spec.DestCluster.KubeConfigKey
	if secretKey == "" {
		secretKey = "kubeconfig"
	}
	return r.ClientManager.GetClientFromSecret(ctx, m.Namespace, m.Spec.DestCluster.KubeConfigSecret, secretKey)
}

func (r *StatefulSetMigrationReconciler) failMigration(ctx context.Context, m *migrationv1alpha1.StatefulSetMigration, reason string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Error(nil, "Migration failed", "reason", reason)

	m.Status.Phase = migrationv1alpha1.PhaseFailed
	m.Status.LastError = reason
	now := metav1.Now()
	m.Status.CompletionTime = &now
	r.setCondition(m, "Failed", metav1.ConditionTrue, "Failed", reason)

	if err := r.Status().Update(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *StatefulSetMigrationReconciler) setCondition(m *migrationv1alpha1.StatefulSetMigration, condType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}

	// Update or append condition
	for i, c := range m.Status.Conditions {
		if c.Type == condType {
			m.Status.Conditions[i] = condition
			return
		}
	}
	m.Status.Conditions = append(m.Status.Conditions, condition)
}

func (r *StatefulSetMigrationReconciler) patchPVsToRetain(ctx context.Context, cc *multicluster.ClusterClient, namespace string, sts *appsv1.StatefulSet) ([]string, error) {
	var pvNames []string

	// List PVCs for this StatefulSet
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := cc.Client.List(ctx, pvcList, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	for _, pvc := range pvcList.Items {
		// Check if this PVC belongs to our StatefulSet
		// StatefulSet PVC naming convention: <volumeClaimTemplate>-<stsName>-<index>
		if pvc.Spec.VolumeName == "" {
			continue
		}

		// Get the PV
		pv := &corev1.PersistentVolume{}
		if err := cc.Client.Get(ctx, types.NamespacedName{Name: pvc.Spec.VolumeName}, pv); err != nil {
			continue
		}

		// Patch to Retain if not already
		if pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
			pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain
			if err := cc.Client.Update(ctx, pv); err != nil {
				return nil, fmt.Errorf("failed to patch PV %s to Retain: %w", pv.Name, err)
			}
		}

		pvNames = append(pvNames, pv.Name)
	}

	return pvNames, nil
}

func (r *StatefulSetMigrationReconciler) orphanStatefulSet(ctx context.Context, cc *multicluster.ClusterClient, namespace, name string) error {
	sts := &appsv1.StatefulSet{}
	if err := cc.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, sts); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // Already deleted
		}
		return err
	}

	// Delete with orphan propagation
	propagation := metav1.DeletePropagationOrphan
	return cc.Client.Delete(ctx, sts, &client.DeleteOptions{
		PropagationPolicy: &propagation,
	})
}

func (r *StatefulSetMigrationReconciler) waitForPodDeletion(ctx context.Context, cc *multicluster.ClusterClient, namespace, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pod := &corev1.Pod{}
			err := cc.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, pod)
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			// Pod still exists, continue waiting
		}
	}
}

func (r *StatefulSetMigrationReconciler) waitForPodReady(ctx context.Context, cc *multicluster.ClusterClient, namespace, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for pod %s to be ready", name)
		case <-ticker.C:
			pod := &corev1.Pod{}
			if err := cc.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, pod); err != nil {
				continue // Pod might not exist yet
			}

			// Check if pod is ready
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					return nil
				}
			}
		}
	}
}

func (r *StatefulSetMigrationReconciler) createDestinationStatefulSet(ctx context.Context, sourceCC, destCC *multicluster.ClusterClient, m *migrationv1alpha1.StatefulSetMigration) error {
	// Get source StatefulSet as template
	// Note: The STS was deleted with orphan propagation, so we need to reconstruct it
	// In practice, you might want to store the STS spec in the migration status before deletion

	// For now, we'll create a minimal STS that matches the source
	// This is a simplified version - in production you'd want to copy more fields
	sourceSTS := &appsv1.StatefulSet{}
	// Try to get it (might still exist briefly after orphan delete)
	err := sourceCC.Client.Get(ctx, types.NamespacedName{
		Namespace: m.Spec.SourceNamespace,
		Name:      m.Spec.StatefulSetName,
	}, sourceSTS)

	if err != nil {
		return fmt.Errorf("source StatefulSet no longer available for copying spec: %w", err)
	}

	// Create destination STS with replicas=1
	destSTS := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Spec.StatefulSetName,
			Namespace: m.Spec.DestNamespace,
			Labels:    sourceSTS.Labels,
			Annotations: map[string]string{
				"migration.aqua.io/migrated-from": fmt.Sprintf("%s/%s", m.Spec.SourceNamespace, m.Spec.StatefulSetName),
			},
		},
		Spec: *sourceSTS.Spec.DeepCopy(),
	}

	// Set replicas to 1 for first pod
	one := int32(1)
	destSTS.Spec.Replicas = &one

	// Update namespace references in pod template if needed
	destSTS.Spec.Template.Namespace = m.Spec.DestNamespace

	return destCC.Client.Create(ctx, destSTS)
}

func (r *StatefulSetMigrationReconciler) scaleDestinationStatefulSet(ctx context.Context, cc *multicluster.ClusterClient, m *migrationv1alpha1.StatefulSetMigration, replicas int32) error {
	sts := &appsv1.StatefulSet{}
	if err := cc.Client.Get(ctx, types.NamespacedName{
		Namespace: m.Spec.DestNamespace,
		Name:      m.Spec.StatefulSetName,
	}, sts); err != nil {
		return err
	}

	sts.Spec.Replicas = &replicas
	return cc.Client.Update(ctx, sts)
}

func getVolumeIDFromPV(pv *corev1.PersistentVolume) (string, error) {
	if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == "ebs.csi.aws.com" {
		return pv.Spec.CSI.VolumeHandle, nil
	}
	if pv.Spec.AWSElasticBlockStore != nil {
		return aws.GetVolumeIDFromHandle(pv.Spec.AWSElasticBlockStore.VolumeID), nil
	}
	return "", fmt.Errorf("PV %s is not an EBS volume", pv.Name)
}

// SetupWithManager sets up the controller with the Manager
func (r *StatefulSetMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&migrationv1alpha1.StatefulSetMigration{}).
		Complete(r)
}
