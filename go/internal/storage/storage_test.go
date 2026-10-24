package storage_test

import (
	"io"
	"path/filepath"
	"slices"
	"testing"

	. "github.com/exios66/mapreduce-reloaded/go/internal/storage"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage/s3test"
)

func TestScheme(t *testing.T) {
	for name, want := range map[string]string{
		"/data/in.txt":         "",
		"in.txt":               "",
		"s3://bucket/key":      "s3",
		"dir/a://b":            "",
		`C:\data\in.txt`:       "",
		"file:///data/in.txt":  "file",
		"s3://bucket/a/b*.txt": "s3",
	} {
		if got := Scheme(name); got != want {
			t.Errorf("Scheme(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestJoin(t *testing.T) {
	if got, want := Join("out", "_temporary", "part"), filepath.Join("out", "_temporary", "part"); got != want {
		t.Errorf("Join = %q, want %q", got, want)
	}
	if got, want := Join("s3://b/out/", "_temporary", "part"), "s3://b/out/_temporary/part"; got != want {
		t.Errorf("Join = %q, want %q", got, want)
	}
}

func TestUnknownScheme(t *testing.T) {
	if _, err := Open(t.Context(), "gs://bucket/key", 0); err == nil {
		t.Error("Open of gs:// succeeded, want an error")
	}
}

func TestLocal(t *testing.T) { testBackend(t, filepath.Join(t.TempDir(), "out")) }

func TestS3(t *testing.T) {
	bucket := s3test.Start(t)
	testBackend(t, bucket+"/out")
	w, err := Create(t.Context(), bucket+"/other/object")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := DeleteAll(t.Context(), bucket); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"out", "other"} {
		if left, err := List(t.Context(), bucket+"/"+dir+"/*"); err != nil || len(left) > 0 {
			t.Errorf("after DeleteAll of the bucket, List of %s = %q, %v", dir, left, err)
		}
	}
}

// testBackend writes, commits, lists, reads and deletes files under dir.
func testBackend(t *testing.T, dir string) {
	ctx := t.Context()
	tmp := Join(dir, "_temporary")
	if err := MkdirAll(ctx, tmp); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		w, err := Create(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"b", "a"} {
		write(Join(tmp, name), "hello "+name)
		if err := Commit(ctx, Join(tmp, name), Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	write(Join(tmp, "c"), "left behind")

	list := func(pattern string, want ...string) {
		t.Helper()
		got, err := List(ctx, pattern)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("List(%q) = %q, %v, want %q", pattern, got, err, want)
		}
	}
	list(Join(dir, "?"), Join(dir, "a"), Join(dir, "b"))
	list(Join(tmp, "*"), Join(tmp, "c"))
	list(Join(dir, "part-*"))
	if size, err := Size(ctx, Join(dir, "a")); err != nil || size != 7 {
		t.Errorf("Size = %d, %v, want 7", size, err)
	}
	for offset, want := range map[int64]string{0: "hello a", 6: "a"} {
		r, err := Open(ctx, Join(dir, "a"), offset)
		if err != nil {
			t.Fatal(err)
		}
		if b, err := io.ReadAll(r); err != nil || string(b) != want {
			t.Errorf("read from offset %d = %q, %v, want %q", offset, b, err, want)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}

	if err := Delete(ctx, Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	list(Join(dir, "?"), Join(dir, "a"))
	if err := DeleteAll(ctx, tmp); err != nil {
		t.Fatal(err)
	}
	list(Join(tmp, "*"))
	list(Join(dir, "?"), Join(dir, "a"))
	if err := DeleteAll(ctx, tmp); err != nil {
		t.Errorf("DeleteAll of a missing directory = %v", err)
	}
}
