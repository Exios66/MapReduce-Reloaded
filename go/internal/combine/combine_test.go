package combine_test

import (
	"fmt"
	"io"
	"iter"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/combine"
	"github.com/exios66/mapreduce-reloaded/go/internal/extsort"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// sum is word count's reducer, which is also its combiner.
var sum = api.ReduceFunc(func(_ api.Context, key []byte, values iter.Seq[[]byte], out api.Emitter) error {
	total := 0
	for v := range values {
		n, err := strconv.Atoi(string(v))
		if err != nil {
			return err
		}
		total += n
	}
	return out.Emit(key, strconv.AppendInt(nil, int64(total), 10))
})

func words(n int) []string {
	rng := rand.New(rand.NewPCG(3, 4))
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("w%d", rng.IntN(40))
	}
	return out
}

type record struct{ key, value string }

// mapSide sorts word-count map output for words, combining each spill if
// combiner is set, and returns the sorter's records.
func mapSide(t *testing.T, ctx api.Context, words []string, combiner api.Combiner) ([]record, *extsort.Sorter) {
	t.Helper()
	cfg := extsort.Config{Dir: t.TempDir(), Budget: 512}
	if combiner != nil {
		cfg.Combine = combine.Func(ctx, combiner)
	}
	s := extsort.New(cfg)
	t.Cleanup(func() { _ = s.Close() })
	for _, w := range words {
		if err := s.Add([]byte(w), []byte("1")); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Sorted()
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, r), s
}

// reduce applies sum to sorted records, as the reduce side would.
func reduce(t *testing.T, recs []record) []record {
	t.Helper()
	var out []record
	r := &memRecords{recs: recs, i: -1}
	emit := emitter(func(k, v []byte) error { out = append(out, record{string(k), string(v)}); return nil })
	for key, values := range extsort.Groups(r) {
		if err := sum.Reduce(newContext(), key, values, emit); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestCombinerPreservesOutput(t *testing.T) {
	in := words(2000)
	ctx := newContext()
	plain, plainSorter := mapSide(t, newContext(), in, nil)
	combined, combinedSorter := mapSide(t, ctx, in, sum)

	if !slices.Equal(reduce(t, plain), reduce(t, combined)) {
		t.Fatal("word count differs with the combiner")
	}
	if len(combined) >= len(plain) || combinedSorter.SpilledBytes() >= plainSorter.SpilledBytes() {
		t.Fatalf("combiner did not shrink map output: %d → %d records, %d → %d bytes",
			len(plain), len(combined), plainSorter.SpilledBytes(), combinedSorter.SpilledBytes())
	}
	if ctx.counts[combine.InputRecords] != int64(len(in)) ||
		ctx.counts[combine.OutputRecords] != int64(len(combined)) {
		t.Fatalf("counters %v, want %d in and %d out", ctx.counts, len(in), len(combined))
	}
}

// TestCombinerAppliedAnyNumberOfTimes checks the §4.3 requirement on a
// combiner: applying it 0, 1 or more times does not change the result.
func TestCombinerAppliedAnyNumberOfTimes(t *testing.T) {
	recs, _ := mapSide(t, newContext(), words(500), nil)
	want := reduce(t, recs)
	apply := combine.Func(newContext(), sum)
	for n := range 4 {
		if got := reduce(t, recs); !slices.Equal(got, want) {
			t.Fatalf("combined %d times: output differs", n)
		}
		var next []record
		err := apply(&memRecords{recs: recs, i: -1}, func(k, v []byte) error {
			next = append(next, record{string(k), string(v)})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		recs = next
	}
}

func TestCombinerMustKeepKey(t *testing.T) {
	rename := api.ReduceFunc(func(_ api.Context, key []byte, _ iter.Seq[[]byte], out api.Emitter) error {
		return out.Emit(append([]byte("x"), key...), nil)
	})
	err := combine.Func(newContext(), rename)(&memRecords{recs: []record{{"a", "1"}}, i: -1}, nil)
	if err == nil || !strings.Contains(err.Error(), `emitted key "xa"`) {
		t.Fatalf("error = %v, want a key-change error", err)
	}
}

func collect(t *testing.T, r extsort.Records) []record {
	t.Helper()
	var out []record
	for r.Next() {
		out = append(out, record{string(r.Key()), string(r.Value())})
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type memRecords struct {
	recs []record
	i    int
}

func (m *memRecords) Next() bool    { m.i++; return m.i < len(m.recs) }
func (m *memRecords) Key() []byte   { return []byte(m.recs[m.i].key) }
func (m *memRecords) Value() []byte { return []byte(m.recs[m.i].value) }
func (m *memRecords) Err() error    { return nil }

type emitter func(key, value []byte) error

func (f emitter) Emit(key, value []byte) error { return f(key, value) }

type testContext struct{ counts map[string]int64 }

func newContext() *testContext { return &testContext{counts: map[string]int64{}} }

func (c *testContext) Task() api.TaskID                     { return api.TaskID{Kind: api.MapTask} }
func (c *testContext) Param(string) string                  { return "" }
func (c *testContext) Count(name string, delta int64)       { c.counts[name] += delta }
func (c *testContext) Logger() *slog.Logger                 { return slog.New(slog.DiscardHandler) }
func (c *testContext) SideOutput(string) (io.Writer, error) { return io.Discard, nil }
