package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// Directories that MkdirAll creates are writable by everyone, so the host
// user can delete output the containers wrote (ADR 0016); ones that exist
// keep their mode.
func TestLocalMkdirAllMode(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "a", "b")
	if err := (Local{}).MkdirAll(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(root, "a"), dir} {
		if info, err := os.Stat(d); err != nil || info.Mode().Perm() != 0o777 {
			t.Errorf("%s: %v, %v; want mode 0777", d, info.Mode(), err)
		}
	}
	if info, _ := os.Stat(root); info.Mode().Perm() != 0o700 {
		t.Errorf("existing directory's mode changed to %v", info.Mode())
	}
	if err := (Local{}).MkdirAll(t.Context(), dir); err != nil {
		t.Errorf("existing directory: %v", err)
	}
	file := filepath.Join(root, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Local{}).MkdirAll(t.Context(), filepath.Join(file, "x")); err == nil {
		t.Error("a path through a file: no error")
	}
}
