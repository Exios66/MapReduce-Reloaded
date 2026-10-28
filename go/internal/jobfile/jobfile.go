// Package jobfile loads user job files (ADR 0015): one Go source file,
// package job, with Map and Reduce and optionally Combine and Partition,
// interpreted by Yaegi so workers run new code without a rebuild.
//
// A job file imports "mapreduce/api" for api.Context, api.Emitter and the task
// kinds (api.TaskKind, api.MapTask, api.ReduceTask), the
// same interfaces compiled programs use, the compiled helpers api.Words and
// api.Sum, and the standard library; nothing else. Reduce and Combine take values as a plain
// func(yield func([]byte) bool), since Yaegi cannot import iter; the loader
// passes the framework's iter.Seq[[]byte], which has the same underlying type.
package jobfile

import (
	"container/list"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"iter"
	"reflect"
	"sync"
	"time"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// MaxSize is the largest job file accepted, in bytes.
const MaxSize = 1 << 20

// maxCached bounds the programs kept, so a long-lived worker running many
// different job files does not grow without limit; the least recently used
// is dropped first.
const maxCached = 16

// loadTimeout bounds interpreting a job file, which runs its package-level
// initializers; a var or init that never returns must not hang the
// coordinator's SubmitJob. Its goroutine is abandoned, not stopped.
var loadTimeout = 10 * time.Second

// The signatures a job file's functions must have.
type (
	mapFunc       = func(ctx api.Context, key, value []byte, out api.Emitter) error
	reduceFunc    = func(ctx api.Context, key []byte, values func(yield func([]byte) bool), out api.Emitter) error
	partitionFunc = func(key []byte, r uint32) uint32
)

// symbols is what "mapreduce/api" offers a job file.
var symbols = interp.Exports{
	"mapreduce/api/api": {
		"Context":    reflect.ValueOf((*api.Context)(nil)),
		"Emitter":    reflect.ValueOf((*api.Emitter)(nil)),
		"TaskID":     reflect.ValueOf((*api.TaskID)(nil)),
		"TaskKind":   reflect.ValueOf((*api.TaskKind)(nil)),
		"MapTask":    reflect.ValueOf(api.MapTask),
		"ReduceTask": reflect.ValueOf(api.ReduceTask),
		"Sum":        reflect.ValueOf(api.Sum),
		"Words":      reflect.ValueOf(api.Words),
	},
}

var (
	mu      sync.Mutex
	cache   = map[[sha256.Size]byte]*list.Element{} // of entry, in lru
	lru     = list.New()                            // most recently used first
	loading = map[[sha256.Size]byte]*loadCall{}     // loads in progress
)

type entry struct {
	sum [sha256.Size]byte
	p   api.Program
}

// loadCall is one interpretation of a source, shared by every caller that
// asks for it while it runs.
type loadCall struct {
	done chan struct{} // closed when p and err are set
	p    api.Program
	err  error
}

// Load interprets src and returns its program, reusing a cached program
// while it remains among the 16 most recently used sources. Each source is
// interpreted, and its package initializers run, once at a time: callers
// that ask for a source while it loads wait for that load. The lock is not
// held while interpreting, so one slow file does not block others.
// It returns errors for source over MaxSize bytes, invalid job code, or
// loading longer than loadTimeout. A timeout does not stop initialization,
// and later callers of that source wait on the same load.
func Load(src string) (api.Program, error) {
	if len(src) > MaxSize {
		return api.Program{}, fmt.Errorf("job file is %d bytes; the limit is 1 MiB", len(src))
	}
	sum := sha256.Sum256([]byte(src))
	mu.Lock()
	if e, ok := cache[sum]; ok {
		lru.MoveToFront(e)
		mu.Unlock()
		return e.Value.(*entry).p, nil
	}
	c, ok := loading[sum]
	if !ok {
		c = &loadCall{done: make(chan struct{})}
		loading[sum] = c
		go c.run(sum, src)
	}
	mu.Unlock()

	select {
	case <-c.done:
		return c.p, c.err
	case <-time.After(loadTimeout):
		return api.Program{}, fmt.Errorf("job file: loading it took over %v; does a var or init never return?", loadTimeout)
	}
}

// run interprets src and caches the program if it loaded.
func (c *loadCall) run(sum [sha256.Size]byte, src string) {
	p, err := load(src)
	mu.Lock()
	c.p, c.err = p, err
	delete(loading, sum)
	if err == nil {
		cache[sum] = lru.PushFront(&entry{sum, p})
		if lru.Len() > maxCached {
			delete(cache, lru.Remove(lru.Back()).(*entry).sum)
		}
	}
	mu.Unlock()
	close(c.done)
}

// cached reports how many programs are cached, for tests.
func cached() int {
	mu.Lock()
	defer mu.Unlock()
	return len(cache)
}

// load interprets package job, running its initializers, and requires Map
// and Reduce with the supported signatures. Combine and Partition are optional.
// Parse, symbol setup, evaluation, and signature errors are returned;
// interpreter panics during setup or evaluation are converted to errors.
func load(src string) (p api.Program, err error) {
	f, err := parser.ParseFile(token.NewFileSet(), "job.go", src, parser.SkipObjectResolution)
	if err != nil {
		return api.Program{}, fmt.Errorf("job file: %w", err)
	}
	if f.Name.Name != "job" {
		return api.Program{}, fmt.Errorf("job file: it is package %s; it must be package job", f.Name.Name)
	}
	declared := topLevelNames(f)
	for _, name := range []string{"Map", "Reduce"} {
		if !declared[name] {
			return api.Program{}, fmt.Errorf("job file: it has no %s", name)
		}
	}

	// The interpreter panics on some malformed programs instead of
	// returning an error.
	defer func() {
		if r := recover(); r != nil {
			p, err = api.Program{}, fmt.Errorf("job file: %v", r)
		}
	}()
	i := interp.New(interp.Options{})
	if err := errors.Join(i.Use(stdlib.Symbols), i.Use(symbols)); err != nil {
		return api.Program{}, err
	}
	if _, err := i.Eval(src); err != nil {
		return api.Program{}, fmt.Errorf("job file: %w", err)
	}

	m, err := lookup[mapFunc](i, "Map")
	if err != nil {
		return api.Program{}, err
	}
	r, err := lookup[reduceFunc](i, "Reduce")
	if err != nil {
		return api.Program{}, err
	}
	p = api.Program{Mapper: api.MapFunc(m), Reducer: reducer(r)}
	if declared["Combine"] {
		c, err := lookup[reduceFunc](i, "Combine")
		if err != nil {
			return api.Program{}, err
		}
		p.Combiner = reducer(c)
	}
	if declared["Partition"] {
		part, err := lookup[partitionFunc](i, "Partition")
		if err != nil {
			return api.Program{}, err
		}
		p.Partitioner = api.PartitionFunc(part)
	}
	return p, nil
}

// lookup returns the job's top-level name as a non-nil function of type F,
// returning an error if evaluation fails or the value has the wrong type.
func lookup[F any](i *interp.Interpreter, name string) (F, error) {
	var zero F
	v, err := i.Eval("job." + name)
	if err != nil {
		return zero, fmt.Errorf("job file: %s: %w", name, err)
	}
	if v.IsValid() && v.CanInterface() {
		if f, ok := v.Interface().(F); ok && v.Kind() == reflect.Func && !v.IsNil() {
			return f, nil
		}
	}
	return zero, fmt.Errorf("job file: %s has type %v; want %v", name, v.Type(), reflect.TypeFor[F]())
}

// reducer adapts a job file's Reduce or Combine to api.Reducer.
func reducer(f reduceFunc) api.Reducer {
	return api.ReduceFunc(func(ctx api.Context, key []byte, values iter.Seq[[]byte], out api.Emitter) error {
		return f(ctx, key, values, out)
	})
}

// topLevelNames returns the file's top-level function, variable and constant
// names.
func topLevelNames(f *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				if v, ok := s.(*ast.ValueSpec); ok {
					for _, n := range v.Names {
						names[n.Name] = true
					}
				}
			}
		}
	}
	return names
}
