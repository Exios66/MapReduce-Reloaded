// Package partition decides which reduce task receives each intermediate key.
//
// The default partition function is specified in docs/file-format.md
// ("Partitioning"): fnv1a64(key) mod R. Go and Python must agree on it
// exactly, so it is written out here rather than taken from a library.
package partition

const (
	offset64 = 0xcbf29ce484222325 // FNV-1a 64 offset basis
	prime64  = 0x100000001b3      // FNV 64 prime
)

// FNV1a64 returns the 64-bit FNV-1a hash of key's raw bytes.
// It does not allocate.
func FNV1a64(key []byte) uint64 {
	h := uint64(offset64)
	for _, b := range key {
		h ^= uint64(b)
		h *= prime64 // uint64 multiplication wraps mod 2^64, as the spec requires
	}
	return h
}

// Partition returns the reduce task, in 0..r-1, that receives key.
// r is the job's number of reduce tasks. It must be at least 1; the
// coordinator rejects jobs with R = 0, so r == 0 here is a bug and panics.
func Partition(key []byte, r uint32) uint32 {
	if r == 0 {
		panic("partition: number of reduce tasks is 0")
	}
	return uint32(FNV1a64(key) % uint64(r))
}
