package cluster_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/require"
)

func TestIncompleteOfflineImportCannotOpenANativeCluster(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "MIGRATION-IMPORTING"), []byte(`{"version":1}`), 0600))
	_, err := cluster.New(cluster.Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: dir})
	require.ErrorContains(t, err, "offline import")
}
