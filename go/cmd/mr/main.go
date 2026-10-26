// Command mr runs MapReduce jobs, in this process or on a cluster.
//
//	mr run -local [job flags] <program> <output-dir> <inputs>
//	mr submit [-coordinator addr] [-max-skipped N] [-wait] [job flags] <program> <output-dir> <inputs>
//	mr status [-coordinator addr] <job-id>
//
// The job flags are -m M, -r R, -param name=value (repeatable),
// -input-format, -output-format, -intermediate-codec and -output-codec.
//
// program is a registered program name, such as wordcount; inputs are one or
// more files or glob patterns. Local mode (§4.7) runs every task in this
// process and prints the counters to standard error. It skips no bad
// records (§3.7): the first record that makes user code fail fails the job,
// so -max-skipped is submit's alone. Each -param is passed to the program's
// code, which reads it with Context.Param.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/all"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/internal/wire"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const usage = `usage:
  mr run -local [job flags] <program> <output-dir> <inputs>
  mr submit [-coordinator addr] [-max-skipped N] [-wait] [job flags] <program> <output-dir> <inputs>
  mr status [-coordinator addr] <job-id>
job flags:
  -m M  -r R  -param name=value (repeatable)
  -input-format text|kv  -output-format text|kv
  -intermediate-codec none|zstd|snappy  -output-codec none|zstd|snappy
  -combiner=false (run without the program's combiner)`

// pollInterval is how often submit -wait checks the job's status.
const pollInterval = 500 * time.Millisecond

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mr:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "run":
		return runLocal(args[1:], stderr)
	case "submit":
		return submit(args[1:], stdout, stderr)
	case "status":
		return jobStatus(args[1:], stdout, stderr)
	}
	return errors.New(usage)
}

// job is a job as given on the command line.
type job struct {
	program, output string
	inputs          []string
	splitSize       uint64
	r               uint32
	params          params
	// Codec and format names, checked when the job is validated.
	inputFormat, intermediateCodec, outputFormat, outputCodec string
	combiner                                                  bool
}

// jobFlags are the flags that describe a job.
type jobFlags struct {
	m, r                                                      *uint
	params                                                    params
	inputFormat, intermediateCodec, outputFormat, outputCodec *string
	combiner                                                  *bool
}

// addJobFlags defines the job flags on fs; defaultR is the default of -r.
func addJobFlags(fs *flag.FlagSet, defaultR uint) jobFlags {
	f := jobFlags{
		m:      fs.Uint("m", 0, "approximate number of map tasks (default: one per 64 MiB of input)"),
		r:      fs.Uint("r", defaultR, "number of reduce tasks"),
		params: params{},

		inputFormat:       fs.String("input-format", "text", "input format: text or kv"),
		intermediateCodec: fs.String("intermediate-codec", "none", "compression for map output: none, zstd or snappy"),
		outputFormat:      fs.String("output-format", "text", "output format: text or kv"),
		outputCodec:       fs.String("output-codec", "none", "compression for kv output: none, zstd or snappy"),
		combiner:          fs.Bool("combiner", true, "run the program's combiner on map output, if it has one (§4.3)"),
	}
	fs.Var(f.params, "param", "a `name=value` parameter for the program; may be repeated")
	return f
}

// params is a repeatable name=value flag.
type params map[string]string

func (p params) String() string { return fmt.Sprint(map[string]string(p)) }

func (p params) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	if !ok || name == "" {
		return fmt.Errorf("%q is not name=value", v)
	}
	p[name] = value
	return nil
}

