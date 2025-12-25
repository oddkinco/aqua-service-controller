package migration

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTranslatePV(t *testing.T) {
	tests := []struct {
		name      string
		sourcePV  *corev1.PersistentVolume
		sourcePVC *corev1.PersistentVolumeClaim
		config    PVTranslationConfig
		wantErr   bool
		validate  func(*testing.T, *TranslationResult)
	}{
		{
			name: "basic CSI EBS volume translation",
			sourcePV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pvc-12345",
					UID:  "pv-uid-12345",
				},
				Spec: corev1.PersistentVolumeSpec{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						CSI: &corev1.CSIPersistentVolumeSource{
							Driver:       "ebs.csi.aws.com",
							VolumeHandle: "vol-0123456789abcdef0",
							FSType:       "ext4",
						},
					},
					StorageClassName:              "gp3",
					PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
					NodeAffinity: &corev1.VolumeNodeAffinity{
						Required: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      "topology.kubernetes.io/zone",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"us-east-1a"},
										},
									},
								},
							},
						},
					},
					ClaimRef: &corev1.ObjectReference{
						Namespace: "source-ns",
						Name:      "data-web-0",
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeBound,
				},
			},
			sourcePVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "data-web-0",
					Namespace: "source-ns",
					UID:       "pvc-uid-12345",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
					VolumeName: "pvc-12345",
				},
			},
			config: PVTranslationConfig{
				DestNamespace:        "dest-ns",
				DestPVCName:          "data-web-0",
				PreserveNodeAffinity: true,
			},
			wantErr: false,
			validate: func(t *testing.T, result *TranslationResult) {
				// Check PV
				if result.PV == nil {
					t.Fatal("expected PV to be set")
				}
				if result.PV.Spec.CSI == nil {
					t.Fatal("expected CSI source to be set")
				}
				if result.PV.Spec.CSI.VolumeHandle != "vol-0123456789abcdef0" {
					t.Errorf("expected volume handle vol-0123456789abcdef0, got %s", result.PV.Spec.CSI.VolumeHandle)
				}
				if result.PV.Spec.PersistentVolumeReclaimPolicy != corev1.PersistentVolumeReclaimRetain {
					t.Errorf("expected Retain reclaim policy, got %s", result.PV.Spec.PersistentVolumeReclaimPolicy)
				}
				if result.PV.Spec.ClaimRef.Namespace != "dest-ns" {
					t.Errorf("expected claim ref namespace dest-ns, got %s", result.PV.Spec.ClaimRef.Namespace)
				}
				if result.PV.Spec.NodeAffinity == nil {
					t.Error("expected node affinity to be preserved")
				}

				// Check PVC
				if result.PVC == nil {
					t.Fatal("expected PVC to be set")
				}
				if result.PVC.Namespace != "dest-ns" {
					t.Errorf("expected PVC namespace dest-ns, got %s", result.PVC.Namespace)
				}
				if result.PVC.Name != "data-web-0" {
					t.Errorf("expected PVC name data-web-0, got %s", result.PVC.Name)
				}

				// Check volume ID extraction
				if result.VolumeID != "vol-0123456789abcdef0" {
					t.Errorf("expected volume ID vol-0123456789abcdef0, got %s", result.VolumeID)
				}

				// Check AZ extraction
				if result.AvailabilityZone != "us-east-1a" {
					t.Errorf("expected AZ us-east-1a, got %s", result.AvailabilityZone)
				}
			},
		},
		{
			name: "storage class mapping",
			sourcePV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "pvc-mapping-test",
				},
				Spec: corev1.PersistentVolumeSpec{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						CSI: &corev1.CSIPersistentVolumeSource{
							Driver:       "ebs.csi.aws.com",
							VolumeHandle: "vol-mapping123",
						},
					},
					StorageClassName: "gp2",
				},
			},
			sourcePVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pvc",
					Namespace: "source",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("5Gi"),
						},
					},
					VolumeName: "pvc-mapping-test",
				},
			},
			config: PVTranslationConfig{
				DestNamespace: "dest",
				DestPVCName:   "test-pvc",
				StorageClassMapping: map[string]string{
					"gp2": "gp3",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, result *TranslationResult) {
				if result.PV.Spec.StorageClassName != "gp3" {
					t.Errorf("expected storage class gp3 (mapped from gp2), got %s", result.PV.Spec.StorageClassName)
				}
			},
		},
		{
			name: "legacy AWS EBS volume",
			sourcePV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "legacy-ebs-pv",
				},
				Spec: corev1.PersistentVolumeSpec{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("20Gi"),
					},
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						AWSElasticBlockStore: &corev1.AWSElasticBlockStoreVolumeSource{
							VolumeID: "aws://us-east-1a/vol-legacy123",
							FSType:   "ext4",
						},
					},
				},
			},
			sourcePVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "legacy-pvc",
					Namespace: "source",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("20Gi"),
						},
					},
					VolumeName: "legacy-ebs-pv",
				},
			},
			config: PVTranslationConfig{
				DestNamespace: "dest",
				DestPVCName:   "legacy-pvc",
			},
			wantErr: false,
			validate: func(t *testing.T, result *TranslationResult) {
				// Should extract volume ID from path format
				if result.VolumeID != "vol-legacy123" {
					t.Errorf("expected volume ID vol-legacy123, got %s", result.VolumeID)
				}
				// Should use legacy volume source
				if result.PV.Spec.AWSElasticBlockStore == nil {
					t.Error("expected AWSElasticBlockStore source")
				}
			},
		},
		{
			name: "nil PV should error",
			sourcePV: nil,
			sourcePVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
			},
			config: PVTranslationConfig{
				DestNamespace: "dest",
				DestPVCName:   "test",
			},
			wantErr: true,
		},
		{
			name: "nil PVC should error",
			sourcePV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
			},
			sourcePVC: nil,
			config: PVTranslationConfig{
				DestNamespace: "dest",
				DestPVCName:   "test",
			},
			wantErr: true,
		},
		{
			name: "non-EBS volume should error",
			sourcePV: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "nfs-pv"},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						NFS: &corev1.NFSVolumeSource{
							Server: "nfs.example.com",
							Path:   "/exports/data",
						},
					},
				},
			},
			sourcePVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nfs-pvc",
					Namespace: "source",
				},
			},
			config: PVTranslationConfig{
				DestNamespace: "dest",
				DestPVCName:   "nfs-pvc",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := TranslatePV(tt.sourcePV, tt.sourcePVC, tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("TranslatePV() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}

func TestExtractEBSVolumeID(t *testing.T) {
	tests := []struct {
		name    string
		pv      *corev1.PersistentVolume
		want    string
		wantErr bool
	}{
		{
			name: "CSI volume handle",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						CSI: &corev1.CSIPersistentVolumeSource{
							Driver:       "ebs.csi.aws.com",
							VolumeHandle: "vol-abc123",
						},
					},
				},
			},
			want:    "vol-abc123",
			wantErr: false,
		},
		{
			name: "legacy EBS - direct volume ID",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						AWSElasticBlockStore: &corev1.AWSElasticBlockStoreVolumeSource{
							VolumeID: "vol-direct123",
						},
					},
				},
			},
			want:    "vol-direct123",
			wantErr: false,
		},
		{
			name: "legacy EBS - path format",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						AWSElasticBlockStore: &corev1.AWSElasticBlockStoreVolumeSource{
							VolumeID: "aws://us-east-1a/vol-path123",
						},
					},
				},
			},
			want:    "vol-path123",
			wantErr: false,
		},
		{
			name: "unsupported CSI driver",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						CSI: &corev1.CSIPersistentVolumeSource{
							Driver:       "pd.csi.storage.gke.io",
							VolumeHandle: "projects/test/disks/test",
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractEBSVolumeID(tt.pv)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractEBSVolumeID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("extractEBSVolumeID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractAvailabilityZone(t *testing.T) {
	tests := []struct {
		name string
		pv   *corev1.PersistentVolume
		want string
	}{
		{
			name: "standard topology label",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					NodeAffinity: &corev1.VolumeNodeAffinity{
						Required: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      "topology.kubernetes.io/zone",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"us-west-2a"},
										},
									},
								},
							},
						},
					},
				},
			},
			want: "us-west-2a",
		},
		{
			name: "legacy topology label",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{
					NodeAffinity: &corev1.VolumeNodeAffinity{
						Required: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      "failure-domain.beta.kubernetes.io/zone",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"eu-west-1b"},
										},
									},
								},
							},
						},
					},
				},
			},
			want: "eu-west-1b",
		},
		{
			name: "no node affinity",
			pv: &corev1.PersistentVolume{
				Spec: corev1.PersistentVolumeSpec{},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAvailabilityZone(tt.pv)
			if got != tt.want {
				t.Errorf("extractAvailabilityZone() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetPVCNameForStatefulSetPod(t *testing.T) {
	tests := []struct {
		volumeClaimTemplate string
		stsName             string
		index               int
		want                string
	}{
		{"data", "web", 0, "data-web-0"},
		{"data", "web", 5, "data-web-5"},
		{"storage", "postgres", 2, "storage-postgres-2"},
		{"logs", "app", 10, "logs-app-10"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := GetPVCNameForStatefulSetPod(tt.volumeClaimTemplate, tt.stsName, tt.index)
			if got != tt.want {
				t.Errorf("GetPVCNameForStatefulSetPod() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidatePVForMigration(t *testing.T) {
	tests := []struct {
		name    string
		pv      *corev1.PersistentVolume
		wantErr bool
	}{
		{
			name: "valid bound CSI volume",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						CSI: &corev1.CSIPersistentVolumeSource{
							Driver: "ebs.csi.aws.com",
						},
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeBound,
				},
			},
			wantErr: false,
		},
		{
			name: "valid bound legacy EBS volume",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						AWSElasticBlockStore: &corev1.AWSElasticBlockStoreVolumeSource{
							VolumeID: "vol-123",
						},
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeBound,
				},
			},
			wantErr: false,
		},
		{
			name:    "nil PV",
			pv:      nil,
			wantErr: true,
		},
		{
			name: "not bound",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						CSI: &corev1.CSIPersistentVolumeSource{
							Driver: "ebs.csi.aws.com",
						},
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeAvailable,
				},
			},
			wantErr: true,
		},
		{
			name: "non-EBS volume",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						NFS: &corev1.NFSVolumeSource{},
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeBound,
				},
			},
			wantErr: true,
		},
		{
			name: "wrong CSI driver",
			pv: &corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pv"},
				Spec: corev1.PersistentVolumeSpec{
					PersistentVolumeSource: corev1.PersistentVolumeSource{
						CSI: &corev1.CSIPersistentVolumeSource{
							Driver: "pd.csi.storage.gke.io",
						},
					},
				},
				Status: corev1.PersistentVolumeStatus{
					Phase: corev1.VolumeBound,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePVForMigration(tt.pv)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePVForMigration() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
