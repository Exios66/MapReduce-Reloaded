// Package wire converts between the RPC messages of proto/mapreduce/v1 and
// the in-process types the coordinator and workers use.
//
// File locations travel as URIs. A file:// URI becomes a local path; a URI
// with a scheme that internal/storage stores, such as s3://, stays a URI.
package wire

import (
	"fmt"
	"net/url"
	"path/filepath"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// JobSpec returns the job m describes, with its program looked up in the
// registry by name. It validates the result.
func JobSpec(m *pb.JobSpec) (api.JobSpec, error) {
	p, ok := api.Lookup(m.GetProgram())
	if !ok {
		return api.JobSpec{}, fmt.Errorf("wire: unknown program %q", m.GetProgram())
	}
	switch name := m.GetPartitioner(); name {
	case "": // the program's own, if it has one
	case "hash":
		p.Partitioner = nil
	default:
		return api.JobSpec{}, fmt.Errorf("wire: unknown partitioner %q", name)
	}
	if !m.GetUseCombiner() {
		p.Combiner = nil
	}
	inputs := make([]string, len(m.GetInputs()))
	for i, uri := range m.GetInputs() {
		var err error
		if inputs[i], err = Path(uri); err != nil {
			return api.JobSpec{}, err
		}
	}
	output, err := Path(m.GetOutput())
	if err != nil {
		return api.JobSpec{}, err
	}
	spec := api.JobSpec{
		Program:      p,
		Inputs:       inputs,
		Output:       output,
		SplitSize:    m.GetSplitSizeBytes(),
		R:            m.GetNumReduceTasks(),
		InputFormat:  m.GetInputFormat(),
		OutputFormat: m.GetOutputFormat(),
		Params:       m.GetParams(),
	}
	return spec, spec.Validate()
}

// InputSplit returns the wire form of s, naming its file by absolute URI.
func InputSplit(s formats.Split) (*pb.InputSplit, error) {
	uri, err := URI(s.Path)
	if err != nil {
		return nil, err
	}
	return &pb.InputSplit{Uri: uri, Offset: uint64(s.Start), Length: uint64(s.End - s.Start)}, nil
}

// Split returns the split m describes.
func Split(m *pb.InputSplit) (formats.Split, error) {
	path, err := Path(m.GetUri())
	if err != nil {
		return formats.Split{}, err
	}
	start := int64(m.GetOffset())
	return formats.Split{Path: path, Start: start, End: start + int64(m.GetLength())}, nil
}

// TaskID returns the attempt m names, in the form user code sees.
func TaskID(m *pb.TaskAttemptId) (api.TaskID, error) {
	id := api.TaskID{Index: m.GetTask().GetIndex(), Attempt: m.GetAttempt()}
	switch m.GetTask().GetKind() {
	case pb.TaskKind_TASK_KIND_MAP:
		id.Kind = api.MapTask
	case pb.TaskKind_TASK_KIND_REDUCE:
		id.Kind = api.ReduceTask
	default:
		return api.TaskID{}, fmt.Errorf("wire: task kind %v", m.GetTask().GetKind())
	}
	return id, nil
}

// URI returns the file:// URI of path, made absolute. A name that is
// already a URI is returned as it is.
func URI(path string) (string, error) {
	if storage.Scheme(path) != "" {
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String(), nil
}

// Path returns the local path of a file:// URI, which must name no host but
// localhost. A string with no scheme is already a path, and a URI that
// internal/storage stores is returned as it is.
func Path(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("wire: %w", err)
	}
	switch u.Scheme {
	case "":
		return uri, nil
	case "file":
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf("wire: %q names a remote host", uri)
		}
		return filepath.FromSlash(u.Path), nil
	}
	if _, err := storage.For(uri); err != nil || storage.Scheme(uri) != u.Scheme {
		return "", fmt.Errorf("wire: unsupported URI %q", uri)
	}
	return uri, nil
}
