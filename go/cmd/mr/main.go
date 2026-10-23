// Command mr runs MapReduce jobs.
//
//	mr run -local [-m M] [-r R] <program> <output-dir> <inputs>
//
// program is a registered program name, such as wordcount; inputs are one or
// more files or glob patterns. Counters are printed to standard error. Only
// local mode, which runs every task in this process (§4.7), exists so far.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"slices"
	"strings"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const usage = "usage: mr run -local [-m M] [-r R] <program> <output-dir> <inputs>"

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "mr:", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) (err error) {
	if len(args) == 0 || args[0] != "run" {
		return errors.New(usage)
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	isLocal := fs.Bool("local", false, "run every task in this process")
	m := fs.Uint("m", 0, "approximate number of map tasks (default: one per 64 MiB of input)")
	r := fs.Uint("r", 1, "number of reduce tasks")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch {
	case !*isLocal:
		return errors.New("only -local is supported so far")
	case fs.NArg() < 3:
		return errors.New(usage)
	case *r > math.MaxUint32:
		return fmt.Errorf("-r %d is too large", *r)
	}

	name, inputs := fs.Arg(0), fs.Args()[2:]
	program, ok := api.Lookup(name)
	if !ok {
		return fmt.Errorf("unknown program %q; registered: %s", name, strings.Join(api.Programs(), ", "))
	}
	spec := api.JobSpec{Program: program, Inputs: inputs, Output: fs.Arg(1), R: uint32(*r)}
	if *m > 0 {
		if spec.SplitSize, err = local.SplitSize(inputs, int(*m)); err != nil {
			return err
		}
	}

	work, err := os.MkdirTemp("", "mr-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(work)) }()
	counts, err := local.Run(spec, work)
	if err != nil {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		if _, err := fmt.Fprintf(stderr, "%s\t%d\n", name, counts[name]); err != nil {
			return err
		}
	}
	return nil
}
