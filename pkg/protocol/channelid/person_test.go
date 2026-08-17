package channelid

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodePersonChannelBreaksCRCTiesDeterministically(t *testing.T) {
	left := "l98cu"
	right := "pvdba"

	require.Equal(t, EncodePersonChannel(left, right), EncodePersonChannel(right, left))
}

func TestEncodePersonChannelFixedVectorsForCrossLanguageClients(t *testing.T) {
	tests := []struct{ left, right, expected string }{
		{left: "alice", right: "bob", expected: "bob@alice"},
		{left: "u1", right: "u2", expected: "u2@u1"},
		{left: "user_100", right: "amb_7", expected: "user_100@amb_7"},
		{left: "l98cu", right: "pvdba", expected: "pvdba@l98cu"},
	}
	for _, test := range tests {
		require.Equal(t, test.expected, EncodePersonChannel(test.left, test.right))
		require.Equal(t, test.expected, EncodePersonChannel(test.right, test.left))
	}
}
