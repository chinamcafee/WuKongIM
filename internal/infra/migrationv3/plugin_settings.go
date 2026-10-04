package migrationv3

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
)

// installPluginSettings writes through the unchanged native store before the
// generation fingerprint is sealed. A resumed import never overwrites drift.
func installPluginSettings(ctx context.Context, node migration.TargetNode, w migration.Workspace, report *migration.PluginSettingsReport) error {
	if report == nil {
		return nil
	}
	dir := filepath.Join(node.DataDir, "plugin-state")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("plugin settings directory is not a real directory")
	}
	store := pluginhost.NewStore(dir)
	err = migration.WalkPluginSettings(ctx, w, *report, func(record migration.MappedPluginSettings) error {
		if record.TargetNode != node.NodeID {
			return nil
		}
		path := filepath.Join(dir, record.Desired.No+".json")
		if info, err := os.Lstat(path); err == nil {
			if !info.Mode().IsRegular() {
				return errors.New("plugin settings file is not regular")
			}
			existing, err := store.Load(record.Desired.No)
			if err != nil {
				return err
			}
			equal, err := migration.EqualPluginState(existing, record.Desired)
			if err != nil {
				return err
			}
			if !equal {
				return errors.New("existing plugin settings differ from import assignment")
			}
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return store.Save(record.Desired)
	})
	if err != nil {
		return err
	}
	// Check unexpected files before accepting an incomplete generation on retry.
	var count uint64
	if err := walkNativePluginStates(ctx, dir, func(pluginhost.DesiredState) error { count++; return nil }); err != nil {
		return err
	}
	if count != report.ByTarget[node.NodeID] {
		return errors.New("native plugin settings count differs from assignment")
	}
	return syncDir(node.DataDir)
}

func (v *nativeView) WalkPluginStates(ctx context.Context, visit func(pluginhost.DesiredState) error) error {
	return walkNativePluginStates(ctx, filepath.Join(v.node.DataDir, "plugin-state"), visit)
}

// walkNativePluginStates bounds directory enumeration and rejects alternate
// file identities, symlinks and unfinished writes instead of ignoring them.
func walkNativePluginStates(ctx context.Context, dir string, visit func(pluginhost.DesiredState) error) (err error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("plugin settings directory is not a real directory")
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	store := pluginhost.NewStore(dir)
	for {
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
				return errors.New("unexpected native plugin settings file")
			}
			state, err := store.Load(strings.TrimSuffix(entry.Name(), ".json"))
			if err != nil {
				return errors.New("invalid native plugin settings file")
			}
			if err := visit(state); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
