// Package wordcount is the paper's word count (§2), registered as
// "wordcount". Import it for its side effect to make it runnable by name.
//
// A word is a maximal run of bytes that are not ASCII whitespace (space and
// \t \n \v \f \r), so the Go and Python versions agree on every input.
package wordcount

import (
	"bytes"
	"iter"
	"strconv"

	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Program counts words. Its reducer doubles as the combiner.
var Program = api.Program{
	Mapper:   api.MapFunc(Map),
	Reducer:  api.ReduceFunc(Sum),
	Combiner: api.ReduceFunc(Sum),
}

func init() { api.Register("wordcount", Program) }

var one = []byte("1")

// Map emits (word, "1") for each word of a line.
func Map(_ api.Context, _, line []byte, out api.Emitter) error {
	for _, word := range bytes.FieldsFunc(line, isSpace) {
		if err := out.Emit(word, one); err != nil {
			return err
		}
	}
	return nil
}

// Sum emits the sum of a word's counts.
func Sum(_ api.Context, word []byte, counts iter.Seq[[]byte], out api.Emitter) error {
	var total int64
	for c := range counts {
		n, err := strconv.ParseInt(string(c), 10, 64)
		if err != nil {
			return err
		}
		total += n
	}
	return out.Emit(word, strconv.AppendInt(nil, total, 10))
}

func isSpace(r rune) bool { return r == ' ' || '\t' <= r && r <= '\r' }
