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

// A directory name is not a pattern: Escape lets List find the files of one
// named with glob characters.
func TestEscape(t *testing.T) {
	dir := filepath.Join(t.TempDir(), `out[ab]*?\`)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(dir, "part-r-00000")
	if err := os.WriteFile(part, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := List(t.Context(), Join(Escape(dir), "part-r-*"))
	if err != nil || len(got) != 1 || got[0] != part {
		t.Fatalf("List = %q, %v; want %q", got, err, part)
	}
	if got, _ := List(t.Context(), Join(dir, "part-r-*")); len(got) != 0 {
		t.Fatalf("without Escape the directory still matched itself: %q", got)
	}
}
