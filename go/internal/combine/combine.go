// Package combine runs a user's Combiner on sorted map output before it is
// spilled (§4.3).
package combine

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/exios66/mapreduce-reloaded/go/internal/extsort"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Counter names, reported through api.Context.Count. Their ratio shows how
// much the combiner shrank the map output.
const (
	InputRecords  = "combine.input.records"
	OutputRecords = "combine.output.records"
)

// Func adapts c to an extsort.CombineFunc: each key's records are reduced by
// c, which may emit any number of records but only for that same key, so the
// output stays sorted.
func Func(ctx api.Context, c api.Combiner) extsort.CombineFunc {
	return func(in extsort.Records, emit func(key, value []byte) error) error {
		counted := &countedRecords{Records: in}
		var out int64
		var err error
		for key, values := range extsort.Groups(counted) {
			err = c.Reduce(ctx, key, values, emitFunc(func(k, v []byte) error {
				if !bytes.Equal(k, key) {
					return fmt.Errorf("combine: combiner emitted key %q while combining %q", k, key)
				}
				out++
				return emit(k, v)
			}))
			if err != nil {
				break
			}
		}
		ctx.Count(InputRecords, counted.n)
		ctx.Count(OutputRecords, out)
		return errors.Join(err, in.Err())
	}
}

type countedRecords struct {
	extsort.Records
	n int64
}

func (c *countedRecords) Next() bool {
	ok := c.Records.Next()
	if ok {
		c.n++
	}
	return ok
}

type emitFunc func(key, value []byte) error

func (f emitFunc) Emit(key, value []byte) error { return f(key, value) }
