package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestGetVolumeIDFromHandle(t *testing.T) {
	tests := []struct {
		name   string
		handle string
		want   string
	}{
		{
			name:   "direct volume ID",
			handle: "vol-0123456789abcdef0",
			want:   "vol-0123456789abcdef0",
		},
		{
			name:   "AWS path format",
			handle: "aws://us-east-1a/vol-abc123",
			want:   "vol-abc123",
		},
		{
			name:   "simple path format",
			handle: "/vol-simple",
			want:   "vol-simple",
		},
		{
			name:   "empty string",
			handle: "",
			want:   "",
		},
		{
			name:   "multiple slashes",
			handle: "a/b/c/vol-deep",
			want:   "vol-deep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetVolumeIDFromHandle(tt.handle)
			if got != tt.want {
				t.Errorf("GetVolumeIDFromHandle(%q) = %q, want %q", tt.handle, got, tt.want)
			}
		})
	}
}

func TestVolumeStateString(t *testing.T) {
	tests := []struct {
		state types.VolumeState
		want  string
	}{
		{types.VolumeStateAvailable, "available"},
		{types.VolumeStateInUse, "in-use"},
		{types.VolumeStateCreating, "creating"},
		{types.VolumeStateDeleted, "deleted"},
		{types.VolumeStateDeleting, "deleting"},
		{types.VolumeStateError, "error"},
		{types.VolumeState("unknown"), "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			got := VolumeStateString(tt.state)
			if got != tt.want {
				t.Errorf("VolumeStateString(%v) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestDefaultWaitConfig(t *testing.T) {
	cfg := DefaultWaitConfig()

	if cfg.PollInterval.Seconds() != 5 {
		t.Errorf("expected PollInterval of 5s, got %v", cfg.PollInterval)
	}

	if cfg.Timeout.Minutes() != 5 {
		t.Errorf("expected Timeout of 5m, got %v", cfg.Timeout)
	}
}
