package api

import (
	"fmt"
	"math"
	"strconv"
)

// Words returns the words of line: maximal runs of bytes that are not ASCII
// whitespace (space and \t \n \v \f \r), the same words as
// bytes.FieldsFunc with that test. The words share line's memory.
//
// Job files should call it rather than split lines themselves: interpreted
// code that tests each byte runs dozens of times slower (ADR 0015).
func Words(line []byte) [][]byte {
	var words [][]byte
	start := -1
	for i, c := range line {
		if c == ' ' || '\t' <= c && c <= '\r' {
			if start >= 0 {
				words = append(words, line[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		words = append(words, line[start:])
	}
	return words
}

// Sum emits key with the total of its values, which are base-10 int64
// counts. It fails, emitting nothing, on a value that does not parse as an
// int64 or on a total that overflows one. It suits counting jobs as
// a Reducer and a Combiner. values is a plain func rather than an
// iter.Seq, so a job file can use it directly (var Combine = api.Sum);
// compiled code passes an iter.Seq[[]byte] unchanged.
func Sum(_ Context, key []byte, values func(yield func([]byte) bool), out Emitter) error {
	var total int64
	var err error
	values(func(v []byte) bool {
		var n int64
		if n, err = strconv.ParseInt(string(v), 10, 64); err != nil {
			return false
		}
		if n > 0 && total > math.MaxInt64-n || n < 0 && total < math.MinInt64-n {
			err = fmt.Errorf("api.Sum: the total for %q overflows int64", key)
			return false
		}
		total += n
		return true
	})
	if err != nil {
		return err
	}
	return out.Emit(key, strconv.AppendInt(nil, total, 10))
}
