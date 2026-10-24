package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
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

func (Local) MkdirAll(_ context.Context, dir string) error { return os.MkdirAll(dir, 0o755) }

func (Local) DeleteAll(_ context.Context, dir string) error { return os.RemoveAll(dir) }
