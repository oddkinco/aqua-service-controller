// Package main is the entry point for the Storage Mover CLI tool
// This tool is used for testing PV/PVC migration independently of the controller
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aqua-io/aqua-service-controller/internal/aws"
	"github.com/aqua-io/aqua-service-controller/internal/migration"
)

var (
	sourceKubeconfig string
	destKubeconfig   string
	awsRegion        string
	verbose          bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "storagemover",
		Short: "Storage Mover CLI - Test PV/PVC migration between clusters",
		Long: `Storage Mover is a CLI tool for testing the core migration logic
of the Aqua Service Controller. It allows you to:

- Inspect PVs and PVCs in source/destination clusters
- Translate PVs from source to destination format
- Wait for EBS volume detachment
- Create PV/PVC pairs in destination cluster

This tool is intended for testing and debugging the migration process.`,
	}

	// Global flags
	rootCmd.PersistentFlags().StringVar(&sourceKubeconfig, "source-kubeconfig", "", "Path to source cluster kubeconfig")
	rootCmd.PersistentFlags().StringVar(&destKubeconfig, "dest-kubeconfig", "", "Path to destination cluster kubeconfig")
	rootCmd.PersistentFlags().StringVar(&awsRegion, "aws-region", os.Getenv("AWS_REGION"), "AWS region for EBS operations")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")

	// Add commands
	rootCmd.AddCommand(inspectPVCmd())
	rootCmd.AddCommand(inspectPVCCmd())
	rootCmd.AddCommand(translateCmd())
	rootCmd.AddCommand(waitDetachCmd())
	rootCmd.AddCommand(migrateVolumeCmd())
	rootCmd.AddCommand(validateCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// inspectPVCmd inspects a PV in the source cluster
func inspectPVCmd() *cobra.Command {
	var pvName string

	cmd := &cobra.Command{
		Use:   "inspect-pv",
		Short: "Inspect a PersistentVolume in the source cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			client, err := getClient(sourceKubeconfig)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			pv := &corev1.PersistentVolume{}
			if err := client.Get(ctx, types.NamespacedName{Name: pvName}, pv); err != nil {
				return fmt.Errorf("failed to get PV: %w", err)
			}

			printPVInfo(pv)
			return nil
		},
	}

	cmd.Flags().StringVar(&pvName, "name", "", "Name of the PV to inspect")
	cmd.MarkFlagRequired("name")

	return cmd
}

// inspectPVCCmd inspects a PVC in the source cluster
func inspectPVCCmd() *cobra.Command {
	var namespace string
	var pvcName string

	cmd := &cobra.Command{
		Use:   "inspect-pvc",
		Short: "Inspect a PersistentVolumeClaim in the source cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			c, err := getClient(sourceKubeconfig)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			pvc := &corev1.PersistentVolumeClaim{}
			if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: pvcName}, pvc); err != nil {
				return fmt.Errorf("failed to get PVC: %w", err)
			}

			printPVCInfo(pvc)

			// Also get the bound PV
			if pvc.Spec.VolumeName != "" {
				pv := &corev1.PersistentVolume{}
				if err := c.Get(ctx, types.NamespacedName{Name: pvc.Spec.VolumeName}, pv); err == nil {
					fmt.Println("\nBound PV:")
					printPVInfo(pv)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Namespace of the PVC")
	cmd.Flags().StringVar(&pvcName, "name", "", "Name of the PVC to inspect")
	cmd.MarkFlagRequired("name")

	return cmd
}

// translateCmd translates a PV from source to destination format
func translateCmd() *cobra.Command {
	var namespace string
	var pvcName string
	var destNamespace string
	var destPVCName string

	cmd := &cobra.Command{
		Use:   "translate",
		Short: "Translate a PV/PVC from source to destination format",
		Long:  "Shows what the destination PV and PVC would look like without creating them",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			c, err := getClient(sourceKubeconfig)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Get source PVC
			pvc := &corev1.PersistentVolumeClaim{}
			if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: pvcName}, pvc); err != nil {
				return fmt.Errorf("failed to get PVC: %w", err)
			}

			// Get source PV
			pv := &corev1.PersistentVolume{}
			if err := c.Get(ctx, types.NamespacedName{Name: pvc.Spec.VolumeName}, pv); err != nil {
				return fmt.Errorf("failed to get PV: %w", err)
			}

			// Translate
			if destPVCName == "" {
				destPVCName = pvcName
			}
			result, err := migration.TranslatePV(pv, pvc, migration.PVTranslationConfig{
				DestNamespace:        destNamespace,
				DestPVCName:          destPVCName,
				PreserveNodeAffinity: true,
			})
			if err != nil {
				return fmt.Errorf("translation failed: %w", err)
			}

			fmt.Println("=== Translated PV ===")
			printPVInfo(result.PV)

			fmt.Println("\n=== Translated PVC ===")
			printPVCInfo(result.PVC)

			fmt.Printf("\nVolume ID: %s\n", result.VolumeID)
			fmt.Printf("Availability Zone: %s\n", result.AvailabilityZone)

			return nil
		},
	}

	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Source namespace")
	cmd.Flags().StringVar(&pvcName, "name", "", "Source PVC name")
	cmd.Flags().StringVar(&destNamespace, "dest-namespace", "", "Destination namespace")
	cmd.Flags().StringVar(&destPVCName, "dest-pvc-name", "", "Destination PVC name (defaults to source name)")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("dest-namespace")

	return cmd
}

