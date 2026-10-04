package quorumlog

// DefaultRecoveryPageBytes is the native runtime's donor-page budget. Offline
// proposals must fit this bound because recovery cannot split an exact proposal.
const DefaultRecoveryPageBytes = 1 << 20

// RecoveryRecordBytes is the storage-neutral record cost used by donor pages
// and peer admission. Native encodings have a separate budget and may be larger.
func RecoveryRecordBytes(fromUID, clientMsgNo string, payloadBytes int) int {
	return 96 + len(fromUID) + len(clientMsgNo) + payloadBytes
}
