package migrationv3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/usecase/migration"
)

func pluginArtifactAssignments(node uint64, report *migration.PluginArtifactsReport) map[string]migration.PluginArtifactSpec {
	want := map[string]migration.PluginArtifactSpec{}
	if report == nil {
		return want
	}
	for _, a := range report.Targets {
		if a.TargetNode != node {
			continue
		}
		for _, f := range report.Files {
			if f.Spec.SourceNode == a.SourceNode && f.Spec.PluginNo == a.PluginNo {
				want[a.PluginNo] = f.Spec
			}
		}
	}
	return want
}

// installPluginArtifacts writes native plugins/<no>.wkp files before sealing
// the generation. Partial writes use one reserved path and can be resumed;
// published executables are verified, never overwritten.
func installPluginArtifacts(ctx context.Context, node migration.TargetNode, w migration.Workspace, report *migration.PluginArtifactsReport) error {
	want := pluginArtifactAssignments(node.NodeID, report)
	if len(want) == 0 {
		return checkPluginArtifacts(ctx, node, report)
	}
	dir := filepath.Join(node.DataDir, "plugins")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("native plugin directory is not a real directory")
	}
	names := make([]string, 0, len(want))
	for no := range want {
		names = append(names, no)
	}
	sort.Strings(names)
	for _, no := range names {
		if err := installPluginArtifact(ctx, dir, w, want[no]); err != nil {
			return err
		}
	}
	return checkPluginArtifacts(ctx, node, report)
}

func installPluginArtifact(ctx context.Context, dir string, w migration.Workspace, spec migration.PluginArtifactSpec) (err error) {
	path := filepath.Join(dir, spec.PluginNo+".wkp")
	if _, err := os.Lstat(path); err == nil {
		got, err := readNativePluginArtifact(ctx, path, spec.PluginNo)
		if err != nil {
			return err
		}
		if got.Bytes != spec.Bytes || got.SHA256 != spec.SHA256 || got.Mode != 0500 {
			return errors.New("existing plugin executable differs from import")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	partial := filepath.Join(dir, "."+spec.PluginNo+".wkmigrate-partial")
	// This exact path belongs to the already admitted, exclusively locked
	// generation. A regular partial can be rewritten after process interruption.
	if info, err := os.Lstat(partial); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("plugin partial file is not regular")
		}
		if err := os.Chmod(partial, 0600); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := os.Remove(partial); !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, walkErr := migration.WalkPluginArtifact(ctx, w, spec, func(data []byte) error { _, err := f.Write(data); return err })
	if walkErr == nil {
		walkErr = f.Chmod(0500)
	}
	if walkErr == nil {
		walkErr = f.Sync()
	}
	if err := errors.Join(walkErr, f.Close()); err != nil {
		return err
	}
	if err := os.Rename(partial, path); err != nil {
		return err
	}
	return syncDir(dir)
}

func checkPluginArtifacts(ctx context.Context, node migration.TargetNode, report *migration.PluginArtifactsReport) error {
	want := pluginArtifactAssignments(node.NodeID, report)
	err := walkNativePluginArtifacts(ctx, filepath.Join(node.DataDir, "plugins"), func(got migration.NativePluginArtifact) error {
		spec, found := want[got.PluginNo]
		if !found || got.Bytes != spec.Bytes || got.SHA256 != spec.SHA256 || got.Mode != 0500 {
			return errors.New("native plugin executable differs from import assignment")
		}
		delete(want, got.PluginNo)
		return nil
	})
	if err != nil {
		return err
	}
	if len(want) != 0 {
		return errors.New("native plugin executable is missing")
	}
	return nil
}

func (v *nativeView) WalkPluginArtifacts(ctx context.Context, visit func(migration.NativePluginArtifact) error) error {
	return walkNativePluginArtifacts(ctx, filepath.Join(v.node.DataDir, "plugins"), visit)
}

func walkNativePluginArtifacts(ctx context.Context, dir string, visit func(migration.NativePluginArtifact) error) (err error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("native plugin directory is not a real directory")
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	// The plan admits at most 1024 original files, hence at most 1024 per
	// target. Sort this bounded inventory for deterministic evidence digests.
	entries, err := f.ReadDir(1025)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > 1024 {
		return errors.New("native plugin inventory exceeds plan bound")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasSuffix(entry.Name(), ".wkp") {
			return errors.New("unexpected native plugin file")
		}
		got, err := readNativePluginArtifact(ctx, filepath.Join(dir, entry.Name()), strings.TrimSuffix(entry.Name(), ".wkp"))
		if err != nil {
			return err
		}
		if err := visit(got); err != nil {
			return err
		}
	}
	return nil
}

func readNativePluginArtifact(ctx context.Context, path, no string) (out migration.NativePluginArtifact, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0500 || info.Size() <= 0 || info.Size() > 512<<20 {
		return out, errors.New("native plugin must be a bounded regular executable with mode 0500")
	}
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	opened, err := f.Stat()
	if err != nil {
		return out, err
	}
	if !os.SameFile(info, opened) {
		return out, errors.New("native plugin changed while opening")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(&contextReader{ctx: ctx, r: f}, info.Size()+1))
	if err != nil {
		return out, err
	}
	if n != info.Size() {
		return out, errors.New("native plugin size changed while reading")
	}
	return migration.NativePluginArtifact{PluginNo: no, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil)), Mode: uint32(info.Mode().Perm())}, nil
}
