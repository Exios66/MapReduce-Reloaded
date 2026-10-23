package api

import "errors"

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
	// Output is the output directory. Reduce task r writes part-r-<r>.
	Output string
	// SplitSize is the target size of an input split in bytes, which
	// determines M, the number of map tasks (§3.1). 0 means 64 MiB.
	SplitSize uint64
	// R is the number of reduce tasks and output files.
	R uint32
	// InputFormat and OutputFormat name the record formats (§4.4). Empty
	// means "text".
	InputFormat, OutputFormat string
	// Params are passed to user code through Context.Param.
	Params map[string]string
}

// Validate reports every problem with the spec, or nil if it can run.
func (s *JobSpec) Validate() error {
	var errs []error
	if s.Program.Mapper == nil {
		errs = append(errs, errors.New("no Mapper"))
	}
	if s.Program.Reducer == nil {
		errs = append(errs, errors.New("no Reducer"))
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
	return errors.Join(errs...)
}
