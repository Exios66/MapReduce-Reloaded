package api_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// wordCount is the paper's example (§2) against this API.
var wordCount = api.Program{
	Mapper: api.MapFunc(func(_ api.Context, _, line []byte, out api.Emitter) error {
		for _, word := range bytes.Fields(line) {
			if err := out.Emit(word, []byte("1")); err != nil {
				return err
			}
		}
		return nil
	}),
	Reducer: api.ReduceFunc(func(_ api.Context, word []byte, counts iter.Seq[[]byte], out api.Emitter) error {
		sum := 0
		for c := range counts {
			n, err := strconv.Atoi(string(c))
			if err != nil {
				return err
			}
			sum += n
		}
		return out.Emit(word, strconv.AppendInt(nil, int64(sum), 10))
	}),
}

func Example_wordCount() {
	out, err := run(wordCount, "the quick fox", "the lazy dog", "the end")
	if err != nil {
		panic(err)
	}
	fmt.Println(strings.Join(out, "\n"))
	// Output:
	// dog 1
	// end 1
	// fox 1
	// lazy 1
	// quick 1
	// the 3
}

func TestHashPartitioner(t *testing.T) {
	// fnv1a64("foobar") = 0x85944171f73967e8; see testdata/hash/vectors.json.
	if got := api.HashPartitioner.Partition([]byte("foobar"), 7); got != 0x85944171f73967e8%7 {
		t.Fatalf("Partition = %d", got)
	}
}

func TestEmitErrorStopsMapper(t *testing.T) {
	boom := errors.New("spill failed")
	err := wordCount.Mapper.Map(testContext{}, nil, []byte("a b"), emitFunc(func(_, _ []byte) error { return boom }))
	if !errors.Is(err, boom) {
		t.Fatalf("Map error = %v, want %v", err, boom)
	}
}

func TestValidate(t *testing.T) {
	valid := api.JobSpec{Program: wordCount, Inputs: []string{"in"}, Output: "out", R: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	if err := (&api.JobSpec{}).Validate(); err == nil ||
		!strings.Contains(err.Error(), "no Mapper") || !strings.Contains(err.Error(), "R must be at least 1") {
		t.Fatalf("empty spec: got %v, want every problem listed", err)
	}
	nilFuncs := valid
	nilFuncs.Program = api.Program{
		Mapper: api.MapFunc(nil), Reducer: api.ReduceFunc(nil),
		Combiner: api.ReduceFunc(nil), Partitioner: api.PartitionFunc(nil),
	}
	err := nilFuncs.Validate()
	for _, want := range []string{"no Mapper", "no Reducer", "Combiner is a nil function", "Partitioner is a nil function"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("nil functions: got %v, want %q", err, want)
		}
	}
}

func TestRegister(t *testing.T) {
	if _, ok := api.Lookup("test.wordcount"); !ok { // registered already under go test -count=N
		api.Register("test.wordcount", wordCount)
	}
	if _, ok := api.Lookup("test.wordcount"); !ok {
		t.Fatal("registered program not found")
	}
	if !slices.Contains(api.Programs(), "test.wordcount") {
		t.Fatalf("Programs() = %v", api.Programs())
	}
	for _, name := range []string{"test.wordcount", ""} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%q) did not panic", name)
				}
			}()
			api.Register(name, wordCount)
		}()
	}
}

func TestTaskKindString(t *testing.T) {
	if got := fmt.Sprint(api.MapTask, api.ReduceTask, api.TaskKind(9)); got != "map reduce TaskKind(9)" {
		t.Fatalf("got %q", got)
	}
}

// run executes p on lines in memory, sequentially: map, group by key, reduce.
// It returns "key value" lines in key order.
func run(p api.Program, lines ...string) ([]string, error) {
	type kv struct{ key, value []byte }
	var inter []kv
	collect := emitFunc(func(k, v []byte) error {
		inter = append(inter, kv{bytes.Clone(k), bytes.Clone(v)})
		return nil
	})
	for i, line := range lines {
		if err := p.Mapper.Map(testContext{}, []byte(strconv.Itoa(i)), []byte(line), collect); err != nil {
			return nil, err
		}
	}
	slices.SortStableFunc(inter, func(a, b kv) int { return bytes.Compare(a.key, b.key) })

	var out []string
	emit := emitFunc(func(k, v []byte) error {
		out = append(out, fmt.Sprintf("%s %s", k, v))
		return nil
	})
	for group := range chunkBy(inter, func(a, b kv) bool { return bytes.Equal(a.key, b.key) }) {
		values := func(yield func([]byte) bool) {
			for _, r := range group {
				if !yield(r.value) {
					return
				}
			}
		}
		if err := p.Reducer.Reduce(testContext{}, group[0].key, values, emit); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// chunkBy yields runs of consecutive equal elements.
func chunkBy[T any](s []T, eq func(a, b T) bool) iter.Seq[[]T] {
	return func(yield func([]T) bool) {
		for len(s) > 0 {
			n := slices.IndexFunc(s, func(x T) bool { return !eq(s[0], x) })
			if n < 0 {
				n = len(s)
			}
			if !yield(s[:n]) {
				return
			}
			s = s[n:]
		}
	}
}

type emitFunc func(key, value []byte) error

func (f emitFunc) Emit(key, value []byte) error { return f(key, value) }

type testContext struct{}

func (testContext) Task() api.TaskID                     { return api.TaskID{Kind: api.MapTask} }
func (testContext) Param(string) string                  { return "" }
func (testContext) Input() string                        { return "" }
func (testContext) Count(string, int64)                  {}
func (testContext) Logger() *slog.Logger                 { return slog.New(slog.DiscardHandler) }
func (testContext) SideOutput(string) (io.Writer, error) { return io.Discard, nil }
