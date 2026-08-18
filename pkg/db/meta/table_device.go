package meta

import (
	"context"
	"errors"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/schema"
)

const deviceValueCodecMarker = "WUKONG_DEVICE_CREDENTIAL_V3"

// DeviceCredentialStatus is the durable admission state for one UID/device flag.
type DeviceCredentialStatus string

const (
	// DeviceCredentialStatusActive permits token authentication until expiry.
	DeviceCredentialStatusActive DeviceCredentialStatus = "ACTIVE"
	// DeviceCredentialStatusRevoked is a durable version tombstone.
	DeviceCredentialStatusRevoked DeviceCredentialStatus = "REVOKED"
)

// DeviceCredentialMutationOutcome classifies one replicated credential CAS.
type DeviceCredentialMutationOutcome string

const (
	DeviceCredentialOutcomeApplied             DeviceCredentialMutationOutcome = "APPLIED"
	DeviceCredentialOutcomeIdempotent          DeviceCredentialMutationOutcome = "IDEMPOTENT"
	DeviceCredentialOutcomeStaleVersion        DeviceCredentialMutationOutcome = "STALE_VERSION"
	DeviceCredentialOutcomeIdempotencyConflict DeviceCredentialMutationOutcome = "IDEMPOTENCY_CONFLICT"
)

// Device stores per-device token state for a UID.
type Device struct {
	UID               string
	DeviceFlag        int64
	Token             string
	DeviceLevel       int64
	CredentialVersion uint64
	LoginSessionID    string
	OperationID       string
	OperationDigest   string
	CredentialStatus  DeviceCredentialStatus
	ExpiresAtUnixMS   int64
	UpdatedAtUnixMS   int64
	TerminationCause  string
}

// DeviceCredentialMutationResult is populated by the replicated conditional mutation.
type DeviceCredentialMutationResult struct {
	Outcome        DeviceCredentialMutationOutcome
	CurrentVersion uint64
}

var deviceTable = registerMetaTable(TableSpec[Device]{
	ID:   TableIDDevice,
	Name: "device",
	Columns: []schema.Column{
		{ID: columnIDStringKey, Name: "uid", Type: schema.TypeString, Required: true},
		{ID: columnIDIntKey, Name: "device_flag", Type: schema.TypeInt64, Required: true},
		{ID: columnIDValue, Name: "value", Type: schema.TypeBytes},
	},
	Families: []schema.Family{{ID: devicePrimaryFamilyID, Name: "primary", Columns: []uint16{columnIDValue}}},
	Primary: PrimarySpec[Device]{
		IndexID:  devicePrimaryIndexID,
		FamilyID: devicePrimaryFamilyID,
		Name:     "pk_device",
		Columns:  []uint16{columnIDStringKey, columnIDIntKey},
		Layout:   KeyLayout{KeyString, KeyInt64Ordered},
		Key:      func(device Device) KeyParts { return KeyParts{String(device.UID), Int64Ordered(device.DeviceFlag)} },
	},
	Validate: validateDevice,
	EncodeValue: func(device Device) ([]byte, error) {
		return encodeDeviceValue(device), nil
	},
	DecodeValue: func(primary KeyParts, value []byte) (Device, error) {
		return decodeDeviceValue(primary[0].S, primary[1].I64, value)
	},
})

// DeviceTable describes the device table schema.
var DeviceTable = deviceTable.Schema()

// UpsertDevice stores a device regardless of prior existence.
func (s *Shard) UpsertDevice(ctx context.Context, device Device) error {
	return deviceTable.Upsert(ctx, s, device)
}

// GetDevice returns one device by UID and device flag.
func (s *Shard) GetDevice(ctx context.Context, uid string, deviceFlag int64) (Device, bool, error) {
	if err := validateKeyString(uid); err != nil {
		return Device{}, false, err
	}
	return deviceTable.Get(ctx, s, KeyParts{String(uid), Int64Ordered(deviceFlag)})
}

