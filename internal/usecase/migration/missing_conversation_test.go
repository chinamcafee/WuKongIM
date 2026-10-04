package migration

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestMissingConversationDecisionsRejectUnusedMalformedAndEmptyHistory(t *testing.T) {
	m := SourceMember{UID: "user", Channel: ChannelIdentity{ID: "group", Type: 2}}
	r := MissingConversationRecovery{CaptureDigest: strings.Repeat("a", 64), UIDSHA256: diagnosticSHA([]byte(m.UID)), ChannelSHA256: diagnosticSHA([]byte(channelTuple(m.Channel))), RetainedTail: 100}
	s := SourceSelection{Metadata: &MetadataSelection{Policy: MetadataPolicy{MissingConversations: []MissingConversationRecovery{r}}}}
	d, e := newMissingConversationDecisions(s)
	require.NoError(t, e)
	require.ErrorContains(t, d.complete(), "unused")
	_, e = d.readSeq(m, 0)
	require.ErrorContains(t, e, "tail differs")
	seq, e := d.readSeq(m, 100)
	require.NoError(t, e)
	require.EqualValues(t, 100, seq)
	require.NoError(t, d.complete())
	for _, mode := range []string{"duplicate", "hash", "zero", "bound"} {
		t.Run(mode, func(t *testing.T) {
			p := MetadataPolicy{MissingConversations: []MissingConversationRecovery{r}}
			switch mode {
			case "duplicate":
				p.MissingConversations = append(p.MissingConversations, r)
			case "hash":
				p.MissingConversations[0].UIDSHA256 = "invalid"
			case "zero":
				p.MissingConversations[0].RetainedTail = 0
			case "bound":
				p.MissingConversations = make([]MissingConversationRecovery, 1025)
			}
			require.Error(t, validateMissingConversations(&p))
		})
	}
}
