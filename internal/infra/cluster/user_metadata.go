package cluster

import (
	"context"

	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// UserMetadataNode exposes cluster Slot metadata operations used by the user usecase.
type UserMetadataNode interface {
	CreateUserMetadata(context.Context, metadb.User) error
	GetUserMetadata(context.Context, string) (metadb.User, error)
	UpsertDeviceMetadata(context.Context, metadb.Device) error
	GetDeviceMetadata(context.Context, string, int64) (metadb.Device, error)
}

// LoadCredentialFence maps durable device metadata into Presence admission state.
func (s *UserMetadataStore) LoadCredentialFence(ctx context.Context, uid string, deviceFlag uint8) (authoritypresence.CredentialFence, error) {
	device, err := s.GetDevice(ctx, uid, int64(deviceFlag))
	if err != nil {
		return authoritypresence.CredentialFence{}, err
	}
	return credentialFenceFromDevice(device), nil
}

func credentialFenceFromDevice(device metadb.Device) authoritypresence.CredentialFence {
	return authoritypresence.CredentialFence{
		UID: device.UID, DeviceFlag: uint8(device.DeviceFlag), CredentialVersion: device.CredentialVersion,
		LoginSessionID: device.LoginSessionID, Status: authoritypresence.CredentialStatus(device.CredentialStatus),
		ExpiresAtUnixMS: device.ExpiresAtUnixMS, MachineReason: device.TerminationCause,
	}
}

// DeviceCredentialNode exposes replicated conditional credential mutations.
type DeviceCredentialNode interface {
	ApplyDeviceCredentialMetadata(context.Context, metadb.Device) (metadb.DeviceCredentialMutationResult, error)
}

// UserMetadataScanNode exposes cluster user metadata page scans for manager lists.
type UserMetadataScanNode interface {
	ScanUsersSlotPage(context.Context, uint32, metadb.UserCursor, int) ([]metadb.User, metadb.UserCursor, bool, error)
}

// UserMetadataStore adapts cluster Slot metadata to the entry-agnostic user usecase.
type UserMetadataStore struct {
	node           UserMetadataNode
	scanNode       UserMetadataScanNode
	credentialNode DeviceCredentialNode
}

// NewUserMetadataStore creates a cluster-backed user metadata store.
func NewUserMetadataStore(node UserMetadataNode) *UserMetadataStore {
	scanNode, _ := node.(UserMetadataScanNode)
	credentialNode, _ := node.(DeviceCredentialNode)
	return &UserMetadataStore{node: node, scanNode: scanNode, credentialNode: credentialNode}
}

// CreateUser persists UID metadata through Slot ownership.
func (s *UserMetadataStore) CreateUser(ctx context.Context, user metadb.User) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.CreateUserMetadata(ctx, user)
}

// GetUser reads UID metadata from the current Slot route.
func (s *UserMetadataStore) GetUser(ctx context.Context, uid string) (metadb.User, error) {
	if s == nil || s.node == nil {
		return metadb.User{}, metadb.ErrNotFound
	}
	return s.node.GetUserMetadata(ctx, uid)
}

// UpsertDevice persists per-device token metadata through Slot ownership.
func (s *UserMetadataStore) UpsertDevice(ctx context.Context, device metadb.Device) error {
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	return s.node.UpsertDeviceMetadata(ctx, device)
}

// ApplyDeviceCredential performs the replicated credential CAS on the UID Slot.
func (s *UserMetadataStore) ApplyDeviceCredential(ctx context.Context, device metadb.Device) (metadb.DeviceCredentialMutationResult, error) {
	if s == nil || s.credentialNode == nil {
		return metadb.DeviceCredentialMutationResult{}, metadb.ErrNotFound
	}
	return s.credentialNode.ApplyDeviceCredentialMetadata(ctx, device)
}

// GetDevice reads per-device token metadata from the current Slot route.
func (s *UserMetadataStore) GetDevice(ctx context.Context, uid string, deviceFlag int64) (metadb.Device, error) {
	if s == nil || s.node == nil {
		return metadb.Device{}, metadb.ErrNotFound
	}
	return s.node.GetDeviceMetadata(ctx, uid, deviceFlag)
}

// ScanUsersSlotPage returns one user metadata page for a physical Slot.
func (s *UserMetadataStore) ScanUsersSlotPage(ctx context.Context, slotID uint32, after metadb.UserCursor, limit int) ([]metadb.User, metadb.UserCursor, bool, error) {
	if s == nil || s.scanNode == nil {
		return nil, metadb.UserCursor{}, false, metadb.ErrNotFound
	}
	return s.scanNode.ScanUsersSlotPage(ctx, slotID, after, limit)
}
