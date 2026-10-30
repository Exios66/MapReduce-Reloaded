// Package showcase samples a cluster's coordinator and workers into frames,
// and serves them to one read-only page. It never changes a job.
package showcase

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

// Frame is one sample of the cluster: what the coordinator's /status.json
// returned and what each worker's /metrics returned. A source that did not
// answer has OK false and no counters; it is never recorded as zero.
type Frame struct {
	Seq      uint64
	At       time.Time
	Status   json.RawMessage // the coordinator's /status.json, as it came
	StatusOK bool
	Workers  []WorkerSample
	// SampleInterval is the time since the previous round. It is longer
	// than the configured interval when a source was slow.
	SampleInterval time.Duration
}

// WorkerSample is one worker's /metrics as one frame saw it.
type WorkerSample struct {
	Address string           `json:"address"` // the address the sampler scraped
	Host    string           `json:"host"`    // from mr_worker_info, if it answered
	OK      bool             `json:"ok"`
	Bytes   map[string]int64 `json:"bytes,omitempty"`
}

// frameJSON is the recording's form of a Frame. Its sample interval is in
// seconds, not a time.Duration's nanoseconds.
type frameJSON struct {
	Seq                   uint64          `json:"seq"`
	At                    time.Time       `json:"at"`
	Status                json.RawMessage `json:"status"`
	StatusOK              bool            `json:"status_ok"`
	Workers               []WorkerSample  `json:"workers"`
	SampleIntervalSeconds float64         `json:"sample_interval_seconds"`
}

// MarshalJSON writes f in the recording's form.
func (f Frame) MarshalJSON() ([]byte, error) {
	w := frameJSON{
		Seq:                   f.Seq,
		At:                    f.At,
		Status:                f.Status,
		StatusOK:              f.StatusOK,
		Workers:               f.Workers,
		SampleIntervalSeconds: f.SampleInterval.Seconds(),
	}
	// A status that is empty or not JSON is recorded as null, so the frame
	// is kept.
	if !json.Valid(f.Status) {
		w.Status, w.StatusOK = nil, false
	}
	// The page reads frame.workers, so nil is an empty list, not null.
	if w.Workers == nil {
		w.Workers = []WorkerSample{}
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads a frame written by MarshalJSON. A null input leaves f
// unchanged, as encoding/json expects of an Unmarshaler.
func (f *Frame) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var w frameJSON
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	status := w.Status
	if string(status) == "null" {
		status = nil
	}
	*f = Frame{
		Seq:            w.Seq,
		At:             w.At,
		Status:         status,
		StatusOK:       w.StatusOK,
		Workers:        w.Workers,
		SampleInterval: time.Duration(math.Round(w.SampleIntervalSeconds * float64(time.Second))),
	}
	return nil
}

// counters are the worker byte counters the sampler keeps. Other families
// on a worker's /metrics are not read.
var counters = map[string]bool{
	"mr_worker_map_input_bytes_total":       true,
	"mr_worker_map_output_bytes_total":      true,
	"mr_worker_shuffle_fetched_bytes_total": true,
	"mr_worker_reduce_output_bytes_total":   true,
}

// ParseMetrics reads a worker's /metrics text. bytes holds the four byte
// counters it finds; a counter it does not find is absent, not zero, and
// bytes is nil when it finds none, so a body without metrics does not look
// like an answering worker. host is the mr_worker_info host label, unescaped,
// or empty if there is none.
func ParseMetrics(text string) (bytes map[string]int64, host string) {
	for _, line := range strings.Split(text, "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		end := strings.IndexAny(line, "{ ")
		if end < 0 {
			continue
		}
		name, rest := line[:end], line[end:]
		var lab map[string]string
		if rest[0] == '{' {
			var ok bool
			if lab, rest, ok = parseLabels(rest); !ok {
				continue
			}
		}
		fields := strings.Fields(rest) // the value, then an optional timestamp
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch {
		case name == "mr_worker_info":
			if h, ok := lab["host"]; ok {
				host = h
			}
		case counters[name]:
			if bytes == nil {
				bytes = make(map[string]int64)
			}
			bytes[name] = v
		}
	}
	return bytes, host
}

// parseLabels reads the {name="value",...} block at the start of s. It
// returns the labels and the text after the closing brace; ok is false when
// the block is malformed.
func parseLabels(s string) (lab map[string]string, rest string, ok bool) {
	lab = make(map[string]string)
	s = s[1:] // the opening brace
	for {
		s = strings.TrimLeft(s, " ")
		if strings.HasPrefix(s, "}") {
			return lab, s[1:], true
		}
		name, after, found := strings.Cut(s, "=")
		if !found || !strings.HasPrefix(after, `"`) {
			return nil, "", false
		}
		value, after, found := parseQuoted(after[1:])
		if !found {
			return nil, "", false
		}
		lab[name] = value
		s = strings.TrimPrefix(after, ",")
	}
}

// parseQuoted reads a label value up to its closing quote and undoes the
// escapes promtext.Label writes: \\, \" and \n. found is false if the quote
// never closes.
func parseQuoted(s string) (value, rest string, found bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			return b.String(), s[i+1:], true
		case c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case '\\', '"':
				b.WriteByte(s[i])
			case 'n':
				b.WriteByte('\n')
			default:
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", "", false
}
