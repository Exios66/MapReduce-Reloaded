package jobfile

import (
	"container/list"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const (
	example = "../../../examples/wordcount.go"
	corpus  = "../../../testdata/corpus"
)

func mustLoad(t *testing.T, src string) api.Program {
	t.Helper()
	p, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The example job file gives the compiled wordcount's expected output, byte
// for byte, with and without its combiner.
func TestExampleMatchesGolden(t *testing.T) {
	src, err := os.ReadFile(example)
	if err != nil {
		t.Fatal(err)
	}
	p := mustLoad(t, string(src))
	if p.Combiner == nil {
		t.Fatal("the example's Combine was not loaded")
	}
	noCombiner := p
	noCombiner.Combiner = nil
	for name, prog := range map[string]api.Program{"combiner": p, "no-combiner": noCombiner} {
		out := t.TempDir()
		spec := api.JobSpec{Program: prog, Inputs: []string{filepath.Join(corpus, "*.txt")}, Output: out, R: 4}
		if _, err := local.Run(spec, t.TempDir()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, _ := filepath.Glob(filepath.Join(corpus, "expected", "*"))
		for _, w := range want {
			a, errA := os.ReadFile(w)
			b, errB := os.ReadFile(filepath.Join(out, filepath.Base(w)))
			if errA != nil || errB != nil || string(a) != string(b) {
				t.Errorf("%s: %s differs from the expected output", name, filepath.Base(w))
			}
		}
	}
}

const minimal = `package job

import "mapreduce/api"

func Map(ctx api.Context, key, value []byte, out api.Emitter) error { return out.Emit(value, key) }

func Reduce(ctx api.Context, key []byte, values func(yield func([]byte) bool), out api.Emitter) error {
	return out.Emit(key, nil)
}
`

// Combine and Partition are optional.
func TestOptionalFunctions(t *testing.T) {
	p := mustLoad(t, minimal)
	if p.Mapper == nil || p.Reducer == nil || p.Combiner != nil || p.Partitioner != nil {
		t.Fatalf("minimal job loaded as %+v", p)
	}
	p = mustLoad(t, minimal+`
var Combine = Reduce

func Partition(key []byte, r uint32) uint32 { return uint32(len(key)) % r }
`)
	if p.Combiner == nil || p.Partitioner == nil {
		t.Fatal("Combine or Partition not loaded")
	}
	if got := p.Partitioner.Partition([]byte("abcde"), 3); got != 2 {
		t.Errorf("Partition(abcde, 3) = %d, want 2", got)
	}
}

// A job file that cannot run is rejected when it is loaded, with a reason.
func TestLoadErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"syntax":         {"package job\nfunc Map(", "job file"},
		"no map":         {strings.Replace(minimal, "func Map(", "func NotMap(", 1), "Map"},
		"no reduce":      {strings.Replace(minimal, "func Reduce(", "func NotReduce(", 1), "Reduce"},
		"wrong map":      {strings.Replace(minimal, "out api.Emitter) error { return out.Emit(value, key) }", "out api.Emitter) { _ = out.Emit(value, key) }", 1), "Map"},
		"wrong package":  {strings.Replace(minimal, "package job", "package main", 1), "package job"},
		"bad import":     {strings.Replace(minimal, `"mapreduce/api"`, "\"mapreduce/api\"\nimport \"github.com/x/y\"", 1), "job file"},
		"too large":      {minimal + "// " + strings.Repeat("x", MaxSize), "MiB"},
		"wrong combine":  {minimal + "\nvar Combine = 3\n", "Combine"},
		"empty":          {"", "job file"},
		"map not a func": {strings.Replace(minimal, "func Map(", "var Map = 1\nfunc unused(", 1), "Map"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got error %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// A job may use the API's task kinds, and os.Exit in it panics instead of
// ending the process.
func TestTaskKindsAndExit(t *testing.T) {
	mustLoad(t, minimal+`
func kind(ctx api.Context) bool { var k api.TaskKind = ctx.Task().Kind; return k == api.MapTask || k == api.ReduceTask }
`)
	_, err := Load(strings.Replace(minimal, `import "mapreduce/api"`, "import (\n\t\"mapreduce/api\"\n\t\"os\"\n)\n\nvar _ = exit()\n\nfunc exit() int { os.Exit(3); return 0 }", 1))
	if err == nil || !strings.Contains(err.Error(), "os.Exit") {
		t.Fatalf("got %v, want an error naming os.Exit", err)
	}
}

// An initializer that never returns fails the load instead of hanging it.
func TestLoadTimeout(t *testing.T) {
	defer func(d time.Duration) { loadTimeout = d }(loadTimeout)
	loadTimeout = 200 * time.Millisecond
	_, err := Load(minimal + "\nvar _ = forever()\n\nfunc forever() int { select {} }\n")
	if err == nil || !strings.Contains(err.Error(), "took over") {
		t.Fatalf("got %v, want a timeout", err)
	}
}

// A panic in the job's Map is returned as an error, not a crash, so the
// worker's bad-record handling (§4.6) sees it like a compiled program's.
func TestMapPanic(t *testing.T) {
	p := mustLoad(t, strings.Replace(minimal, "{ return out.Emit(value, key) }", "{ panic(\"boom\") }", 1))
	out := t.TempDir()
	spec := api.JobSpec{Program: p, Inputs: []string{filepath.Join(corpus, "raven.txt")}, Output: out, R: 1}
	if _, err := local.Run(spec, t.TempDir()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v, want the panic's message", err)
	}
}

// Loading the same source twice reuses the first load.
func TestCache(t *testing.T) {
	src := minimal + "\n// TestCache\n"
	before := cached()
	mustLoad(t, src)
	mustLoad(t, src)
	if n := cached() - before; n != 1 {
		t.Errorf("%d programs cached, want 1", n)
	}
}

func TestLoadSizeBoundary(t *testing.T) {
	src := minimal + "//" + strings.Repeat("x", MaxSize-len(minimal)-2)
	mustLoad(t, src)
	if _, err := Load(src + "x"); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("one byte over the limit: %v", err)
	}
}

func TestLoadRejectsInvalidCallbacks(t *testing.T) {
	for name, src := range map[string]string{
		"reduce signature":    strings.Replace(minimal, "values func(yield func([]byte) bool)", "values [][]byte", 1),
		"nil map":             strings.Replace(minimal, "func Map(", "var Map func(api.Context, []byte, []byte, api.Emitter) error\nfunc unused(", 1),
		"nil reduce":          strings.Replace(minimal, "func Reduce(", "var Reduce func(api.Context, []byte, func(func([]byte) bool), api.Emitter) error\nfunc unused(", 1),
		"nil combine":         minimal + "\nvar Combine func(api.Context, []byte, func(func([]byte) bool), api.Emitter) error\n",
		"partition signature": minimal + "\nfunc Partition(key []byte, r uint32) int { return 0 }\n",
		"nil partition":       minimal + "\nvar Partition func([]byte, uint32) uint32\n",
		"method is not map":   strings.Replace(minimal, "func Map(", "type mapper struct{}\nfunc (mapper) Map(", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(src); err == nil || !strings.Contains(err.Error(), "job file") {
				t.Fatalf("invalid callback accepted: %v", err)
			}
		})
	}
}

type emitterFunc func(key, value []byte) error

func (f emitterFunc) Emit(key, value []byte) error { return f(key, value) }

// The adapter must preserve streaming, including early termination and
// errors returned by the compiled emitter.
func TestReducerAdapter(t *testing.T) {
	src := strings.Replace(minimal, "return out.Emit(key, nil)", `
	var first []byte
	values(func(v []byte) bool { first = v; return false })
	return out.Emit(key, first)`, 1) + "\nvar Combine = Reduce\n"
	p := mustLoad(t, src)
	for name, reduce := range map[string]api.Reducer{"reduce": p.Reducer, "combine": p.Combiner} {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/empty=%v", name, empty), func(t *testing.T) {
				yielded, emitted := 0, 0
				wantErr := errors.New("output is full")
				values := func(yield func([]byte) bool) {
					if !empty {
						for _, v := range []string{"first", "second"} {
							yielded++
							if !yield([]byte(v)) {
								return
							}
						}
					}
				}
				err := reduce.Reduce(nil, []byte("key"), values, emitterFunc(func(k, v []byte) error {
					emitted++
					want := "first"
					if empty {
						want = ""
					}
					if string(k) != "key" || string(v) != want {
						t.Errorf("emitted (%q, %q), want (key, %q)", k, v, want)
					}
					return wantErr
				}))
				wantYielded := 1
				if empty {
					wantYielded = 0
				}
				if !errors.Is(err, wantErr) || yielded != wantYielded || emitted != 1 {
					t.Fatalf("error=%v, yielded=%d, emitted=%d", err, yielded, emitted)
				}
			})
		}
	}
}

