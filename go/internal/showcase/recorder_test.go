package showcase_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/exios66/mapreduce-reloaded/go/internal/showcase"
)

// lineOf is the recording line for f, newline included.
func lineOf(t *testing.T, f showcase.Frame) int64 {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	return int64(len(b) + 1)
}

func TestRecorderStopsAtTheCapAndSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.jsonl")
	f := seqFrame(1)
	line := lineOf(t, f)
	maxBytes := 2*line + line/2 // room for two frames, not three
	r, err := showcase.NewRecorder(path, maxBytes)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	for i := 0; i < 5; i++ {
		r.Write(f)
	}
	st := r.State()
	if !st.Stopped || st.Reason != "size cap" {
		t.Errorf("State = %+v, want Stopped with reason %q", st, "size cap")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat recording: %v", err)
	}
	if info.Size() > maxBytes {
		t.Errorf("recording is %d bytes, over the cap of %d", info.Size(), maxBytes)
	}
	if st.Bytes != info.Size() || st.Bytes != 2*line {
		t.Errorf("State.Bytes = %d, file = %d, want %d", st.Bytes, info.Size(), 2*line)
	}
}

func TestRecorderStopsOnAFrameThatWillNotMarshal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.jsonl")
	r, err := showcase.NewRecorder(path, 1<<20)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	r.Write(seqFrame(1))
	bad := seqFrame(2)
	bad.At = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) // time.Time cannot marshal past year 9999
	r.Write(bad)                                          // must not panic
	st := r.State()
	if !st.Stopped || !strings.HasPrefix(st.Reason, "marshal") {
		t.Errorf("State = %+v, want Stopped with a marshal reason", st)
	}
	r.Write(seqFrame(3)) // a stopped recorder ignores later frames
	frames, skipped, err := showcase.ReadRecording(path)
	if err != nil || skipped != 0 || len(frames) != 1 {
		t.Errorf("ReadRecording = %d frames, %d skipped, err %v; want 1 frame, 0 skipped", len(frames), skipped, err)
	}
}

func TestNewRecorderRefusesAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.jsonl")
	if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := showcase.NewRecorder(path, 1<<20); err == nil {
		t.Fatal("NewRecorder over an existing file succeeded, want an error")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "keep me\n" {
		t.Errorf("existing recording changed: %q, %v", b, err)
	}
}

func TestReadRecordingRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "rec.jsonl")
	r, err := showcase.NewRecorder(path, 1<<20)
	if err != nil {
		t.Fatalf("NewRecorder (missing parents): %v", err)
	}
	want := []showcase.Frame{seqFrame(1), seqFrame(2), seqFrame(3)}
	want[0].Workers = []showcase.WorkerSample{} // a frame with no workers reads back as an empty list
	want[2].Workers = []showcase.WorkerSample{}
	want[1].Workers = []showcase.WorkerSample{{Address: "w1:9000", Host: "abc", OK: true, Bytes: map[string]int64{"mr_worker_map_input_bytes_total": 42}}}
	want[2].StatusOK = false
	want[2].SampleInterval = 1500 * time.Millisecond
	for _, f := range want {
		r.Write(f)
	}
	got, skipped, err := showcase.ReadRecording(path)
	if err != nil {
		t.Fatalf("ReadRecording: %v", err)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}
	if len(got) != len(want) {
		t.Fatalf("read %d frames, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Seq != want[i].Seq || !got[i].At.Equal(want[i].At) || got[i].StatusOK != want[i].StatusOK ||
			got[i].SampleInterval != want[i].SampleInterval || !reflect.DeepEqual(got[i].Workers, want[i].Workers) {
			t.Errorf("frame %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReadRecordingSkipsACutOffLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.jsonl")
	r, err := showcase.NewRecorder(path, 1<<20)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	r.Write(seqFrame(1))
	r.Write(seqFrame(2))
	r.Write(seqFrame(3))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	// Cut the last line in half, as a crash mid-write would.
	lastStart := strings.LastIndex(string(b[:len(b)-1]), "\n") + 1
	cut := lastStart + 10
	if err := os.WriteFile(path, b[:cut], 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	frames, skipped, err := showcase.ReadRecording(path)
	if err != nil {
		t.Fatalf("ReadRecording: %v", err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if len(frames) != 2 || frames[0].Seq != 1 || frames[1].Seq != 2 {
		t.Errorf("frames = %v, want seqs 1 and 2", seqs(frames))
	}
}

func TestReadRecordingKeepsALongLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.jsonl")
	r, err := showcase.NewRecorder(path, 1<<24)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	long := seqFrame(1)
	long.Status = json.RawMessage(`{"pad":"` + strings.Repeat("x", 200<<10) + `"}`) // well past 64 KiB
	r.Write(long)
	r.Write(seqFrame(2))
	frames, skipped, err := showcase.ReadRecording(path)
	if err != nil {
		t.Fatalf("ReadRecording: %v", err)
	}
	if skipped != 0 || len(frames) != 2 {
		t.Fatalf("read %d frames, %d skipped; want 2 and 0", len(frames), skipped)
	}
	if len(frames[0].Status) != len(long.Status) {
		t.Errorf("long status came back %d bytes, want %d", len(frames[0].Status), len(long.Status))
	}
}

// frameLine is the recording line for seq, as the recorder writes it.
func frameLine(t *testing.T, seq uint64) string {
	t.Helper()
	b, err := json.Marshal(seqFrame(seq))
	if err != nil {
		t.Fatalf("marshal frame %d: %v", seq, err)
	}
	return string(b) + "\n"
}

func TestReadRecordingSkipsBlankLinesUncounted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.jsonl")
	body := frameLine(t, 1) + "\n" + "  \t\n" + frameLine(t, 2)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	frames, skipped, err := showcase.ReadRecording(path)
	if err != nil {
		t.Fatalf("ReadRecording: %v", err)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0: blank lines are not counted", skipped)
	}
	if !sameSeqs(seqs(frames), []uint64{1, 2}) {
		t.Errorf("frames = %v, want seqs 1 and 2", seqs(frames))
	}
}

func TestReadRecordingCountsNullAndZeroSeqLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.jsonl")
	body := frameLine(t, 1) + "null\n" + `{"seq":0}` + "\n" + frameLine(t, 2)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	frames, skipped, err := showcase.ReadRecording(path)
	if err != nil {
		t.Fatalf("ReadRecording: %v", err)
	}
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2: one null line and one Seq 0 line", skipped)
	}
	if !sameSeqs(seqs(frames), []uint64{1, 2}) {
		t.Errorf("frames = %v, want seqs 1 and 2", seqs(frames))
	}
}

func TestReadRecordingMissingFileIsAnError(t *testing.T) {
	if _, _, err := showcase.ReadRecording(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("ReadRecording of a missing file succeeded, want an error")
	}
}
