package jobfile

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
		want, err := filepath.Glob(filepath.Join(corpus, "expected", "*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(want) == 0 {
			t.Fatal("no golden files found")
		}
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

// Callers loading the same new source at once share one load, so its
// initializers run once.
func TestConcurrentLoadsRunInitOnce(t *testing.T) {
	log := filepath.Join(t.TempDir(), "inits")
	src := strings.Replace(minimal, `import "mapreduce/api"`, `import (
	"mapreduce/api"
	"os"
	"time"
)

var _ = initOnce()

func initOnce() int {
	f, _ := os.OpenFile(`+strconv.Quote(log)+`, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	f.WriteString("x")
	f.Close()
	time.Sleep(100 * time.Millisecond)
	return 0
}`, 1)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { mustLoad(t, src) })
	}
	wg.Wait()
	if b, err := os.ReadFile(log); err != nil || string(b) != "x" {
		t.Fatalf("initializer ran %q times (%v), want once", b, err)
	}
}