// job returns the job described by the flags and arguments of a parsed fs.
func (f jobFlags) job(fs *flag.FlagSet) (job, error) {
	if fs.NArg() < 3 {
		return job{}, errors.New(usage)
	}
	if *f.r > math.MaxUint32 {
		return job{}, fmt.Errorf("-r %d is too large", *f.r)
	}
	if *f.m > math.MaxInt {
		return job{}, fmt.Errorf("-m %d is too large", *f.m)
	}
	j := job{
		program: fs.Arg(0), output: fs.Arg(1), inputs: fs.Args()[2:], r: uint32(*f.r), params: f.params,
		inputFormat: *f.inputFormat, intermediateCodec: *f.intermediateCodec, outputFormat: *f.outputFormat,
		outputCodec: *f.outputCodec, combiner: *f.combiner,
	}
	if *f.m > 0 {
		var err error
		if j.splitSize, err = split.SizeFor(context.Background(), j.inputs, int(*f.m)); err != nil {
			return job{}, err
		}
	}
	return j, nil
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func runLocal(args []string, stderr io.Writer) (err error) {
	fs := newFlagSet("run", stderr)
	isLocal := fs.Bool("local", false, "run every task in this process")
	flags := addJobFlags(fs, 1)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*isLocal {
		return errors.New("mr run needs -local; use mr submit to run on a cluster")
	}
	j, err := flags.job(fs)
	if err != nil {
		return err
	}
	program, ok := api.Lookup(j.program)
	if !ok {
		return fmt.Errorf("unknown program %q; registered: %s", j.program, strings.Join(api.Programs(), ", "))
	}
	if !j.combiner {
		program.Combiner = nil
	}

	work, err := os.MkdirTemp("", "mr-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(work)) }()
	counts, err := local.Run(api.JobSpec{
		Program: program, Inputs: j.inputs, Output: j.output, SplitSize: j.splitSize, R: j.r, Params: j.params,
		InputFormat: j.inputFormat, OutputFormat: j.outputFormat,
		IntermediateCodec: j.intermediateCodec, OutputCodec: j.outputCodec,
	}, work)
	if err != nil {
		return err
	}
	return printCounters(stderr, counts)
}

func submit(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("submit", stderr)
	addr := fs.String("coordinator", "127.0.0.1:18770", "coordinator address")
	wait := fs.Bool("wait", false, "wait for the job to finish, then print its status")
	maxSkipped := fs.Int64("max-skipped", -1, "fail the job if it would skip more bad records than this (negative: no limit)")
	flags := addJobFlags(fs, 0) // 0: the coordinator picks R from its worker count
	if err := fs.Parse(args); err != nil {
		return err
	}
	j, err := flags.job(fs)
	if err != nil {
		return err
	}
	// Send absolute URIs, so the coordinator and workers resolve them the
	// same way whatever their working directory.
	spec := &pb.JobSpec{
		Program: j.program, SplitSizeBytes: j.splitSize, NumReduceTasks: j.r, UseCombiner: j.combiner, Params: j.params,
		InputFormat: j.inputFormat, OutputFormat: j.outputFormat,
		IntermediateCodec: j.intermediateCodec, OutputCodec: j.outputCodec,
	}
	if *maxSkipped >= 0 {
		spec.MaxSkippedRecords = proto.Uint64(uint64(*maxSkipped))
	}
	for _, in := range j.inputs {
		uri, err := wire.URI(in)
		if err != nil {
			return err
		}
		spec.Inputs = append(spec.Inputs, uri)
	}
	if spec.Output, err = wire.URI(j.output); err != nil {
		return err
	}

	client, closeConn, err := dial(*addr)
	if err != nil {
		return err
	}
	defer closeConn()
	ctx := context.Background()
	resp, err := client.SubmitJob(ctx, &pb.SubmitJobRequest{Job: spec}, grpc.WaitForReady(true))
	if err != nil {
		return err
	}
	if !*wait {
		_, err := fmt.Fprintln(stdout, resp.GetJobId())
		return err
	}
	for {
		st, err := client.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: resp.GetJobId()})
		if err != nil {
			return err
		}
		switch st.GetState() {
		case pb.JobState_JOB_STATE_SUCCEEDED:
			return printStatus(stdout, st)
		case pb.JobState_JOB_STATE_FAILED:
			return errors.Join(printStatus(stdout, st), fmt.Errorf("job %s failed: %s", st.GetJobId(), st.GetError()))
		}
		time.Sleep(pollInterval)
	}
}

func jobStatus(args []string, stdout, stderr io.Writer) error {
	fs := newFlagSet("status", stderr)
	addr := fs.String("coordinator", "127.0.0.1:18770", "coordinator address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New(usage)
	}
	client, closeConn, err := dial(*addr)
	if err != nil {
		return err
	}
	defer closeConn()
	st, err := client.GetJobStatus(context.Background(), &pb.GetJobStatusRequest{JobId: fs.Arg(0)})
	if err != nil {
		return err
	}
	return printStatus(stdout, st)
}

func dial(addr string) (pb.CoordinatorServiceClient, func(), error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return pb.NewCoordinatorServiceClient(conn), func() { _ = conn.Close() }, nil
}

// printStatus prints the job's state and progress, its backup attempts
// started and won (§3.6), the map tasks that ran on their split's host or in
// its zone (§3.4), its duration in seconds once it has finished, as
// the coordinator measured it, then its counters.
// The duration is omitted unless both timestamps are present. It returns
// any write errors, still attempting to print counters after an earlier error.
func printStatus(w io.Writer, st *pb.GetJobStatusResponse) error {
	_, err := fmt.Fprintf(w, "%s\t%s\nmap\t%d/%d\nreduce\t%d/%d\nbackups\t%d\nbackups_won\t%d\nmap_host_local\t%d\nmap_zone_local\t%d\n",
		st.GetJobId(), strings.TrimPrefix(st.GetState().String(), "JOB_STATE_"),
		st.GetMapTasksCompleted(), st.GetMapTasksTotal(),
		st.GetReduceTasksCompleted(), st.GetReduceTasksTotal(),
		st.GetBackupAttempts(), st.GetBackupWins(), st.GetMapTasksHostLocal(), st.GetMapTasksZoneLocal())
	if err == nil && st.GetSubmittedAt() != nil && st.GetFinishedAt() != nil {
		d := max(0, st.GetFinishedAt().AsTime().Sub(st.GetSubmittedAt().AsTime())) // 0 if the clock was set back
		_, err = fmt.Fprintf(w, "seconds\t%.3f\n", d.Seconds())
	}
	return errors.Join(err, printCounters(w, st.GetCounters().GetValues()))
}

func printCounters(w io.Writer, counts map[string]int64) error {
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		if _, err := fmt.Fprintf(w, "%s\t%d\n", name, counts[name]); err != nil {
			return err
		}
	}
	return nil
}
