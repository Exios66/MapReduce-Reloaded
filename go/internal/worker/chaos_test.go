package worker_test

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
)

var (
	chaosRuns = flag.Int("chaos.runs", 3, "number of TestChaos runs")
	chaosSeed = flag.Uint64("chaos.seed", 1, "seed of the first TestChaos run")
)

// TestChaos runs word count while, at random times, workers die and are
// replaced, workers freeze, and the coordinator restarts. Every run must
// give local mode's output and counters. A failed run names its seed, and
// -chaos.seed=N -chaos.runs=1 makes the same choices again, though their
// timing against the job may differ.
func TestChaos(t *testing.T) {
	_, want := runLocal(t)
	for seed := *chaosSeed; seed < *chaosSeed+uint64(*chaosRuns); seed++ {
		t.Run(fmt.Sprint("seed-", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, 0))
			c := newCluster(t, func(cfg *coordinator.Config) { cfg.Backups = true })
			job := c.submit("wordcount")
			kills := map[int]func(){}
			add := func() { kills[len(c.live)] = c.addWorker() }
			for range 3 {
				add()
			}
			for range 6 {
				time.Sleep(time.Duration(rng.IntN(50)) * time.Millisecond)
				live := pick(rng, kills)
				switch rng.IntN(3) {
				case 0: // a machine dies, and another joins
					kills[live]()
					delete(kills, live)
					add()
				case 1: // a machine stalls, perhaps long enough to be declared dead
					c.gates[live].freeze()
					time.Sleep(time.Duration(50+rng.IntN(200)) * time.Millisecond)
					c.gates[live].thaw()
				case 2:
					c.stop()
					time.Sleep(time.Duration(rng.IntN(50)) * time.Millisecond)
					c.start()
				}
			}
			status := c.wait(job)
			assertGolden(t, c.output())
			if got := status.GetCounters().GetValues(); !maps.Equal(got, want) {
				t.Fatalf("counters %v, want %v", got, want)
			}
		})
	}
}

// pick returns one of the keys of m, in a way that depends only on rng.
func pick(rng *rand.Rand, m map[int]func()) int {
	keys := slices.Sorted(maps.Keys(m))
	return keys[rng.IntN(len(keys))]
}

// gate holds up a worker's RPCs while it is frozen, as if the worker's
// process had been stopped: its heartbeats, task requests and reports, and
// the fetches it serves. Calls already under way carry on.
// While fail is positive, each RequestTask fails with Internal, as from a
// coordinator handler that panicked, and decrements it.
type gate struct {
	mu   sync.RWMutex
	fail atomic.Int32
}

func (g *gate) freeze() { g.mu.Lock() }
func (g *gate) thaw()   { g.mu.Unlock() }

func (g *gate) pass() {
	g.mu.RLock()
	defer g.mu.RUnlock()
}

func (g *gate) unary(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	g.pass()
	if strings.HasSuffix(method, "/RequestTask") && g.fail.Add(-1) >= 0 {
		return status.Error(codes.Internal, "panic in a handler")
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

func (g *gate) stream(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	g.pass()
	return handler(srv, ss)
}
