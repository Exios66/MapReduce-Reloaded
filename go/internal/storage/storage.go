// Package storage reads and writes a job's input and output files, the role
// GFS plays in the paper (§3.1). A file is named by a local path, or by a URI
// whose scheme picks the backend that stores it.
//
// Intermediate map output is not job input or output: it stays on the
// worker's local disk (§3.1), and the shuffle serves it from there.
package storage

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Backend is one kind of storage.
type Backend interface {
	// List returns the names that match pattern, in path.Match syntax, sorted.
	List(ctx context.Context, pattern string) ([]string, error)
	// Size returns the size of a file in bytes.
	Size(ctx context.Context, name string) (int64, error)
	// Open returns a reader for a file from offset to its end.
	Open(ctx context.Context, name string, offset int64) (io.ReadCloser, error)
	// Create creates or truncates a file for writing. Close completes it.
	Create(ctx context.Context, name string) (io.WriteCloser, error)
	// Commit makes the complete file tmp visible under name, replacing any
	// file there: a reader sees either the old file or all of the new one.
	Commit(ctx context.Context, tmp, name string) error
	// Delete removes a file.
	Delete(ctx context.Context, name string) error
	// MkdirAll creates a directory and its parents.
	MkdirAll(ctx context.Context, dir string) error
	// DeleteAll removes a directory and everything under it. A missing
	// directory is not an error.
	DeleteAll(ctx context.Context, dir string) error
}

var backends = map[string]Backend{"": Local{}, "s3": &S3{}}

// Register makes b the backend for names with the given scheme, replacing
// any backend there. It is not safe to call while files are in use.
func Register(scheme string, b Backend) { backends[scheme] = b }

// For returns the backend that stores name.
func For(name string) (Backend, error) {
	scheme := Scheme(name)
	b, ok := backends[scheme]
	if !ok {
		return nil, fmt.Errorf("storage: no backend for %s:// in %q", scheme, name)
	}
	return b, nil
}

// Scheme returns the URI scheme of name, or "" for a local path.
func Scheme(name string) string {
	scheme, _, ok := strings.Cut(name, "://")
	if !ok || strings.ContainsAny(scheme, `/\`) {
		return ""
	}
	return scheme
}

// Join joins name and elems with the separator of name's backend.
func Join(name string, elems ...string) string {
	if Scheme(name) == "" {
		return filepath.Join(append([]string{name}, elems...)...)
	}
	return strings.Join(append([]string{strings.TrimSuffix(name, "/")}, elems...), "/")
}

// Escape returns name with the characters that List treats as pattern syntax
// escaped, so that it matches only itself. Use it on the literal part of a
// pattern, such as a directory a caller chose.
func Escape(name string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(name)
}

// List returns the names that match pattern, sorted.
func List(ctx context.Context, pattern string) ([]string, error) {
	b, err := For(pattern)
	if err != nil {
		return nil, err
	}
	return b.List(ctx, pattern)
}

// Size returns the size of a file in bytes.
func Size(ctx context.Context, name string) (int64, error) {
	b, err := For(name)
	if err != nil {
		return 0, err
	}
	return b.Size(ctx, name)
}

// Open returns a reader for a file from offset to its end.
func Open(ctx context.Context, name string, offset int64) (io.ReadCloser, error) {
	b, err := For(name)
	if err != nil {
		return nil, err
	}
	return b.Open(ctx, name, offset)
}

// Create creates or truncates a file for writing.
func Create(ctx context.Context, name string) (io.WriteCloser, error) {
	b, err := For(name)
	if err != nil {
		return nil, err
	}
	return b.Create(ctx, name)
}

// Commit makes the complete file tmp visible under name. Both must be in the
// same backend.
func Commit(ctx context.Context, tmp, name string) error {
	b, err := For(name)
	if err != nil {
		return err
	}
	if Scheme(tmp) != Scheme(name) {
		return fmt.Errorf("storage: cannot commit %q as %q", tmp, name)
	}
	return b.Commit(ctx, tmp, name)
}

// Delete removes a file.
func Delete(ctx context.Context, name string) error {
	b, err := For(name)
	if err != nil {
		return err
	}
	return b.Delete(ctx, name)
}

// MkdirAll creates a directory and its parents.
func MkdirAll(ctx context.Context, dir string) error {
	b, err := For(dir)
	if err != nil {
		return err
	}
	return b.MkdirAll(ctx, dir)
}

// DeleteAll removes a directory and everything under it.
func DeleteAll(ctx context.Context, dir string) error {
	b, err := For(dir)
	if err != nil {
		return err
	}
	return b.DeleteAll(ctx, dir)
}