// waitDetachCmd waits for an EBS volume to detach
func waitDetachCmd() *cobra.Command {
	var volumeID string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "wait-detach",
		Short: "Wait for an EBS volume to detach",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if awsRegion == "" {
				return fmt.Errorf("AWS region is required (--aws-region or AWS_REGION env var)")
			}

			ebsClient, err := aws.NewEBSClient(ctx, aws.EBSClientConfig{
				Region: awsRegion,
			})
			if err != nil {
				return fmt.Errorf("failed to create EBS client: %w", err)
			}

			// Get initial state
			info, err := ebsClient.GetVolumeInfo(ctx, volumeID)
			if err != nil {
				return fmt.Errorf("failed to get volume info: %w", err)
			}

			fmt.Printf("Volume: %s\n", volumeID)
			fmt.Printf("Initial state: %s\n", aws.VolumeStateString(info.State))
			fmt.Printf("AZ: %s\n", info.AvailabilityZone)

			if len(info.Attachments) > 0 {
				fmt.Println("Attachments:")
				for _, att := range info.Attachments {
					fmt.Printf("  - Instance: %s, Device: %s, State: %s\n",
						att.InstanceID, att.Device, att.State)
				}
			}

			fmt.Printf("\nWaiting for volume to become available (timeout: %v)...\n", timeout)

			err = ebsClient.WaitForVolumeDetach(ctx, volumeID, aws.WaitForVolumeDetachConfig{
				Timeout:      timeout,
				PollInterval: 5 * time.Second,
				OnPoll: func(info *aws.VolumeInfo) {
					if verbose {
						fmt.Printf("  State: %s\n", aws.VolumeStateString(info.State))
					}
				},
			})

			if err != nil {
				return fmt.Errorf("wait failed: %w", err)
			}

			fmt.Println("Volume is now available!")
			return nil
		},
	}

	cmd.Flags().StringVar(&volumeID, "volume-id", "", "EBS volume ID (e.g., vol-0123456789abcdef0)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Maximum time to wait")
	cmd.MarkFlagRequired("volume-id")

	return cmd
}

