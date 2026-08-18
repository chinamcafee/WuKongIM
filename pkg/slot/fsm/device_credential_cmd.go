package fsm

import (
	"bytes"
	"encoding/binary"
	"fmt"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var deviceCredentialResultMagic = []byte{'W', 'K', 'D', 'C', 1}

const (
	tagDeviceCredentialResultOutcome        uint8 = 1
	tagDeviceCredentialResultCurrentVersion uint8 = 2
)

type applyDeviceCredentialCmd struct {
	device metadb.Device
	result *metadb.DeviceCredentialMutationResult
}

func (c *applyDeviceCredentialCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
	result, err := wb.ApplyDeviceCredentialConditionally(hashSlot, c.device)
	c.result = result
	return err
}

func (c *applyDeviceCredentialCmd) applyResult() []byte {
	return EncodeDeviceCredentialMutationResult(c.result)
}

// EncodeApplyDeviceCredentialCommand encodes one replicated conditional credential mutation.
func EncodeApplyDeviceCredentialCommand(device metadb.Device) []byte {
	return encodeDeviceCommand(cmdTypeApplyDeviceCredential, device)
}

func encodeDeviceCommand(commandType uint8, device metadb.Device) []byte {
	buf := make([]byte, 0, 128+len(device.UID)+len(device.Token)+len(device.OperationDigest))
	buf = append(buf, commandVersion, commandType)
	buf = appendStringTLVField(buf, tagDeviceUID, device.UID)
	buf = appendInt64TLVField(buf, tagDeviceFlag, device.DeviceFlag)
	buf = appendStringTLVField(buf, tagDeviceToken, device.Token)
	buf = appendInt64TLVField(buf, tagDeviceLevel, device.DeviceLevel)
	buf = appendUint64TLVField(buf, tagDeviceCredentialVersion, device.CredentialVersion)
	buf = appendStringTLVField(buf, tagDeviceLoginSessionID, device.LoginSessionID)
	buf = appendStringTLVField(buf, tagDeviceOperationID, device.OperationID)
	buf = appendStringTLVField(buf, tagDeviceOperationDigest, device.OperationDigest)
	buf = appendStringTLVField(buf, tagDeviceCredentialStatus, string(device.CredentialStatus))
	buf = appendInt64TLVField(buf, tagDeviceExpiresAtUnixMS, device.ExpiresAtUnixMS)
	buf = appendInt64TLVField(buf, tagDeviceUpdatedAtUnixMS, device.UpdatedAtUnixMS)
	buf = appendStringTLVField(buf, tagDeviceTerminationCause, device.TerminationCause)
	return buf
}

func decodeApplyDeviceCredential(data []byte) (command, error) {
	device, err := decodeDevice(data)
	if err != nil {
		return nil, err
	}
	return &applyDeviceCredentialCmd{device: device}, nil
}

// EncodeDeviceCredentialMutationResult encodes the replicated CAS result.
func EncodeDeviceCredentialMutationResult(result *metadb.DeviceCredentialMutationResult) []byte {
	if result == nil {
		return nil
	}
	buf := append([]byte(nil), deviceCredentialResultMagic...)
	buf = appendStringTLVField(buf, tagDeviceCredentialResultOutcome, string(result.Outcome))
	buf = appendUint64TLVField(buf, tagDeviceCredentialResultCurrentVersion, result.CurrentVersion)
	return buf
}

// DecodeDeviceCredentialMutationResult decodes a replicated credential CAS result.
func DecodeDeviceCredentialMutationResult(data []byte) (metadb.DeviceCredentialMutationResult, error) {
	if len(data) < len(deviceCredentialResultMagic) || !bytes.Equal(data[:len(deviceCredentialResultMagic)], deviceCredentialResultMagic) {
		return metadb.DeviceCredentialMutationResult{}, fmt.Errorf("%w: device credential result marker", metadb.ErrCorruptValue)
	}
	var result metadb.DeviceCredentialMutationResult
	var haveOutcome, haveVersion bool
	for offset := len(deviceCredentialResultMagic); offset < len(data); {
		tag, value, consumed, err := readTLV(data[offset:])
		if err != nil {
			return metadb.DeviceCredentialMutationResult{}, err
		}
		offset += consumed
		switch tag {
		case tagDeviceCredentialResultOutcome:
			result.Outcome = metadb.DeviceCredentialMutationOutcome(value)
			haveOutcome = true
		case tagDeviceCredentialResultCurrentVersion:
			if len(value) != 8 {
				return metadb.DeviceCredentialMutationResult{}, fmt.Errorf("%w: device credential result version", metadb.ErrCorruptValue)
			}
			result.CurrentVersion = binary.BigEndian.Uint64(value)
			haveVersion = true
		}
	}
	if !haveOutcome || !haveVersion {
		return metadb.DeviceCredentialMutationResult{}, fmt.Errorf("%w: incomplete device credential result", metadb.ErrCorruptValue)
	}
	switch result.Outcome {
	case metadb.DeviceCredentialOutcomeApplied,
		metadb.DeviceCredentialOutcomeIdempotent,
		metadb.DeviceCredentialOutcomeStaleVersion,
		metadb.DeviceCredentialOutcomeIdempotencyConflict:
		return result, nil
	default:
		return metadb.DeviceCredentialMutationResult{}, fmt.Errorf("%w: unknown device credential outcome", metadb.ErrCorruptValue)
	}
}
