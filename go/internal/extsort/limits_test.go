package extsort

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
)

// gen fills key and value with a pseudo-random record: a 10-byte key, as
// in distsort, and a 90-byte value.
func gen(rng *rand.Rand, key, value []byte) {
	binary.LittleEndian.PutUint64(key, rng.Uint64())
	binary.LittleEndian.PutUint16(key[8:], uint16(rng.Uint32()))
	for i := range value {
		value[i] = 'a' + byte(rng.IntN(26))
	}
}

// heapPeak samples the live heap: sample records the heap in use now, and
// peak returns the most seen, less the heap in use when it was made.
type heapPeak struct{ base, max uint64 }

func newHeapPeak() *heapPeak {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return &heapPeak{base: m.HeapAlloc}
}

func (h *heapPeak) sample() {
	runtime.GC() // so that HeapAlloc counts what is live, not garbage
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	h.max = max(h.max, m.HeapAlloc)
}

func (h *heapPeak) peak() uint64 { return h.max - min(h.base, h.max) }

// TestSortMemoryBounded sorts 20 times its budget and checks that the live
// heap stays within a few budgets, while adding records and while merging
// the runs: a sorter that kept everything in memory would hold the whole
// input (§3.1, step 5; ADR 0004).
func TestSortMemoryBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("sorts 80 MB")
	}
	defer debug.SetGCPercent(debug.SetGCPercent(-1)) // GC only when sample asks
	const budget = 4 << 20
	const input = 20 * budget
	for _, codec := range []kv.Codec{kv.CodecNone, kv.CodecZstd} {
		t.Run(codec.String(), func(t *testing.T) {
			warm(t, codec)
			s := New(Config{Dir: t.TempDir(), Budget: budget, Codec: codec})
			defer func() { _ = s.Close() }()
			h := newHeapPeak()
			rng := rand.New(rand.NewPCG(1, 2))
			key, value := make([]byte, 10), make([]byte, 90)
			n := input / (len(key) + len(value))
			for i := range n {
				gen(rng, key, value)
				if err := s.Add(key, value); err != nil {
					t.Fatal(err)
				}
				if i%(n/40) == 0 {
					h.sample()
				}
			}
			addPeak := h.peak()
			it, err := s.Sorted()
			if err != nil {
				t.Fatal(err)
			}
			var prev []byte
			for i := 0; it.Next(); i++ {
				if string(it.Key()) < string(prev) {
					t.Fatalf("record %d out of order", i)
				}
				prev = append(prev[:0], it.Key()...)
				if i%(n/40) == 0 {
					h.sample()
				}
			}
			if err := it.Err(); err != nil {
				t.Fatal(err)
			}
			t.Logf("%d runs; peak heap %.1f MB adding, %.1f MB merging; budget %d MB, input %d MB",
				s.Runs(), float64(addPeak)/(1<<20), float64(h.peak())/(1<<20), budget>>20, input>>20)
			if s.Runs() < 20 {
				t.Errorf("%d runs, want at least 20", s.Runs())
			}
			if limit := uint64(4 * budget); h.peak() > limit {
				t.Errorf("peak heap %d bytes, want at most %d (4 budgets; the input is %d)", h.peak(), limit, input)
			}
		})
	}
}

// warm writes and reads a block with codec, so that the codec's encoder
// and decoder, which every file in the process shares, exist before the
// heap is measured: they are a fixed cost, not one that grows with the
// input.
func warm(t *testing.T, codec kv.Codec) {
	t.Helper()
	var buf bytes.Buffer
	w := kv.NewWriterCodec(&buf, codec)
	value := bytes.Repeat([]byte("x"), kv.BlockTarget)
	if err := errors.Join(w.Write([]byte("k"), value), w.Close()); err != nil {
		t.Fatal(err)
	}
	r := kv.NewReader(&buf)
	for r.Next() {
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
}

func benchmarkSort(b *testing.B, n, budget int, codec kv.Codec) {
	rng := rand.New(rand.NewPCG(1, 2))
	keys, values := make([][]byte, n), make([][]byte, n)
	for i := range n {
		keys[i], values[i] = make([]byte, 10), make([]byte, 90)
		gen(rng, keys[i], values[i])
	}
	b.SetBytes(int64(n * 100))
	for b.Loop() {
		s := New(Config{Dir: b.TempDir(), Budget: budget, Codec: codec})
		for i := range n {
			if err := s.Add(keys[i], values[i]); err != nil {
				b.Fatal(err)
			}
		}
		it, err := s.Sorted()
		if err != nil {
			b.Fatal(err)
		}
		for it.Next() {
		}
		if err := it.Err(); err != nil {
			b.Fatal(err)
		}
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// 10 MB of 100-byte records, sorted in memory, and with a budget a tenth
// of that, so that it spills 10 runs and merges them.
func BenchmarkSortInMemory(b *testing.B) { benchmarkSort(b, 100_000, 64<<20, kv.CodecNone) }
func BenchmarkSortSpill(b *testing.B)    { benchmarkSort(b, 100_000, 1<<20, kv.CodecNone) }
func BenchmarkSortSpillZstd(b *testing.B) {
	benchmarkSort(b, 100_000, 1<<20, kv.CodecZstd)
}
