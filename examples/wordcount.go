// Word count as a job file (ADR 0015): submit it with
//
//	bin/mr submit -m 8 -r 4 -wait examples/wordcount.go /data/out/wc '/data/input/corpus/*.txt'
//
// or run it in one process with mr run -local. Its output is the same as the
// compiled-in wordcount program's, byte for byte.
package job

import (
	"strconv"

	"mapreduce/api"
)

// Map emits each ASCII-whitespace-delimited word in value with a count of 1,
// returning the first emission error. For text input, key is the byte
// offset of the line and value is the line. api.Words is compiled code:
// splitting the line here, byte by byte, would run in the interpreter and
// make the job several times slower (ADR 0015).
func Map(ctx api.Context, key, value []byte, out api.Emitter) error {
	one := []byte("1")
	for _, w := range api.Words(value) {
		if err := out.Emit(w, one); err != nil {
			return err
		}
	}
	return nil
}

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
// obey the rules in api.Combiner. It sees every value a map task emits, so
// it is the compiled api.Sum, which does what Reduce does.
var Combine = api.Sum
