// Package reverselinks is the paper's reverse web-link graph (§2.3),
// registered as "reverselinks". Its input has one link per line, as the
// source page, a tab and the target page. It outputs each target with the
// pages that link to it, sorted, without duplicates, and joined by commas.
// Lines without a tab are skipped.
package reverselinks

import (
	"bytes"
	"iter"
	"slices"

	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Program reverses links.
var Program = api.Program{
	Mapper:  api.MapFunc(Map),
	Reducer: api.ReduceFunc(Reduce),
}

func init() { api.Register("reverselinks", Program) }

// Map emits (target, source) for a link.
func Map(_ api.Context, _, line []byte, out api.Emitter) error {
	source, target, ok := bytes.Cut(line, []byte("\t"))
	if !ok {
		return nil
	}
	return out.Emit(target, source)
}

// Reduce emits a target and the sorted, distinct sources linking to it.
func Reduce(_ api.Context, target []byte, sources iter.Seq[[]byte], out api.Emitter) error {
	var all [][]byte
	for s := range sources {
		all = append(all, bytes.Clone(s))
	}
	slices.SortFunc(all, bytes.Compare)
	return out.Emit(target, bytes.Join(slices.CompactFunc(all, bytes.Equal), []byte(",")))
}
