// Package formats provides the input and output formats a JobSpec names
// (§4.4): "text" for lines of text, and "kv" for MRKV files.
package formats

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Split is the byte range [Start, End) of one input file, the unit of work of
// one map task.
type Split struct {
	Path       string
	Start, End int64
}

// InputFormat divides input files into splits and reads records from them.
type InputFormat interface {
	// Splits divides the file at path into splits of about splitSize bytes.
	// An empty file has no splits.
	Splits(path string, splitSize int64) ([]Split, error)
	// Open returns a reader for the records of one split.
	Open(s Split) (RecordReader, error)
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
	Create(path string) (RecordWriter, error)
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

// Output returns the output format with the given name. "" means "text".
func Output(name string) (OutputFormat, error) { return lookup(outputs, name) }

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
func byteSplits(path string, splitSize int64) ([]Split, error) {
	if splitSize <= 0 {
		return nil, errors.New("formats: split size must be positive")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var splits []Split
	for start := int64(0); start < info.Size(); start += splitSize {
		splits = append(splits, Split{path, start, min(start+splitSize, info.Size())})
	}
	return splits, nil
}