// migrateVolumeCmd performs a full volume migration
func migrateVolumeCmd() *cobra.Command {
	var sourceNamespace string
	var pvcName string
	var destNamespace string
	var destPVCName string
	var dryRun bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "migrate-volume",
		Short: "Migrate a single volume from source to destination cluster",
		Long: `Performs a complete volume migration:
1. Gets the source PVC and PV
2. Waits for the EBS volume to be available
3. Creates the PV and PVC in the destination cluster`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if awsRegion == "" {
				return fmt.Errorf("AWS region is required (--aws-region or AWS_REGION env var)")
			}

			sourceClient, err := getClient(sourceKubeconfig)
			if err != nil {
				return fmt.Errorf("failed to create source client: %w", err)
			}

			destClient, err := getClient(destKubeconfig)
			if err != nil {
				return fmt.Errorf("failed to create destination client: %w", err)
			}

			ebsClient, err := aws.NewEBSClient(ctx, aws.EBSClientConfig{
				Region: awsRegion,
			})
			if err != nil {
				return fmt.Errorf("failed to create EBS client: %w", err)
			}

			// Step 1: Get source PVC and PV
			fmt.Printf("Getting source PVC %s/%s...\n", sourceNamespace, pvcName)
			sourcePVC := &corev1.PersistentVolumeClaim{}
			if err := sourceClient.Get(ctx, types.NamespacedName{Namespace: sourceNamespace, Name: pvcName}, sourcePVC); err != nil {
				return fmt.Errorf("failed to get source PVC: %w", err)
			}

			sourcePV := &corev1.PersistentVolume{}
			if err := sourceClient.Get(ctx, types.NamespacedName{Name: sourcePVC.Spec.VolumeName}, sourcePV); err != nil {
				return fmt.Errorf("failed to get source PV: %w", err)
			}

			// Step 2: Translate
			if destPVCName == "" {
				destPVCName = pvcName
			}
			result, err := migration.TranslatePV(sourcePV, sourcePVC, migration.PVTranslationConfig{
				DestNamespace:        destNamespace,
				DestPVCName:          destPVCName,
				PreserveNodeAffinity: true,
			})
			if err != nil {
				return fmt.Errorf("translation failed: %w", err)
			}

			fmt.Printf("Volume ID: %s\n", result.VolumeID)
			fmt.Printf("AZ: %s\n", result.AvailabilityZone)

			// Step 3: Wait for volume to be available
			fmt.Printf("Waiting for volume to be available (timeout: %v)...\n", timeout)
			err = ebsClient.WaitForVolumeDetach(ctx, result.VolumeID, aws.WaitForVolumeDetachConfig{
				Timeout:      timeout,
				PollInterval: 5 * time.Second,
				OnPoll: func(info *aws.VolumeInfo) {
					fmt.Printf("  Volume state: %s\n", aws.VolumeStateString(info.State))
				},
			})
			if err != nil {
				return fmt.Errorf("volume not available: %w", err)
			}

			if dryRun {
				fmt.Println("\n[DRY RUN] Would create the following resources:")
				fmt.Printf("PV: %s\n", result.PV.Name)
				fmt.Printf("PVC: %s/%s\n", result.PVC.Namespace, result.PVC.Name)
				return nil
			}

			// Step 4: Create PV in destination
			fmt.Printf("Creating PV %s in destination...\n", result.PV.Name)
			if err := destClient.Create(ctx, result.PV); err != nil {
				return fmt.Errorf("failed to create destination PV: %w", err)
			}

			// Step 5: Create PVC in destination
			fmt.Printf("Creating PVC %s/%s in destination...\n", result.PVC.Namespace, result.PVC.Name)
			if err := destClient.Create(ctx, result.PVC); err != nil {
				// Clean up PV if PVC creation fails
				destClient.Delete(ctx, result.PV)
				return fmt.Errorf("failed to create destination PVC: %w", err)
			}

			fmt.Println("\nMigration complete!")
			fmt.Printf("PV: %s\n", result.PV.Name)
			fmt.Printf("PVC: %s/%s\n", result.PVC.Namespace, result.PVC.Name)

			return nil
		},
	}

	cmd.Flags().StringVarP(&sourceNamespace, "source-namespace", "s", "default", "Source namespace")
	cmd.Flags().StringVar(&pvcName, "pvc", "", "Source PVC name")
	cmd.Flags().StringVarP(&destNamespace, "dest-namespace", "d", "", "Destination namespace")
	cmd.Flags().StringVar(&destPVCName, "dest-pvc-name", "", "Destination PVC name (defaults to source name)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be created without actually creating")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Timeout for volume detachment")
	cmd.MarkFlagRequired("pvc")
	cmd.MarkFlagRequired("dest-namespace")
	cmd.MarkFlagRequired("source-kubeconfig")
	cmd.MarkFlagRequired("dest-kubeconfig")

	return cmd
}

