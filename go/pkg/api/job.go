package api

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
)

// Program is a user's MapReduce code: what runs, as opposed to the data it
// runs on.
type Program struct {
	Mapper  Mapper
	Reducer Reducer
	// Combiner is optional; nil means no combining.
	Combiner Combiner
	// Partitioner is optional; nil means HashPartitioner.
	Partitioner Partitioner
}

// JobSpec describes one job: a Program and the data to run it on. It is the
// in-process form of the wire message mapreduce.v1.JobSpec, which refers to
// the program by name.
type JobSpec struct {
	Program Program
	// Inputs are input files or glob patterns, as URIs.
	Inputs []string
	// Output is the output directory. Reduce task r writes part-r-<r>, with r
	// in at least five digits.
	Output string
	// SplitSize is the target size of an input split in bytes, which
	// determines M, the number of map tasks (§3.1). 0 means 64 MiB.
	SplitSize uint64
	// R is the number of reduce tasks and output files.
	R uint32
	// InputFormat and OutputFormat name the record formats (§4.4). Empty
	// means "text".
	InputFormat, OutputFormat string
	// IntermediateCodec compresses the map output and the sort's spill
	// files, and OutputCodec the output files: "none", "zstd" or "snappy".
	// Empty means "none". Only the "kv" output format takes a codec.
	IntermediateCodec, OutputCodec string
	// Params are passed to user code through Context.Param.
	Params map[string]string
}

// Validate reports every problem with the spec, or nil if it can run.
func (s *JobSpec) Validate() error {
	var errs []error
	if isNil(s.Program.Mapper) {
		errs = append(errs, errors.New("no Mapper"))
	}
	if isNil(s.Program.Reducer) {
		errs = append(errs, errors.New("no Reducer"))
	}
	if s.Program.Combiner != nil && isNil(s.Program.Combiner) {
		errs = append(errs, errors.New("the Combiner is a nil function"))
	}
	if s.Program.Partitioner != nil && isNil(s.Program.Partitioner) {
		errs = append(errs, errors.New("the Partitioner is a nil function"))
	}
	if len(s.Inputs) == 0 {
		errs = append(errs, errors.New("no Inputs"))
	}
	if s.Output == "" {
		errs = append(errs, errors.New("no Output"))
	}
	if s.R == 0 {
		errs = append(errs, errors.New("number of reduce tasks R must be at least 1"))
	}
	if _, err := formats.Input(s.InputFormat); err != nil {
		errs = append(errs, err)
	}
	if _, err := kv.ParseCodec(s.IntermediateCodec); err != nil {
		errs = append(errs, fmt.Errorf("intermediate codec: %w", err))
	}
	if c, err := kv.ParseCodec(s.OutputCodec); err != nil {
		errs = append(errs, fmt.Errorf("output codec: %w", err))
	} else if _, err := formats.Output(s.OutputFormat, c); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// isNil reports whether v is nil, or a nil function wrapped in an interface,
// such as MapFunc(nil), which would panic when called.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	f := reflect.ValueOf(v)
	return f.Kind() == reflect.Func && f.IsNil()
}
