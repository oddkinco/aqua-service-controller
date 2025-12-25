// Package aws provides AWS-specific operations for the migration controller
package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// EBSClient provides operations for AWS EBS volumes
type EBSClient struct {
	ec2Client *ec2.Client
	region    string
}

// EBSClientConfig contains configuration for creating an EBS client
type EBSClientConfig struct {
	// Region is the AWS region
	Region string

	// Profile is the AWS profile to use (optional)
	Profile string

	// Endpoint is a custom endpoint URL (optional, for testing)
	Endpoint string
}

// VolumeInfo contains information about an EBS volume
type VolumeInfo struct {
	// VolumeID is the EBS volume ID
	VolumeID string

	// State is the current state of the volume
	State types.VolumeState

	// AvailabilityZone is the AZ where the volume resides
	AvailabilityZone string

	// Size is the volume size in GiB
	Size int32

	// VolumeType is the EBS volume type (gp2, gp3, io1, etc.)
	VolumeType types.VolumeType

	// Attachments contains information about current attachments
	Attachments []VolumeAttachment

	// Tags contains the volume's tags
	Tags map[string]string
}

// VolumeAttachment contains information about a volume attachment
type VolumeAttachment struct {
	// InstanceID is the EC2 instance the volume is attached to
	InstanceID string

	// Device is the device name (e.g., /dev/xvda)
	Device string

	// State is the attachment state
	State types.VolumeAttachmentState
}

// NewEBSClient creates a new EBS client with the given configuration
func NewEBSClient(ctx context.Context, cfg EBSClientConfig) (*EBSClient, error) {
	var opts []func(*config.LoadOptions) error

	if cfg.Region != "" {
		opts = append(opts, config.WithRegion(cfg.Region))
	}

	if cfg.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(cfg.Profile))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	var ec2Opts []func(*ec2.Options)
	if cfg.Endpoint != "" {
		ec2Opts = append(ec2Opts, func(o *ec2.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		})
	}

	return &EBSClient{
		ec2Client: ec2.NewFromConfig(awsCfg, ec2Opts...),
		region:    cfg.Region,
	}, nil
}

// NewEBSClientFromConfig creates a new EBS client from an existing AWS config
func NewEBSClientFromConfig(awsCfg aws.Config) *EBSClient {
	return &EBSClient{
		ec2Client: ec2.NewFromConfig(awsCfg),
		region:    awsCfg.Region,
	}
}

// GetVolumeInfo retrieves information about an EBS volume
func (c *EBSClient) GetVolumeInfo(ctx context.Context, volumeID string) (*VolumeInfo, error) {
	resp, err := c.ec2Client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volumeID},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe volume %s: %w", volumeID, err)
	}

	if len(resp.Volumes) == 0 {
		return nil, fmt.Errorf("volume %s not found", volumeID)
	}

	vol := resp.Volumes[0]
	info := &VolumeInfo{
		VolumeID:         aws.ToString(vol.VolumeId),
		State:            vol.State,
		AvailabilityZone: aws.ToString(vol.AvailabilityZone),
		Size:             aws.ToInt32(vol.Size),
		VolumeType:       vol.VolumeType,
		Tags:             make(map[string]string),
	}

	// Convert attachments
	for _, att := range vol.Attachments {
		info.Attachments = append(info.Attachments, VolumeAttachment{
			InstanceID: aws.ToString(att.InstanceId),
			Device:     aws.ToString(att.Device),
			State:      att.State,
		})
	}

	// Convert tags
	for _, tag := range vol.Tags {
		info.Tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}

	return info, nil
}

// IsVolumeAvailable checks if a volume is in the "available" state (not attached)
func (c *EBSClient) IsVolumeAvailable(ctx context.Context, volumeID string) (bool, error) {
	info, err := c.GetVolumeInfo(ctx, volumeID)
	if err != nil {
		return false, err
	}
	return info.State == types.VolumeStateAvailable, nil
}

