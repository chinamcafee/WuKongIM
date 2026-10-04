package meta

import "testing"

// Historical bytes must remain readable after both forks extend Channel state.
func TestChannelValueMergePreservesHistoricalLayouts(t *testing.T) {
	base := []byte(nil)
	for _, v := range []int64{0, 0, 0, 1, 7, 2, 0} {
		base = appendValueInt64(base, v)
	}
	for _, tc := range []struct {
		name               string
		value              []byte
		expiry             int64
		state              DirectoryProjectionState
		generation, policy uint64
	}{
		{"baseline", base, 0, DirectoryProjectionNone, 0, 0},
		{"linku", appendValueInt64(append([]byte(nil), base...), 1900000000), 1900000000, DirectoryProjectionNone, 0, 0},
		{"upstream", appendValueUint64(appendValueUint64(appendValueInt64(append([]byte(nil), base...), int64(DirectoryProjectionReady)), 9), 4), 0, DirectoryProjectionReady, 9, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, err := decodeChannelValue("g1", 2, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if row.ExpireAtUnixSeconds != tc.expiry || row.DirectoryProjectionState != tc.state || row.DirectoryProjectionGeneration != tc.generation || row.SendBanVersion != tc.policy {
				t.Fatalf("row=%+v", row)
			}
			next, err := decodeChannelValue("g1", 2, encodeChannelValue(row))
			if err != nil || next != row {
				t.Fatalf("roundtrip=%+v err=%v", next, err)
			}
		})
	}
}
func TestMergeDurableIdentifiersDoNotAlias(t *testing.T) {
	ids := map[uint32]bool{}
	for _, table := range Tables() {
		if ids[table.ID] {
			t.Fatalf("duplicate table ID %d", table.ID)
		}
		ids[table.ID] = true
	}
	if TableIDCMDDeviceCursor != 16 || TableIDUserCMDChannelMembership == 16 {
		t.Fatal("existing Link-U device cursors must retain table 16")
	}
}
