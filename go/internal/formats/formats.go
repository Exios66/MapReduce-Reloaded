// Package formats provides the input and output formats a JobSpec names
// (§4.4): "text" for lines of text, and "kv" for MRKV files.
package formats

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
)

// MaxSplits is the most splits one file may be cut into. It is the limit on
// a job's map tasks (api.MaxMapTasks), enforced here because the list of
// splits is built in memory before the job can be rejected.
const MaxSplits = 100_000

// Split is the byte range [Start, End) of one input file, the unit of work of
// one map task. Path names the file in storage.
type Split struct {
	Path       string
	Start, End int64
}

// InputFormat divides input files into splits and reads records from them.
type InputFormat interface {
	// Splits divides the file at path into splits of about splitSize bytes.
	// An empty file has no splits.
	Splits(ctx context.Context, path string, splitSize int64) ([]Split, error)
	// Open returns a reader for the records of one split.
	Open(ctx context.Context, s Split) (RecordReader, error)
}

// RecordReader iterates over records, like kv.Reader: Key and Value are
// valid until the next call to Next, and Err reports why Next returned false.
type RecordReader interface {
	Next() bool
	Key() []byte
	Value() []byte
	Err() error
	io.Closer
}

// OutputFormat creates output files.
type OutputFormat interface {
	Create(ctx context.Context, path string) (RecordWriter, error)
}

// RecordWriter writes records. Close flushes them and closes the file.
type RecordWriter interface {
	Write(key, value []byte) error
	io.Closer
}

var (
	inputs  = map[string]InputFormat{"text": Text{}, "kv": KV{}}
	outputs = map[string]OutputFormat{"text": Text{}, "kv": KV{}}
)

// Input returns the input format with the given name. "" means "text".
func Input(name string) (InputFormat, error) { return lookup(inputs, name) }

// Output returns the output format with the given name, writing blocks
// compressed with codec. "" means "text". Only "kv" takes a codec.
func Output(name string, codec kv.Codec) (OutputFormat, error) {
	f, err := lookup(outputs, name)
	if err != nil || codec == kv.CodecNone {
		return f, err
	}
	k, ok := f.(KV)
	if !ok {
		return nil, fmt.Errorf("formats: output format %q takes no codec; only kv does", cmp.Or(name, "text"))
	}
	k.Codec = codec
	return k, nil
}

func lookup[F any](formats map[string]F, name string) (F, error) {
	if name == "" {
		name = "text"
	}
	f, ok := formats[name]
	if !ok {
		return f, fmt.Errorf("formats: unknown format %q", name)
	}
	return f, nil
}

// byteSplits cuts the file at path into consecutive ranges of splitSize bytes.
func byteSplits(ctx context.Context, path string, splitSize int64) ([]Split, error) {
	if splitSize <= 0 {
		return nil, errors.New("formats: split size must be positive")
	}
	size, err := storage.Size(ctx, path)
	if err != nil {
		return nil, err
	}
	// Count before allocating: a tiny split size on a large file would
	// otherwise build the whole list before any limit could reject it.
	n := size / splitSize
	if size%splitSize != 0 {
		n++
	}
	if n > MaxSplits {
		return nil, fmt.Errorf("formats: %s would be cut into %d splits; the most is %d (raise the split size)", path, n, MaxSplits)
	}
	splits := slices.Grow([]Split(nil), int(n))
	for start := int64(0); start < size; start += splitSize {
		splits = append(splits, Split{path, start, min(start+splitSize, size)})
	}
	return splits, nil
}
