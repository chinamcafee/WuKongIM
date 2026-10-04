package migrationv3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var importMu sync.Mutex

// checkGeneration admits only an absent output or the exact interrupted
// generation. A completed generation is read-only on every retry.
func checkGeneration(dir string, marker []byte) (bool, error) {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("target must be a regular directory")
	}
	data, err := readMarker(filepath.Join(dir, "MIGRATION-IMPORTING"))
	if err != nil || !bytes.Equal(data, marker) {
		return false, errors.New("existing target is not the same migration generation")
	}
	data, err = readMarker(filepath.Join(dir, "MIGRATION-COMPLETE"))
	if err == nil && !bytes.Equal(data, marker) {
		return false, errors.New("completed target belongs to a different migration generation")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("existing native generation contains a non-regular file")
		}
		return nil
	}); err != nil {
		return false, err
	}
	return true, nil
}

func readMarker(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, errors.New("invalid migration marker")
	}
	return os.ReadFile(path)
}

// generationDigest streams every immutable native file after storage closes.
// The resulting checkpoint detects runtime opens, edits and partial publication
// before a resumed importer can write to a completed node.
func generationDigest(ctx context.Context, dir string) (string, error) {
	all := sha256.New()
	enc := json.NewEncoder(all)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if entry.Name() == "LOCK" || (filepath.Dir(rel) == "." && (strings.HasPrefix(entry.Name(), "MIGRATION-") || strings.HasPrefix(entry.Name(), ".wkmigrate-marker-"))) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("native generation contains a non-regular file")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, readErr := io.Copy(h, &contextReader{ctx: ctx, r: f})
		err = errors.Join(readErr, f.Close())
		if err != nil {
			return err
		}
		if n != info.Size() {
			return errors.New("native generation changed during fingerprint")
		}
		return enc.Encode(struct {
			Path   string
			Bytes  int64
			SHA256 string
		}{filepath.ToSlash(rel), n, hex.EncodeToString(h.Sum(nil))})
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(all.Sum(nil)), nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func nodeReady(ctx context.Context, dir string) (bool, error) {
	expected, err := readMarker(filepath.Join(dir, "MIGRATION-READY"))
	if errors.Is(err, os.ErrNotExist) {
		if _, completeErr := os.Lstat(filepath.Join(dir, "MIGRATION-COMPLETE")); !errors.Is(completeErr, os.ErrNotExist) {
			return false, errors.New("completed target has no immutable data checkpoint")
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	actual, err := generationDigest(ctx, dir)
	if err != nil {
		return false, err
	}
	if string(expected) != actual {
		return false, errors.New("completed target data changed; import refuses to overwrite it")
	}
	return true, nil
}

// writeExclusive publishes a synced marker atomically without replacing an
// existing name. Same-byte retries are harmless, including after power loss.
func writeExclusive(path string, data []byte) (err error) {
	if old, e := readMarker(path); e == nil {
		if bytes.Equal(old, data) {
			return nil
		}
		return errors.New("migration marker conflicts with existing generation")
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".wkmigrate-marker-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() {
		removeErr := os.Remove(name)
		if !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := os.Link(name, path); err != nil {
		return fmt.Errorf("publish migration marker: %w", err)
	}
	return syncDir(dir)
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
