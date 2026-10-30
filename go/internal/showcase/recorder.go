package showcase

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// DefaultRecordingCap is the most a recording may hold: 256 MiB.
const DefaultRecordingCap = 256 << 20

// RecorderState says how far a recording has got and whether it has stopped.
type RecorderState struct {
	Path    string
	Bytes   int64
	Stopped bool
	Reason  string // why it stopped, when Stopped is true
}

// Recorder appends frames to a file, one JSON object per line. Once a write
// fails, or a frame would pass the size cap, it stops and ignores later
// frames, so recording never stops the live view. It is safe for concurrent
// use.
type Recorder struct {
	mu       sync.Mutex
	f        *os.File
	path     string
	maxBytes int64
	bytes    int64
	stopped  bool
	reason   string
}

// NewRecorder creates the recording at path, and any missing parent
// directories. It fails if the file already exists, so a recording is never
// overwritten. A maxBytes below 1 means DefaultRecordingCap.
func NewRecorder(path string, maxBytes int64) (*Recorder, error) {
	if maxBytes < 1 {
		maxBytes = DefaultRecordingCap
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("recording: create directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, fmt.Errorf("recording: %w", err)
	}
	return &Recorder{f: f, path: path, maxBytes: maxBytes}, nil
}

// Write appends f as one line. It never returns an error: a frame that cannot
// be written stops the recording, and State says why.
func (r *Recorder) Write(f Frame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	line, err := json.Marshal(f)
	if err != nil {
		r.stop(fmt.Sprintf("marshal: %v", err))
		return
	}
	line = append(line, '\n')
	if r.bytes+int64(len(line)) > r.maxBytes {
		r.stop("size cap")
		return
	}
	n, err := r.f.Write(line)
	r.bytes += int64(n)
	if err != nil {
		r.stop(fmt.Sprintf("write: %v", err))
	}
}

// stop ends the recording and releases its file. The caller holds the lock.
func (r *Recorder) stop(reason string) {
	r.stopped = true
	r.reason = reason
	_ = r.f.Close()
}

// State reports the recording's path, size and whether it has stopped.
func (r *Recorder) State() RecorderState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RecorderState{Path: r.path, Bytes: r.bytes, Stopped: r.stopped, Reason: r.reason}
}

// ReadRecording reads the frames in a recording, in file order. A blank line
// is skipped and not counted. A line that does not parse, such as one cut off
// by a crash, or that decodes to null or to a frame with Seq 0, is skipped
// and counted in skipped. A line of any length is read.
func ReadRecording(path string) (frames []Frame, skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	// ReadBytes grows its buffer as needed, so a long line is not cut short
	// the way a bufio.Scanner's default buffer would cut it.
	br := bufio.NewReader(f)
	for {
		line, readErr := br.ReadBytes('\n')
		if text := bytes.TrimSpace(line); len(text) > 0 {
			var fr Frame
			if string(text) == "null" || json.Unmarshal(text, &fr) != nil || fr.Seq == 0 {
				skipped++
			} else {
				frames = append(frames, fr)
			}
		}
		if errors.Is(readErr, io.EOF) {
			return frames, skipped, nil
		}
		if readErr != nil {
			return frames, skipped, readErr
		}
	}
}
