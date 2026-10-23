// Package distsort is the paper's distributed sort (§5.3), registered as
// "distsort". It outputs the input's lines in byte order across all R output
// files: every line in part-r-00000 sorts before every line in part-r-00001,
// and so on, so concatenating the files in order gives the whole input
// sorted.
//
// Each reduce task sorts its own keys already (§4.2); what makes the order
// global is the partitioner, which splits the key space into R ranges by a
// key's first byte. The paper picks the split points from a sample of the
// keys; first bytes suit keys spread evenly over all byte values, as in its
// benchmark's random keys.
package distsort

import (
	"github.com/exios66/mapreduce-reloaded/go/examples/grep"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Program sorts lines.
var Program = api.Program{
	Mapper:      api.MapFunc(Map),
	Reducer:     api.ReduceFunc(grep.Identity),
	Partitioner: api.PartitionFunc(Partition),
}

func init() { api.Register("distsort", Program) }

// Map emits (line, "").
func Map(_ api.Context, _, line []byte, out api.Emitter) error { return out.Emit(line, nil) }

// Partition sends a key to the range of r that holds its first byte. It
// never sends a key to a lower partition than a smaller key.
func Partition(key []byte, r uint32) uint32 {
	if len(key) == 0 {
		return 0
	}
	return uint32(uint64(key[0]) * uint64(r) / 256)
}
