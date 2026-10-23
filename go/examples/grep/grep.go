// Package grep is the paper's distributed grep (§2.3), registered as
// "grep": it outputs each input line that contains the job's "pattern"
// parameter, a literal string, so the Go and Python versions agree.
package grep

import (
	"bytes"
	"errors"
	"iter"

	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Program finds matching lines. Its reducer is the identity, so a line that
// occurs twice is output twice.
var Program = api.Program{
	Mapper:  api.MapFunc(Map),
	Reducer: api.ReduceFunc(Identity),
}

func init() { api.Register("grep", Program) }

// Map emits (line, "") if the line contains the pattern.
func Map(ctx api.Context, _, line []byte, out api.Emitter) error {
	pattern := ctx.Param("pattern")
	if pattern == "" {
		return errors.New("grep: no pattern; set one with -param pattern=<text>")
	}
	if !bytes.Contains(line, []byte(pattern)) {
		return nil
	}
	return out.Emit(line, nil)
}

// Identity emits each of a key's values.
func Identity(_ api.Context, key []byte, values iter.Seq[[]byte], out api.Emitter) error {
	for v := range values {
		if err := out.Emit(key, v); err != nil {
			return err
		}
	}
	return nil
}