func TestMapperAdapter(t *testing.T) {
	p := mustLoad(t, minimal)
	wantErr := errors.New("cannot emit")
	calls := 0
	err := p.Mapper.Map(nil, []byte("offset"), []byte("line"), emitterFunc(func(k, v []byte) error {
		calls++
		if string(k) != "line" || string(v) != "offset" {
			t.Errorf("emitted (%q, %q), want (line, offset)", k, v)
		}
		return wantErr
	}))
	if calls != 1 || !errors.Is(err, wantErr) {
		t.Fatalf("calls=%d, error=%v", calls, err)
	}
}

// Reduce panics from interpreted code must reach the existing bad-record
// handling, as Map panics do, instead of crashing the worker process.
func TestReducePanic(t *testing.T) {
	p := mustLoad(t, strings.Replace(minimal, "return out.Emit(key, nil)", `panic("reduce boom")`, 1))
	spec := api.JobSpec{
		Program: p, Inputs: []string{filepath.Join(corpus, "raven.txt")}, Output: t.TempDir(), R: 1,
	}
	if _, err := local.Run(spec, t.TempDir()); err == nil || !strings.Contains(err.Error(), "reduce boom") {
		t.Fatalf("got %v, want the Reduce panic's message", err)
	}
}

// Filling the cache must evict the least recently used source, while a
// cache hit protects that entry and does not flush other programs.
func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	mu.Lock()
	oldCache, oldLRU := cache, lru
	cache, lru = map[[sha256.Size]byte]*list.Element{}, list.New()
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		cache, lru = oldCache, oldLRU
		mu.Unlock()
	})
	sources := make([]string, maxCached+1)
	for i := range sources {
		sources[i] = fmt.Sprintf("%s\n// cache source %d\n", minimal, i)
	}
	for _, src := range sources[:maxCached] {
		mustLoad(t, src)
	}
	mustLoad(t, sources[0]) // refresh the oldest source
	mustLoad(t, sources[maxCached])
	if got := cached(); got != maxCached {
		t.Fatalf("cache size=%d, want %d", got, maxCached)
	}
	mu.Lock()
	var kept []int
	for i, src := range sources {
		if _, ok := cache[sha256.Sum256([]byte(src))]; ok {
			kept = append(kept, i)
		}
	}
	mu.Unlock()
	want := append([]int{0}, make([]int, maxCached-1)...)
	for i := 1; i < len(want); i++ {
		want[i] = i + 1
	}
	if !slices.Equal(kept, want) {
		t.Fatalf("cached sources=%v, want %v", kept, want)
	}
}
