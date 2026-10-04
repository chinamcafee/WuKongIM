package migration

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMigrationStatePreservesOpaqueIdentitiesAndDeterministicKeys(t *testing.T) {
	type state struct {
		ID       string
		Channel  ChannelIdentity
		Fields   map[string]any
		Payload  []byte
		Raw      json.RawMessage
		Created  time.Time
		Optional *string
	}
	raw := "a\xff\x00b"
	value := state{ID: raw, Channel: ChannelIdentity{ID: "c\xfe", Type: 1}, Fields: map[string]any{raw: "d\xff", "other": []any{"e\xfe"}}, Payload: []byte{0, 255}, Raw: json.RawMessage(`{"plain":"value"}`), Created: time.Unix(5, 0).UTC(), Optional: &raw}
	encoded, err := MarshalState(value)
	require.NoError(t, err)
	require.True(t, json.Valid(encoded))
	var got state
	require.NoError(t, UnmarshalState(encoded, &got))
	require.Equal(t, value, got)
	again, err := MarshalState(value)
	require.NoError(t, err)
	require.Equal(t, encoded, again)
	require.NotEqual(t, IdentityKey("a\xff"), IdentityKey("a\xfe"))
	require.NotEqual(t, IdentityKey("a\xff"), IdentityKey("a\ufffd"))
	// A JSON string can resemble the envelope without being interpreted as it.
	literal := string(encoded)
	var restored string
	b, err := MarshalState(literal)
	require.NoError(t, err)
	require.NoError(t, UnmarshalState(b, &restored))
	require.Equal(t, literal, restored)
}
func TestMigrationStateRejectsWrongEncodingAndMalformedStringsAtomically(t *testing.T) {
	for _, data := range [][]byte{[]byte(`{"value":{},"encoding":"other"}`), []byte(`{"value":"%","encoding":"wkmigrate-bytes-v1"}`), []byte(`{"ID":"old-workspace"}`), []byte(`{"value":"YQ==\n","encoding":"wkmigrate-bytes-v1"}`)} {
		got := "unchanged"
		require.Error(t, UnmarshalState(data, &got))
		require.Equal(t, "unchanged", got)
	}
	encoded, err := MarshalState("valid")
	require.NoError(t, err)
	require.Error(t, UnmarshalState(append(bytes.Clone(encoded), encoded...), new(string)))
}
