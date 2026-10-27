package storage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
)

// Local stores files in the local file system, or a volume shared by every
// node. Commit is a rename, which is atomic, as GFS's is (§3.3).
type Local struct{}

func (Local) List(_ context.Context, pattern string) ([]string, error) {
	matches, err := filepath.Glob(pattern)
	slices.Sort(matches)
	return matches, err
}

func (Local) Size(_ context.Context, name string) (int64, error) {
	info, err := os.Stat(name)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func (Local) Open(_ context.Context, name string, offset int64) (io.ReadCloser, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

func (Local) Create(_ context.Context, name string) (io.WriteCloser, error) {
	return os.Create(name)
}

func (Local) Commit(_ context.Context, tmp, name string) error { return os.Rename(tmp, name) }

func (Local) Delete(_ context.Context, name string) error { return os.Remove(name) }

// MkdirAll makes each directory it creates writable by everyone, whatever
// the umask: the containers write job output as their own user, and the
// host user must be able to delete it from the shared data/ directory
// (ADR 0016).
func (l Local) MkdirAll(ctx context.Context, dir string) error {
	if info, err := os.Stat(dir); err == nil {
		if !info.IsDir() {
			return &os.PathError{Op: "mkdir", Path: dir, Err: syscall.ENOTDIR}
		}
		return nil
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := l.MkdirAll(ctx, parent); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, 0o777); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil // made by a concurrent call
		}
		return err
	}
	return os.Chmod(dir, 0o777)
}

func (Local) DeleteAll(_ context.Context, dir string) error { return os.RemoveAll(dir) }
