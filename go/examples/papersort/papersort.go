// Package papersort registers the section 5.3 benchmark's 10-byte-key plus
// original-record representation. Distsort remains the whole-line variant.
package papersort

import (
	"fmt"

	"github.com/exios66/mapreduce-reloaded/go/examples/distsort"
	"github.com/exios66/mapreduce-reloaded/go/examples/grep"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Program sorts 100-byte benchmark records (99 bytes without the newline).
var Program = api.Program{
	Mapper:      api.MapFunc(Map),
	Reducer:     api.ReduceFunc(grep.Identity),
	Partitioner: api.PartitionFunc(distsort.Partition),
}

func init() { api.Register("papersort", Program) }

// Map preserves the original record as the value, rather than as the key.
func Map(_ api.Context, _, line []byte, out api.Emitter) error {
	if len(line) != 99 {
		return fmt.Errorf("papersort: record has %d bytes without newline, want 99", len(line))
	}
	return out.Emit(line[:10], line)
}
