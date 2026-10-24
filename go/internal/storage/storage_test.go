package storage

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
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

func TestLocal(t *testing.T) {
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "out")
	if err := MkdirAll(ctx, Join(dir, "tmp")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b", "a"} {
		w, err := Create(ctx, Join(dir, "tmp", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, "hello "+name); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := Commit(ctx, Join(dir, "tmp", name), Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}

	names, err := List(ctx, Join(dir, "*"))
	if want := []string{Join(dir, "a"), Join(dir, "b"), Join(dir, "tmp")}; err != nil || !slices.Equal(names, want) {
		t.Fatalf("List = %q, %v, want %q", names, err, want)
	}
	if size, err := Size(ctx, Join(dir, "a")); err != nil || size != 7 {
		t.Errorf("Size = %d, %v, want 7", size, err)
	}
	r, err := Open(ctx, Join(dir, "a"), 6)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := io.ReadAll(r); err != nil || string(b) != "a" {
		t.Errorf("read from offset 6 = %q, %v, want %q", b, err, "a")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Delete(ctx, Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	if err := DeleteAll(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("after DeleteAll, Stat = %v, want not exist", err)
	}
	if err := DeleteAll(ctx, dir); err != nil {
		t.Errorf("DeleteAll of a missing directory = %v", err)
	}
}
