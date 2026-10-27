package task

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"iter"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// TestReduceMemoryBounded merges 100 map outputs, 80 MB in all, through
// a reduce that streams each group's values out, and checks that the live
// heap stays near what the open readers buffer, not near the input: the
// merge reads each source a block at a time (§3.1, steps 5 and 6).
func TestReduceMemoryBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("merges 80 MB")
	}
	const sources, perSource = 100, 8000 // records of 100 bytes
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(1, 2))
	var paths []string
	for i := range sources {
		recs := make([][2][]byte, perSource)
		for j := range recs {
			key, value := make([]byte, 10), make([]byte, 90)
			binary.BigEndian.PutUint64(key, rng.Uint64())
			for k := range value {
				value[k] = 'a' + byte(rng.IntN(26))
			}
			recs[j] = [2][]byte{key, value}
		}
		slices.SortFunc(recs, func(a, b [2][]byte) int { return bytes.Compare(a[0], b[0]) })
		path := filepath.Join(dir, fmt.Sprint(i))
		if _, err := writeKV(path, kv.CodecZstd, &pairs{recs: recs, i: -1}); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	in, closeAll, err := OpenSources(paths)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeAll() }()

	defer debug.SetGCPercent(debug.SetGCPercent(-1)) // GC only when sample asks
	var m runtime.MemStats
	sample := func() uint64 {
		runtime.GC()
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	spec := api.JobSpec{Output: filepath.Join(t.TempDir(), "out"), R: 1, OutputFormat: "kv"}
	if err := Setup(t.Context(), spec.Output); err != nil {
		t.Fatal(err)
	}
	var base, peak uint64
	groups := 0
	spec.Program.Reducer = api.ReduceFunc(func(_ api.Context, key []byte, values iter.Seq[[]byte], out api.Emitter) error {
		if groups%(sources*perSource/40) == 0 {
			peak = max(peak, sample())
		}
		groups++
		for v := range values {
			if err := out.Emit(key, v); err != nil {
				return err
			}
		}
		return nil
	})
	base = sample()
	counts, err := Reduce(t.Context(), spec, api.TaskID{Kind: api.ReduceTask}, in, nil, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if got := counts[ReduceOutputRecords]; got != sources*perSource {
		t.Fatalf("%d records out, want %d", got, sources*perSource)
	}
	used := peak - min(base, peak)
	t.Logf("peak heap %.1f MB merging %d sources, %d MB in all", float64(used)/(1<<20), sources, sources*perSource*100>>20)
	// Each reader buffers a block (64 KB), decompressed and as stored, and
	// 4 KB of file: about 132 KB a source, so 13 MB here.
	if limit := uint64(sources * 256 << 10); used > limit {
		t.Errorf("peak heap %d bytes, want at most %d (256 KB a source)", used, limit)
	}
	if err := Cleanup(t.Context(), spec.Output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(spec.Output, "part-r-00000")); err != nil {
		t.Fatal(err)
	}
}

// pairs is a slice of records as extsort.Records.
type pairs struct {
	recs [][2][]byte
	i    int
}

func (p *pairs) Next() bool    { p.i++; return p.i < len(p.recs) }
func (p *pairs) Key() []byte   { return p.recs[p.i][0] }
func (p *pairs) Value() []byte { return p.recs[p.i][1] }
func (p *pairs) Err() error    { return nil }
