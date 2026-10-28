package worker_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/all"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage/s3test"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
	"github.com/exios66/mapreduce-reloaded/go/internal/wire"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
)

// chaosJob returns a job chosen by rng: word count over the corpus or an
// example program over its inputs in testdata/examples, with or without
// its combiner, with any intermediate codec, and in either output format.
func chaosJob(t *testing.T, rng *rand.Rand, output string) *pb.JobSpec {
	t.Helper()
	data, err := os.ReadFile("../../../testdata/examples/jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	type job struct {
		Inputs []string
		Params map[string]string
	}
	var jobs map[string]job
	if err := json.Unmarshal(data, &jobs); err != nil {
		t.Fatal(err)
	}
	for name, j := range jobs {
		for i, in := range j.Inputs {
			j.Inputs[i] = filepath.Join("../../..", in)
		}
		jobs[name] = j
	}
	jobs["wordcount"] = job{Inputs: inputs}
	names := slices.Sorted(maps.Keys(jobs))
	name := names[rng.IntN(len(names))]
	return &pb.JobSpec{
		Program: name, Inputs: jobs[name].Inputs, Output: output, Params: jobs[name].Params,
		NumMapTasks: 7, NumReduceTasks: 4, UseCombiner: rng.IntN(2) == 0,
		IntermediateCodec: []string{"none", "zstd", "snappy"}[rng.IntN(3)],
		OutputFormat:      []string{"text", "kv"}[rng.IntN(2)],
	}
}