// validateCmd validates a PV for migration
func validateCmd() *cobra.Command {
	var pvName string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a PV is suitable for migration",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			c, err := getClient(sourceKubeconfig)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			pv := &corev1.PersistentVolume{}
			if err := c.Get(ctx, types.NamespacedName{Name: pvName}, pv); err != nil {
				return fmt.Errorf("failed to get PV: %w", err)
			}

			if err := migration.ValidatePVForMigration(pv); err != nil {
				fmt.Printf("❌ Validation failed: %v\n", err)
				return err
			}

			fmt.Println("✅ PV is valid for migration")

			// Additional info
			if pv.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
				fmt.Printf("⚠️  Warning: Reclaim policy is %s (should be Retain for safe migration)\n",
					pv.Spec.PersistentVolumeReclaimPolicy)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&pvName, "name", "", "Name of the PV to validate")
	cmd.MarkFlagRequired("name")

	return cmd
}

// Helper functions

func getClient(kubeconfigPath string) (client.Client, error) {
	if kubeconfigPath == "" {
		kubeconfigPath = os.Getenv("KUBECONFIG")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	return client.New(config, client.Options{Scheme: scheme})
}

func printPVInfo(pv *corev1.PersistentVolume) {
	fmt.Printf("Name: %s\n", pv.Name)
	fmt.Printf("Status: %s\n", pv.Status.Phase)
	fmt.Printf("Capacity: %s\n", pv.Spec.Capacity.Storage().String())
	fmt.Printf("Access Modes: %v\n", pv.Spec.AccessModes)
	fmt.Printf("Reclaim Policy: %s\n", pv.Spec.PersistentVolumeReclaimPolicy)
	fmt.Printf("Storage Class: %s\n", pv.Spec.StorageClassName)

	if pv.Spec.ClaimRef != nil {
		fmt.Printf("Claim: %s/%s\n", pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name)
	}

	if pv.Spec.CSI != nil {
		fmt.Printf("CSI Driver: %s\n", pv.Spec.CSI.Driver)
		fmt.Printf("Volume Handle: %s\n", pv.Spec.CSI.VolumeHandle)
	} else if pv.Spec.AWSElasticBlockStore != nil {
		fmt.Printf("EBS Volume ID: %s\n", pv.Spec.AWSElasticBlockStore.VolumeID)
	}

	if pv.Spec.NodeAffinity != nil && pv.Spec.NodeAffinity.Required != nil {
		for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
			for _, expr := range term.MatchExpressions {
				if expr.Key == "topology.kubernetes.io/zone" {
					fmt.Printf("Zone: %v\n", expr.Values)
				}
			}
		}
	}
}

func printPVCInfo(pvc *corev1.PersistentVolumeClaim) {
	fmt.Printf("Name: %s\n", pvc.Name)
	fmt.Printf("Namespace: %s\n", pvc.Namespace)
	fmt.Printf("Status: %s\n", pvc.Status.Phase)
	fmt.Printf("Volume: %s\n", pvc.Spec.VolumeName)
	fmt.Printf("Access Modes: %v\n", pvc.Spec.AccessModes)

	if pvc.Spec.StorageClassName != nil {
		fmt.Printf("Storage Class: %s\n", *pvc.Spec.StorageClassName)
	}

	if req, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		fmt.Printf("Requested: %s\n", req.String())
	}
}
