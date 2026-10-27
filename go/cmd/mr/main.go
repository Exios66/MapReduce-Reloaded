// Command mr runs MapReduce jobs, in this process or on a cluster.
//
//	mr run -local [job flags] <program | job.go> <output-dir> <inputs>
//	mr submit [-coordinator addr] [-max-skipped N] [-wait] [job flags] <program | job.go> <output-dir> <inputs>
//	mr status [-coordinator addr] <job-id>
//	mr upload <local files, dirs or globs…> </data/dir>
//	mr cat </data/output-dir>
//
// The job flags are -m M, -r R, -param name=value (repeatable),
// -input-format, -output-format, -intermediate-codec, -output-codec and
// -combiner=false.
//
// program is a registered program name, such as wordcount, or a path ending
// in .go to a job file with Map and Reduce (ADR 0015), which mr reads and
// sends with the job; inputs are one or
// more files or glob patterns. Local mode (§4.7) runs every task in this
// process and prints the counters to standard error. It skips no bad
// records (§3.7): the first record that makes user code fail fails the job,
// so -max-skipped is submit's alone. Each -param is passed to the program's
// code, which reads it with Context.Param. -m is the exact number of map
// tasks (ADR 0016); submit reports the M that resulted on standard error.
//
// upload and cat reach the cluster's shared storage from the host (ADR
// 0016): a path /data/... names the same file inside $MR_DATA_DIR (default
// ./data). upload copies files into it, and a directory's contents; cat
// prints a job's part-r-* files in order.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/all"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/jobfile"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/internal/wire"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const usage = `usage:
  mr run -local [job flags] <program | job.go> <output-dir> <inputs>
  mr submit [-coordinator addr] [-max-skipped N] [-wait] [job flags] <program | job.go> <output-dir> <inputs>
  mr status [-coordinator addr] <job-id>
  mr upload <local files, dirs or globs...> </data/dir>
  mr cat </data/output-dir>
job flags:
  -m M (exact number of map tasks)  -r R  -param name=value (repeatable)
  -input-format text|kv  -output-format text|kv
  -intermediate-codec none|zstd|snappy  -output-codec none|zstd|snappy
  -combiner=false (run without the program's combiner)`

// pollInterval is how often submit -wait checks the job's status.
const pollInterval = 500 * time.Millisecond

func main() { os.Exit(exitCode(run(os.Args[1:], os.Stdout, os.Stderr), os.Stderr)) }

// exitCode returns the exit status for run's error, printing it to stderr.
// Asking for help, which has printed the usage, is not an error.
func exitCode(err error, stderr io.Writer) int {
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	_, _ = fmt.Fprintln(stderr, "mr:", err)
	return 1
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
	case "upload":
		return upload(args[1:], stdout)
	case "cat":
		return cat(args[1:], stdout)
	}
	return errors.New(usage)
}

// job is a job as given on the command line.
type job struct {
	program, output string
	inputs          []string
	m               int
	r               uint32
	params          params
	// Codec and format names, checked when the job is validated.
	inputFormat, intermediateCodec, outputFormat, outputCodec string
	combiner                                                  bool
	// source is the job file's Go source, when program names one (ADR 0015).
	source string
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
		m:      fs.Uint("m", 0, "number of map tasks, M (default: one per 64 MiB of input)"),
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
// -m is passed on as the exact number of map tasks (ADR 0016); the runner
// cuts the input. A program ending in .go is read as a job file and named
// by its basename, without validation. It returns errors for missing
// arguments, oversized task counts, or job file reads or size limits.
func (f jobFlags) job(fs *flag.FlagSet) (job, error) {
	if fs.NArg() < 3 {
		return job{}, errors.New(usage)
	}
	if *f.r > math.MaxUint32 {
		return job{}, fmt.Errorf("-r %d is too large", *f.r)
	}
	if *f.m > math.MaxUint32 {
		return job{}, fmt.Errorf("-m %d is too large", *f.m)
	}
	j := job{
		program: fs.Arg(0), output: fs.Arg(1), inputs: fs.Args()[2:], r: uint32(*f.r), params: f.params,
		inputFormat: *f.inputFormat, intermediateCodec: *f.intermediateCodec, outputFormat: *f.outputFormat,
		outputCodec: *f.outputCodec, combiner: *f.combiner,
	}
	if strings.HasSuffix(j.program, ".go") {
		src, err := readJobFile(j.program)
		if err != nil {
			return job{}, err
		}
		j.program, j.source = filepath.Base(j.program), string(src)
	}
	j.m = int(*f.m)
	return j, nil
}

// readJobFile reads a job file, refusing one over jobfile.MaxSize before
// reading it all.
// It returns open, read, close, or size-limit errors without validating Go code.
func readJobFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("job file: %w", err)
	}
	src, err := io.ReadAll(io.LimitReader(f, jobfile.MaxSize+1))
	if err = errors.Join(err, f.Close()); err != nil {
		return nil, fmt.Errorf("job file: %w", err)
	}
	if len(src) > jobfile.MaxSize {
		return nil, fmt.Errorf("job file: %s is over the 1 MiB limit", path)
	}
	return src, nil
}

