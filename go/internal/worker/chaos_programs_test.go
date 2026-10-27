package worker_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/all"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/internal/wire"
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

// TestChaosPrograms is TestChaos over chaosJob's jobs, with three more
// faults: a map output file lost from a worker's disk, so that the map task
// must run again; reports and heartbeats that arrive twice; and a record
// torn at the end of the coordinator's log when it restarts. Every run must
// give local mode's output, byte for byte, and its counters. Part of #155.
func TestChaosPrograms(t *testing.T) {
	for seed := *chaosSeed; seed < *chaosSeed+uint64(*chaosRuns); seed++ {
		t.Run(fmt.Sprint("seed-", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, 1))
			c := newCluster(t, func(cfg *coordinator.Config) { cfg.Backups = true })
			spec := chaosJob(t, rng, c.output())
			t.Logf("%s, combiner %v, codec %s, format %s", spec.GetProgram(),
				spec.GetUseCombiner(), spec.GetIntermediateCodec(), spec.GetOutputFormat())

			ref := proto.Clone(spec).(*pb.JobSpec)
			ref.Output = filepath.Join(t.TempDir(), "out")
			api, err := wire.JobSpec(ref)
			if err != nil {
				t.Fatal(err)
			}
			want, err := local.Run(api, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}

			sub, err := c.client.SubmitJob(c.ctx, &pb.SubmitJobRequest{Job: spec})
			if err != nil {
				t.Fatal(err)
			}
			job := sub.GetJobId()
			kills := map[int]func(){}
			add := func() { kills[len(c.live)] = c.addWorker() }
			for range 3 {
				add()
			}
			for range 6 {
				time.Sleep(time.Duration(rng.IntN(50)) * time.Millisecond)
				live := pick(rng, kills)
				switch rng.IntN(5) {
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
				case 3: // a disk loses a map output file
					if files := c.mapFiles(job); len(files) > 0 {
						slices.Sort(files)
						_ = os.Remove(files[rng.IntN(len(files))])
					}
				case 4: // a worker's reports and heartbeats arrive twice, for a while
					c.gates[live].dup.Store(true)
					time.Sleep(time.Duration(20+rng.IntN(80)) * time.Millisecond)
					c.gates[live].dup.Store(false)
				}
			}
			status := c.wait(job)
			got, _ := filepath.Glob(filepath.Join(c.output(), "*"))
			assertSameFiles(t, got, ref.GetOutput())
			if got := status.GetCounters().GetValues(); !maps.Equal(got, want) {
				t.Fatalf("counters %v, want %v", got, want)
			}
		})
	}
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