func validateDevice(device Device) error {
	if err := validateKeyString(device.UID); err != nil {
		return err
	}
	if device.CredentialVersion == 0 || strings.TrimSpace(device.LoginSessionID) == "" ||
		strings.TrimSpace(device.OperationID) == "" || strings.TrimSpace(device.OperationDigest) == "" ||
		device.UpdatedAtUnixMS <= 0 {
		return dberrors.ErrInvalidArgument
	}
	switch device.CredentialStatus {
	case DeviceCredentialStatusActive:
		if strings.TrimSpace(device.Token) == "" || device.ExpiresAtUnixMS <= device.UpdatedAtUnixMS {
			return dberrors.ErrInvalidArgument
		}
	case DeviceCredentialStatusRevoked:
		if device.Token != "" || device.ExpiresAtUnixMS != 0 {
			return dberrors.ErrInvalidArgument
		}
	default:
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func encodeDeviceValue(device Device) []byte {
	value := appendValueString(nil, deviceValueCodecMarker)
	value = appendValueString(value, device.Token)
	value = appendValueInt64(value, device.DeviceLevel)
	value = appendValueUint64(value, device.CredentialVersion)
	value = appendValueString(value, device.LoginSessionID)
	value = appendValueString(value, device.OperationID)
	value = appendValueString(value, device.OperationDigest)
	value = appendValueString(value, string(device.CredentialStatus))
	value = appendValueInt64(value, device.ExpiresAtUnixMS)
	value = appendValueInt64(value, device.UpdatedAtUnixMS)
	value = appendValueString(value, device.TerminationCause)
	return value
}

func decodeDeviceValue(uid string, deviceFlag int64, value []byte) (Device, error) {
	marker, rest, err := readValueString(value)
	if err != nil || marker != deviceValueCodecMarker {
		return Device{}, dberrors.ErrCorruptValue
	}
	token, rest, err := readValueString(rest)
	if err != nil {
		return Device{}, err
	}
	deviceLevel, rest, err := readValueInt64(rest)
	if err != nil {
		return Device{}, err
	}
	credentialVersion, rest, err := readValueUint64(rest)
	if err != nil {
		return Device{}, err
	}
	loginSessionID, rest, err := readValueString(rest)
	if err != nil {
		return Device{}, err
	}
	operationID, rest, err := readValueString(rest)
	if err != nil {
		return Device{}, err
	}
	operationDigest, rest, err := readValueString(rest)
	if err != nil {
		return Device{}, err
	}
	credentialStatus, rest, err := readValueString(rest)
	if err != nil {
		return Device{}, err
	}
	expiresAtUnixMS, rest, err := readValueInt64(rest)
	if err != nil {
		return Device{}, err
	}
	updatedAtUnixMS, rest, err := readValueInt64(rest)
	if err != nil {
		return Device{}, err
	}
	terminationCause, rest, err := readValueString(rest)
	if err != nil {
		return Device{}, err
	}
	if len(rest) != 0 {
		return Device{}, dberrors.ErrCorruptValue
	}
	device := Device{
		UID: uid, DeviceFlag: deviceFlag, Token: token, DeviceLevel: deviceLevel,
		CredentialVersion: credentialVersion, LoginSessionID: loginSessionID,
		OperationID: operationID, OperationDigest: operationDigest,
		CredentialStatus: DeviceCredentialStatus(credentialStatus),
		ExpiresAtUnixMS:  expiresAtUnixMS, UpdatedAtUnixMS: updatedAtUnixMS,
		TerminationCause: terminationCause,
	}
	if err := validateDevice(device); err != nil {
		return Device{}, errors.Join(dberrors.ErrCorruptValue, err)
	}
	return device, nil
}

func sameDeviceCredentialMutation(left, right Device) bool {
	return left.UID == right.UID && left.DeviceFlag == right.DeviceFlag &&
		left.CredentialVersion == right.CredentialVersion &&
		left.OperationID == right.OperationID && left.OperationDigest == right.OperationDigest &&
		left.CredentialStatus == right.CredentialStatus &&
		left.LoginSessionID == right.LoginSessionID
}