// WaitForVolumeDetachConfig contains configuration for WaitForVolumeDetach
type WaitForVolumeDetachConfig struct {
	// PollInterval is how often to check the volume state (default: 5s)
	PollInterval time.Duration

	// Timeout is the maximum time to wait (default: 5m)
	Timeout time.Duration

	// OnPoll is called each time the volume is polled (optional)
	OnPoll func(info *VolumeInfo)
}

// DefaultWaitConfig returns the default wait configuration
func DefaultWaitConfig() WaitForVolumeDetachConfig {
	return WaitForVolumeDetachConfig{
		PollInterval: 5 * time.Second,
		Timeout:      5 * time.Minute,
	}
}

// WaitForVolumeDetach blocks until the EBS volume is detached and available
// This is critical for migration - we must wait for the volume to be detached
// from the source cluster before it can be attached to the destination cluster.
func (c *EBSClient) WaitForVolumeDetach(ctx context.Context, volumeID string, cfg WaitForVolumeDetachConfig) error {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	// Check immediately first
	info, err := c.GetVolumeInfo(ctx, volumeID)
	if err != nil {
		return fmt.Errorf("failed to get initial volume info: %w", err)
	}
	if info.State == types.VolumeStateAvailable {
		return nil // Already available
	}
	if cfg.OnPoll != nil {
		cfg.OnPoll(info)
	}

	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("timeout waiting for volume %s to detach (waited %v)", volumeID, cfg.Timeout)
			}
			return ctx.Err()

		case <-ticker.C:
			info, err := c.GetVolumeInfo(ctx, volumeID)
			if err != nil {
				return fmt.Errorf("failed to get volume info: %w", err)
			}

			if cfg.OnPoll != nil {
				cfg.OnPoll(info)
			}

			if info.State == types.VolumeStateAvailable {
				return nil // Success - volume is now available
			}

			// Check for error states
			if info.State == types.VolumeStateError {
				return fmt.Errorf("volume %s is in error state", volumeID)
			}
			if info.State == types.VolumeStateDeleted || info.State == types.VolumeStateDeleting {
				return fmt.Errorf("volume %s is being deleted or already deleted", volumeID)
			}

			// Still attached or in-use, continue waiting
		}
	}
}

// DescribeVolumeAttachments returns the current attachment state of a volume
func (c *EBSClient) DescribeVolumeAttachments(ctx context.Context, volumeID string) ([]VolumeAttachment, error) {
	info, err := c.GetVolumeInfo(ctx, volumeID)
	if err != nil {
		return nil, err
	}
	return info.Attachments, nil
}

// GetVolumeIDFromHandle extracts the volume ID from various formats
// AWS EBS volume handles can be in formats like:
// - vol-0123456789abcdef0
// - aws://us-east-1a/vol-0123456789abcdef0
func GetVolumeIDFromHandle(handle string) string {
	// If it contains a slash, it's a path format
	if len(handle) > 0 {
		for i := len(handle) - 1; i >= 0; i-- {
			if handle[i] == '/' {
				return handle[i+1:]
			}
		}
	}
	// Already just the volume ID
	return handle
}

// ValidateVolumeExists checks if a volume exists and returns basic info
func (c *EBSClient) ValidateVolumeExists(ctx context.Context, volumeID string) error {
	_, err := c.GetVolumeInfo(ctx, volumeID)
	return err
}

// VolumeStateString returns a human-readable string for a volume state
func VolumeStateString(state types.VolumeState) string {
	switch state {
	case types.VolumeStateAvailable:
		return "available"
	case types.VolumeStateInUse:
		return "in-use"
	case types.VolumeStateCreating:
		return "creating"
	case types.VolumeStateDeleted:
		return "deleted"
	case types.VolumeStateDeleting:
		return "deleting"
	case types.VolumeStateError:
		return "error"
	default:
		return string(state)
	}
}
