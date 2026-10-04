//go:build integration

package migrationv2_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// This optional integration requires the exact private original executable.
// Synthetic source fixtures isolate archive/import correctness; the separate
// app integration exercises actual Receive behavior on Linux/amd64.
func TestAuditedPluginProgramArchiveAndNativeImport(t *testing.T) {
	path := os.Getenv("WKMIGRATE_TEST_PLUGIN_BINARY")
	if path == "" {
		t.Skip("requires the audited original plugin executable")
	}
	program, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "671b3436d1a8d765371077009b1dfd6dec4528a1ce9cdc0dbebe2cfddc5b3224", fmt.Sprintf("%x", sha256.Sum256(program)))
	testPluginSettingsAndArtifactsTopology(t, program)
}