// TestChaosPrograms is TestChaos over chaosJob's jobs, writing the output
// to the local file system or to S3, under more faults. Each seed injects
// every kind of fault once, in an order and at times it chooses: a worker
// killed and replaced; a worker frozen; the coordinator restarted, perhaps
// after a record torn at the end of its log; a map output file lost from a
// worker's disk or corrupted on it, so the map task must run again; reports and heartbeats
// that arrive twice; shuffle fetches that break halfway; the coordinator's
// clock set back an hour; every worker's disk full for a while; and, with
// S3, a commit's copy or delete failing past the client's retries. The
// test fails if no fetch broke or an S3 failure never happened. Every third seed runs word count with a mapper that crashes on
// each file's first line, so that skip lists must survive the restarts;
// every other seed writes to S3. Every run must
// give local mode's output, byte for byte, and its counters (#155).
func TestChaosPrograms(t *testing.T) {
	defer worker.SetDiskPoll(5 * time.Millisecond)()
	for seed := *chaosSeed; seed < *chaosSeed+uint64(*chaosRuns); seed++ {
		t.Run(fmt.Sprint("seed-", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, 1))
			var back atomic.Int64 // how far the coordinator's clock is set back
			c := newCluster(t, func(cfg *coordinator.Config) {
				cfg.Backups = true
				// Round(0) drops the monotonic reading, as a clock read after
				// it was set back has none to compare with.
				cfg.Now = func() time.Time { return time.Now().Add(-time.Duration(back.Load())).Round(0) }
			})
			var full atomic.Bool
			c.setup = func(w *worker.Worker) {
				w.MinFreeDisk = 1 << 30
				worker.SetFreeBytes(w, func(string) (uint64, error) {
					if full.Load() {
						return 1 << 20, nil
					}
					return 2 << 30, nil
				})
			}
			var s3 *s3test.Server
			output := c.output()
			if seed%2 == 1 { // so that the default seeds, 1 to 3, try both
				s3 = s3test.StartServer(t)
				output = "s3://" + s3test.Bucket + "/out"
			}
			spec := chaosJob(t, rng, output)
			program := spec.GetProgram()
			if seed%3 == 2 { // seed 2 among the default ones
				spec.Program, spec.Inputs, spec.Params = "test.bad-record", inputs, nil
				program = "wordcount" // the reference
			}
			t.Logf("%s, combiner %v, codec %s, format %s, output %s", spec.GetProgram(),
				spec.GetUseCombiner(), spec.GetIntermediateCodec(), spec.GetOutputFormat(), output)

			ref := proto.Clone(spec).(*pb.JobSpec)
			ref.Program, ref.Output = program, filepath.Join(t.TempDir(), "out")
			api, err := wire.JobSpec(ref)
			if err != nil {
				t.Fatal(err)
			}
			want, err := local.Run(api, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if spec.GetProgram() == "test.bad-record" {
				// One line skipped in each of the corpus's three files: each
				// is empty, so only this counter sees them.
				want[task.SkippedRecords] = 3
			}

			sub, err := c.client.SubmitJob(c.ctx, &pb.SubmitJobRequest{Job: spec})
			if err != nil {
				t.Fatal(err)
			}
			job := sub.GetJobId()
			kills := map[int]func(){}
			add := func() {
				kills[len(c.live)] = c.addWorker()
				// The first fetch or two each worker serves breaks halfway,
				// so that some fetch breaks however the faults fall.
				c.gates[len(c.gates)-1].cut.Store(int32(1 + rng.IntN(2)))
			}
			for range 3 {
				add()
			}
			// Armed now, so that the run reaches it: with S3, a commit's copy
			// or delete fails past the client's 3 tries.
			if s3 != nil {
				s3.Fail([]string{"CopyObject", "DeleteObject"}[rng.IntN(2)], 3*(1+rng.IntN(2)), 500, "InternalError")
			}
			for _, fault := range rng.Perm(8) {
				time.Sleep(time.Duration(rng.IntN(50)) * time.Millisecond)
				live := pick(rng, kills)
				switch fault {
				case 0: // a machine dies, and another joins
					kills[live]()
					delete(kills, live)
					add()
				case 1: // a machine stalls, perhaps long enough to be declared dead
					c.gates[live].freeze()
					time.Sleep(time.Duration(50+rng.IntN(200)) * time.Millisecond)
					c.gates[live].thaw()
				case 2: // the coordinator restarts, perhaps after a torn append
					c.stop()
					if rng.IntN(2) == 0 {
						tearLog(t, filepath.Join(c.workDir, "coordinator.log"))
					}
					time.Sleep(time.Duration(rng.IntN(50)) * time.Millisecond)
					c.start()
				case 3: // a disk loses or corrupts a map output file
					if files := c.mapFiles(job); len(files) > 0 {
						slices.Sort(files)
						name := files[rng.IntN(len(files))]
						if rng.IntN(2) == 0 {
							_ = os.Remove(name)
						} else {
							corrupt(name, rng)
						}
					}
				case 4: // a worker's reports and heartbeats arrive twice, for a while
					c.gates[live].dup.Store(true)
					time.Sleep(time.Duration(20+rng.IntN(80)) * time.Millisecond)
					c.gates[live].dup.Store(false)
				case 5: // a worker's next fetch breaks halfway
					c.gates[live].cut.Add(1)
				case 6: // the coordinator's clock is set back
					back.Add(int64(time.Hour))
				case 7: // every disk fills up, for a while
					full.Store(true)
					time.Sleep(time.Duration(20+rng.IntN(80)) * time.Millisecond)
					full.Store(false)
				}
			}
			status := c.wait(job)
			cuts := int32(0)
			for _, g := range c.gates {
				cuts += g.cuts.Load()
			}
			if cuts == 0 {
				t.Error("no fetch was cut")
			}
			if s3 != nil && s3.Pending() > 0 {
				t.Errorf("%d injected S3 failures did not happen", s3.Pending())
			}
			assertSameOutput(t, output, ref.GetOutput())
			if got := status.GetCounters().GetValues(); !maps.Equal(got, want) {
				t.Fatalf("counters %v, want %v", got, want)
			}
		})
	}
}

// assertSameOutput checks that the part files in the directory got, on any
// backend, are those in the local directory want, byte for byte.
func assertSameOutput(t *testing.T, got, want string) {
	t.Helper()
	read := func(dir string) map[string]string {
		names, err := storage.List(t.Context(), storage.Join(dir, "part-*"))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string]string{}
		for _, name := range names {
			r, err := storage.Open(t.Context(), name, 0)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			if err = errors.Join(err, r.Close()); err != nil {
				t.Fatal(err)
			}
			files[path.Base(name)] = string(data)
		}
		return files
	}
	g, w := read(got), read(want)
	if len(w) == 0 || !maps.Equal(g, w) {
		t.Fatalf("output %v differs from local mode's %v", slices.Sorted(maps.Keys(g)), slices.Sorted(maps.Keys(w)))
	}
}

// corrupt flips a byte of the file name, if it has any, in place, as a bad
// disk sector would: its checksums must catch it.
func corrupt(name string, rng *rand.Rand) {
	data, err := os.ReadFile(name)
	if err != nil || len(data) == 0 {
		return
	}
	data[rng.IntN(len(data))] ^= 0xff
	_ = os.WriteFile(name, data, 0o644)
}

// tearLog appends the start of a record, as a crash during an append
// leaves it: a header that promises 64 bytes, and 2 of them.
func tearLog(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{64, 0, 0, 0, 1, 2, 3, 4, 'x', 'y'}); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
