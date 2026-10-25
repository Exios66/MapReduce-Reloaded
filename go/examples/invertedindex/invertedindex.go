// Package invertedindex is the paper's inverted index (§2.3), registered as
// "invertedindex". It outputs each word, split as word count splits them,
// with the input files it occurs in: their base names, sorted, without
// duplicates, and joined by commas.
package invertedindex

import (
	"bytes"
	"iter"
	"path/filepath"
	"slices"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Program indexes words. Its combiner drops a map task's repeats of a
// (word, file) pair.
var Program = api.Program{
	Mapper:   api.MapFunc(Map),
	Reducer:  api.ReduceFunc(Reduce),
	Combiner: api.ReduceFunc(Distinct),
}

func init() { api.Register("invertedindex", Program) }

// Map emits (word, file) for each word of a line.
func Map(ctx api.Context, _, line []byte, out api.Emitter) error {
	file := []byte(filepath.Base(ctx.Input()))
	for _, word := range wordcount.Words(line) {
		if err := out.Emit(word, file); err != nil {
			return err
		}
	}
	return nil
}

// Distinct emits each of a word's files once.
func Distinct(_ api.Context, word []byte, files iter.Seq[[]byte], out api.Emitter) error {
	for _, f := range distinct(files) {
		if err := out.Emit(word, f); err != nil {
			return err
		}
	}
	return nil
}

// Reduce emits a word and its sorted, distinct files.
func Reduce(_ api.Context, word []byte, files iter.Seq[[]byte], out api.Emitter) error {
	return out.Emit(word, bytes.Join(distinct(files), []byte(",")))
}

// distinct returns the sorted, distinct values. It keeps one copy of each
// value, so a word that occurs a million times in a few files stays small.
func distinct(values iter.Seq[[]byte]) [][]byte {
	seen := map[string]bool{}
	var all [][]byte
	for v := range values {
		if !seen[string(v)] {
			seen[string(v)] = true
			all = append(all, bytes.Clone(v))
		}
	}
	slices.SortFunc(all, bytes.Compare)
	return all
}
