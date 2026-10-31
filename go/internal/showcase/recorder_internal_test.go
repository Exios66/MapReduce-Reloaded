package showcase

import (
	"path/filepath"
	"testing"
	"time"
)

// This test sits in package showcase so it can close the recording file
// under the recorder; no black-box call makes a write fail on every OS.
func TestRecorderSurvivesAFailedWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rec.jsonl")
	r, err := NewRecorder(path, 1<<20)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	if err := r.f.Close(); err != nil {
		t.Fatalf("close under the recorder: %v", err)
	}
	f := Frame{Seq: 1, At: time.Unix(1, 0), StatusOK: true}
	r.Write(f) // must not panic
	st := r.State()
	if !st.Stopped || st.Reason == "" {
		t.Errorf("State = %+v, want Stopped with a reason after a failed write", st)
	}
}
