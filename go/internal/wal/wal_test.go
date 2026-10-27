package wal

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// open opens the log at path and returns it with the records it replayed.
func open(t *testing.T, path string) (*Log, []string) {
	t.Helper()
	var got []string
	l, err := Open(path, func(p []byte) error {
		got = append(got, string(p))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, got
}

func appendAll(t *testing.T, l *Log, records ...string) {
	t.Helper()
	for _, r := range records {
		if err := l.Append([]byte(r)); err != nil {
			t.Fatal(err)
		}
	}
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// Records survive a reopen, in order, and appends continue after them.
func TestReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log")
	l, got := open(t, path)
	if got != nil {
		t.Fatalf("new log replayed %q", got)
	}
	appendAll(t, l, "a", "bb")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if l, got = open(t, path); !slices.Equal(got, []string{"a", "bb"}) {
		t.Fatalf("replayed %q", got)
	}
	appendAll(t, l, "ccc")
	if _, got = open(t, path); !slices.Equal(got, []string{"a", "bb", "ccc"}) {
		t.Fatalf("replayed %q", got)
	}
}

// A crash mid-append leaves a torn last record, which Open drops so that
// appends continue from the last whole record.
func TestTornTail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	whole := filepath.Join(dir, "whole")
	l, _ := open(t, whole)
	appendAll(t, l, "first", "second")
	data, err := os.ReadFile(whole)
	if err != nil {
		t.Fatal(err)
	}
	firstEnd := int64(headerSize + len("first"))

	flipped := slices.Clone(data)
	flipped[len(flipped)-1] ^= 1
	zeroed := append(slices.Clone(data[:firstEnd]), make([]byte, headerSize)...)
	for name, file := range map[string][]byte{
		"short header":  data[:firstEnd+headerSize-1],
		"short payload": data[:len(data)-1],
		"bad checksum":  flipped,
		"zeroed":        zeroed,
		"long zeroed":   append(slices.Clone(zeroed), make([]byte, 3*headerSize)...),
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, file, 0o644); err != nil {
			t.Fatal(err)
		}
		l, got := open(t, path)
		if !slices.Equal(got, []string{"first"}) || size(t, path) != firstEnd {
			t.Errorf("%s: replayed %q, file is %d bytes; want first only, %d bytes", name, got, size(t, path), firstEnd)
		}
		appendAll(t, l, "third")
		if _, got := open(t, path); !slices.Equal(got, []string{"first", "third"}) {
			t.Errorf("%s: after append, replayed %q", name, got)
		}
	}
}

// Damage before the last record is not a torn write, and is reported.
func TestCorrupt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log")
	l, _ := open(t, path)
	appendAll(t, l, "first", "second")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[headerSize] ^= 1
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, func([]byte) error { return nil }); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open = %v, want ErrCorrupt", err)
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log")
	l, _ := open(t, path)
	appendAll(t, l, "a")
	if err := l.Append(nil); err == nil {
		t.Error("appended an empty record")
	}
	boom := errors.New("boom")
	if _, err := Open(path, func([]byte) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("Open = %v, want the replay error", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Append([]byte("b")); err == nil {
		t.Error("appended to a closed log")
	}
	if err := l.Append([]byte("c")); err == nil {
		t.Error("appended after a failed append")
	}
}

// Compact keeps the chosen records in order; appends continue after them,
// and a reopened log replays exactly those.
func TestCompact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	l, _ := open(t, path)
	appendAll(t, l, "a1", "b1", "a2", "b2", "a3")
	before := size(t, path)
	if err := l.Compact(func(p []byte) (bool, error) { return p[0] == 'a', nil }); err != nil {
		t.Fatal(err)
	}
	if size(t, path) >= before {
		t.Errorf("size %d after compaction, %d before", size(t, path), before)
	}
	appendAll(t, l, "c1")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, got := open(t, path); !slices.Equal(got, []string{"a1", "a2", "a3", "c1"}) {
		t.Fatalf("replayed %q", got)
	}
	if _, err := os.Stat(path + compactSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temporary file left: %v", err)
	}
}

// A compaction that fails leaves the log as it was, still appendable.
func TestCompactFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	l, _ := open(t, path)
	appendAll(t, l, "a", "b")
	boom := errors.New("boom")
	if err := l.Compact(func([]byte) (bool, error) { return false, boom }); !errors.Is(err, boom) {
		t.Fatalf("Compact returned %v", err)
	}
	appendAll(t, l, "c")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, got := open(t, path); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("replayed %q", got)
	}
}

// A crash during compaction, before the rename, leaves the old log and a
// temporary file, which Open removes.
func TestOpenRemovesStaleCompaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	l, _ := open(t, path)
	appendAll(t, l, "a")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+compactSuffix, []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, got := open(t, path); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("replayed %q", got)
	}
	if _, err := os.Stat(path + compactSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale temporary file kept: %v", err)
	}
}
