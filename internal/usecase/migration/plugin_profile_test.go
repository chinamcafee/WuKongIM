package migration

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
	"github.com/stretchr/testify/require"
)

func TestReceiveProfileRejectsUnverifiedContractChanges(t *testing.T) {
	for _, fault := range []string{"valid", "binary", "profile", "missing-program", "unmapped-program", "competition", "unapproved-config", "source-version", "method", "priority", "version", "status", "config-key", "config-type", "legacy-config"} {
		t.Run(fault, func(t *testing.T) {
			ctx, w := context.Background(), dedupeTestWorkspace(t)
			p := Plan{SourceCommit: "a888f89533d0e7d1b2030e06504ca97f1ad891d4", PluginConfigs: []PluginConfigMapping{{PluginNo: aiExamplePluginNo, SourceNode: 1}}}
			c := SourceCapture{Digest: strings.Repeat("c", 64), Tables: map[string]uint64{"Plugin": 3}}
			for n := uint64(1); n <= 3; n++ {
				p.Sources = append(p.Sources, NodeOptions{NodeID: n})
				p.PluginArtifacts = append(p.PluginArtifacts, PluginArtifactSpec{SourceNode: n, PluginNo: aiExamplePluginNo, Path: fmt.Sprintf("/source-%d/plugin", n), Bytes: 11856443, SHA256: aiExampleProgramSHA256, Profile: AIExampleReceiveProfile})
			}
			switch fault {
			case "binary":
				p.PluginArtifacts[0].SHA256 = strings.Repeat("0", 64)
			case "profile":
				p.PluginArtifacts[0].Profile = "operator-claims-compatible"
			case "missing-program":
				p.PluginArtifacts = p.PluginArtifacts[:2]
			case "unmapped-program":
				p.PluginArtifacts[0].Profile = ""
			case "competition":
				c.Tables["Plugin"]++
			case "unapproved-config":
				p.PluginConfigs = nil
			case "source-version":
				p.SourceCommit = strings.Repeat("0", 40)
			}
			for n := uint64(1); n <= 3; n++ {
				rec := MappedPluginSettings{SourceNode: n, SourceRowSHA256: fmt.Sprintf("%064x", n), Original: SourcePlugin{No: aiExamplePluginNo, Version: "0.0.1", Methods: []string{"Receive"}, Priority: 1, Config: []byte(`{"name":"approved"}`)}}
				if n == 2 {
					switch fault {
					case "method":
						rec.Original.Methods = append(rec.Original.Methods, "Send")
					case "priority":
						rec.Original.Priority++
					case "version":
						rec.Original.Version = "0.0.2"
					case "status":
						rec.Original.Status = 3
					case "config-key":
						rec.Original.Config = []byte(`{"name":"approved","extra":true}`)
					case "config-type":
						rec.Original.Config = []byte(`{"name":null}`)
					case "legacy-config":
						rec.Original.Config = []byte(`{"Name":"approved"}`)
					}
				}
				data, err := MarshalState(rec)
				require.NoError(t, err)
				key := "plugin-settings-original/v2/" + c.Digest + "/" + p.Digest() + fmt.Sprintf("/%020d/%x", n, []byte(aiExamplePluginNo))
				require.NoError(t, w.Put(ctx, []transfer.SpoolRow{{Key: []byte(key), Value: data}}))
			}
			report, err := certifyPluginProfile(ctx, p, c, w)
			if fault != "valid" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, AIExampleReceiveProfile, report.Profile)
			require.Len(t, report.SourceRows, 3)
		})
	}
}
