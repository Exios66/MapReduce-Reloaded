// Word count as a job file (ADR 0015): submit it with
//
//	bin/mr submit -m 8 -r 4 -wait examples/wordcount.go /data/out/wc '/data/input/corpus/*.txt'
//
// or run it in one process with mr run -local. Its output is the same as the
// compiled-in wordcount program's, byte for byte.
package job

import (
	"bytes"
	"strconv"

	"mapreduce/api"
)

// Map emits each ASCII-whitespace-delimited word in value with a count of 1,
// returning the first emission error. For text input, key is the byte
// offset of the line and value is the line.
func Map(ctx api.Context, key, value []byte, out api.Emitter) error {
	for _, w := range bytes.FieldsFunc(value, isSpace) {
		if err := out.Emit(w, []byte("1")); err != nil {
			return err
		}
	}
	return nil
}

// isSpace is the same word boundary the registered wordcount uses: ASCII
// whitespace only, so both versions give the same output.
func isSpace(r rune) bool { return r == ' ' || '\t' <= r && r <= '\r' }

// Reduce sums base-10 int64 counts for key and emits their decimal total,
// returning any emission error. Numeric parse errors are ignored.
// values yields each value in turn; returning false from its callback
// would stop iteration early.
func Reduce(ctx api.Context, key []byte, values func(yield func([]byte) bool), out api.Emitter) error {
	var n int64
	values(func(v []byte) bool {
		x, _ := strconv.ParseInt(string(v), 10, 64)
		n += x
		return true
	})
	return out.Emit(key, strconv.AppendInt(nil, n, 10))
}

// Combine is optional. If present, it runs on map output (§4.3) and must
// obey the rules in api.Combiner.
var Combine = Reduce