// load returns the job's program: its job file, interpreted, or the
// registered program it names.
// It returns jobfile.Load errors or an error for an unknown registered name.
func (j job) load() (api.Program, error) {
	if j.source != "" {
		return jobfile.Load(j.source)
	}
	p, ok := api.Lookup(j.program)
	if !ok {
		return api.Program{}, fmt.Errorf("unknown program %q; registered: %s, or a .go job file",
			j.program, strings.Join(api.Programs(), ", "))
	}
	return p, nil
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// runLocal parses mr run arguments, requires -local, and runs a registered
// program or interpreted job file in this process. It uses the program's
// combiner, if present, unless -combiner=false is set. On success it writes
// the counters to stderr. It returns argument, job file read or load,
// program lookup, execution, and counter write errors, along with errors
// creating or removing its temporary intermediate files.
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
	program, err := j.load()
	if err != nil {
		return err
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
		Program: program, Inputs: j.inputs, Output: j.output, M: j.m, R: j.r, Params: j.params,
		InputFormat: j.inputFormat, OutputFormat: j.outputFormat,
		IntermediateCodec: j.intermediateCodec, OutputCodec: j.outputCodec,
	}, work)
	if err != nil {
		return err
	}
	return printCounters(stderr, counts)
}

// submit parses mr submit arguments and submits a job to the coordinator,
// requesting the program's combiner unless -combiner=false is set.
// A .go program path is read locally and its source sent for validation
// by the coordinator.
// Without -wait it writes the job ID to stdout; with -wait it waits for a
// terminal state and writes status and counters instead, returning an error
// if the job failed. Flag diagnostics go to stderr.
// Argument, job file read or size-limit, input inspection, URI conversion,
// connection, RPC, and output write errors are returned to the caller.
// Waiting has no timeout.
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
		Program: j.program, NumMapTasks: uint32(j.m), NumReduceTasks: j.r, UseCombiner: j.combiner, Params: j.params,
		ProgramSource: j.source,
		InputFormat:   j.inputFormat, OutputFormat: j.outputFormat,
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
	if j.m > 0 {
		st, err := client.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: resp.GetJobId()})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stderr, "mr: %d map tasks (-m %d)\n", st.GetMapTasksTotal(), j.m); err != nil {
			return err
		}
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

// sharedRoot is the path at which every container mounts shared storage.
const sharedRoot = "/data"

// sharedPath returns the host path of a shared-storage path /data/...,
// inside $MR_DATA_DIR (default ./data). It fails if the path is outside
// /data or the directory does not exist, so a run from the wrong directory
// does not write somewhere the cluster cannot see.
func sharedPath(p string) (string, error) {
	p = path.Clean(p)
	rel, ok := strings.CutPrefix(p, sharedRoot)
	if !ok || rel != "" && rel[0] != '/' {
		return "", fmt.Errorf("%s is not in shared storage; give a path under %s/", p, sharedRoot)
	}
	dir := os.Getenv("MR_DATA_DIR")
	if dir == "" {
		dir = "data"
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("shared storage %s is not a directory: run mr from the repository root after make cluster-up, or set MR_DATA_DIR", dir)
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

// upload copies local files into a shared-storage directory, which it
// creates. A directory's contents are copied into it, keeping their layout,
// and a pattern is expanded, so a quoted glob works. It prints each file it
// copied.
func upload(args []string, stdout io.Writer) error {
	if len(args) < 2 {
		return errors.New(usage)
	}
	dst, err := sharedPath(args[len(args)-1])
	if err != nil {
		return err
	}
	for _, pattern := range args[:len(args)-1] {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			return fmt.Errorf("upload: %s: no such file", pattern)
		}
		for _, src := range matches {
			if err := uploadOne(src, dst, stdout); err != nil {
				return err
			}
		}
	}
	return nil
}

// uploadOne copies src into the directory dst: a file as dst/<its name>, a
// directory's files under dst at their paths relative to src.
func uploadOne(src, dst string, stdout io.Writer) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, filepath.Join(dst, filepath.Base(src)), stdout)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		return copyFile(p, filepath.Join(dst, rel), stdout)
	})
}

// copyFile copies src to dst, creating dst's directory, through a
// temporary file so a job never reads a partial copy.
func copyFile(src, dst string, stdout io.Writer) (err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".upload-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	_, err = io.Copy(tmp, in)
	err = errors.Join(err, tmp.Chmod(0o644), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err == nil {
		_, err = fmt.Fprintln(stdout, dst)
	}
	return err
}

// cat prints the part-r-* files of a job's output directory in shared
// storage, in order.
func cat(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	dir, err := sharedPath(args[0])
	if err != nil {
		return err
	}
	parts, err := filepath.Glob(filepath.Join(dir, "part-r-*"))
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("cat: %s has no part-r-* files; has the job finished?", args[0])
	}
	slices.Sort(parts) // part-r-NNNNN, zero-padded
	for _, p := range parts {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(stdout, f)
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
	}
	return nil
}
