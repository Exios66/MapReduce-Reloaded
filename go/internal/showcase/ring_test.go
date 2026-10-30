package showcase_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/exios66/mapreduce-reloaded/go/internal/showcase"
)

// seqFrame is a frame with the given sequence number and a valid status.
func seqFrame(seq uint64) showcase.Frame {
	return showcase.Frame{
		Seq:      seq,
		At:       time.Unix(1_700_000_000, 0).Add(time.Duration(seq) * 250 * time.Millisecond),
		Status:   json.RawMessage(`{"ok":true}`),
		StatusOK: true,
	}
}

// seqs lists the sequence numbers of frames, in order.
func seqs(frames []showcase.Frame) []uint64 {
	out := make([]uint64, len(frames))
	for i, f := range frames {
		out[i] = f.Seq
	}
	return out
}

func sameSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRingAfterReturnsOnlyNewerFrames(t *testing.T) {
	r := showcase.NewRing(10)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Add(seqFrame(seq))
	}
	frames, resync := r.After(2, 100)
	if resync {
		t.Errorf("After(2) resync = true, want false")
	}
	if got, want := seqs(frames), []uint64{3, 4, 5}; !sameSeqs(got, want) {
		t.Errorf("After(2) seqs = %v, want %v", got, want)
	}
	frames, _ = r.After(5, 100)
	if len(frames) != 0 {
		t.Errorf("After(newest) returned %d frames, want none", len(frames))
	}
}

func TestRingAfterLimitsTheCount(t *testing.T) {
	r := showcase.NewRing(10)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Add(seqFrame(seq))
	}
	frames, _ := r.After(0, 2)
	if got, want := seqs(frames), []uint64{1, 2}; !sameSeqs(got, want) {
		t.Errorf("After(0, 2) seqs = %v, want %v", got, want)
	}
	frames, _ = r.After(1, 2)
	if got, want := seqs(frames), []uint64{2, 3}; !sameSeqs(got, want) {
		t.Errorf("After(1, 2) seqs = %v, want %v", got, want)
	}
}

func TestRingAfterZeroStartsAtTheOldestFrameKept(t *testing.T) {
	r := showcase.NewRing(3)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Add(seqFrame(seq))
	}
	frames, resync := r.After(0, 100)
	if resync {
		t.Errorf("After(0) resync = true, want false: seq 0 is not a resync")
	}
	if got, want := seqs(frames), []uint64{3, 4, 5}; !sameSeqs(got, want) {
		t.Errorf("After(0) seqs = %v, want %v", got, want)
	}
}

func TestRingReportsResyncWhenTheFrameIsGone(t *testing.T) {
	r := showcase.NewRing(3)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Add(seqFrame(seq))
	}
	// The client last saw frame 1; frames 2 is gone, so it must resync.
	frames, resync := r.After(1, 100)
	if !resync {
		t.Errorf("After(1) resync = false, want true: frame 2 was evicted")
	}
	if got, want := seqs(frames), []uint64{3, 4, 5}; !sameSeqs(got, want) {
		t.Errorf("After(1) seqs = %v, want the oldest frames kept %v", got, want)
	}
	// The client last saw frame 2, the frame just before the oldest kept one.
	_, resync = r.After(2, 100)
	if resync {
		t.Errorf("After(2) resync = true, want false: nothing after 2 was evicted")
	}
}

func TestRingAfterOnAnEmptyRing(t *testing.T) {
	r := showcase.NewRing(3)
	frames, resync := r.After(0, 10)
	if len(frames) != 0 || resync {
		t.Errorf("After(0) on an empty ring = %v, resync %v; want no frames and no resync", seqs(frames), resync)
	}
	// A client that has seen frames, against a ring with none, is from an
	// earlier run of the sampler: it must resync.
	frames, resync = r.After(7, 10)
	if len(frames) != 0 || !resync {
		t.Errorf("After(7) on an empty ring = %v, resync %v; want no frames and resync true", seqs(frames), resync)
	}
}

func TestRingAfterAheadOfTheNewestResyncs(t *testing.T) {
	// The sampler restarted: the client's seq 9 is above every frame kept.
	r := showcase.NewRing(10)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Add(seqFrame(seq))
	}
	frames, resync := r.After(9, 100)
	if !resync {
		t.Errorf("After(9) resync = false, want true: seq is ahead of the ring")
	}
	if got, want := seqs(frames), []uint64{1, 2, 3, 4, 5}; !sameSeqs(got, want) {
		t.Errorf("After(9) seqs = %v, want the oldest frames kept %v", got, want)
	}
	frames, resync = r.After(9, 2)
	if !resync || !sameSeqs(seqs(frames), []uint64{1, 2}) {
		t.Errorf("After(9, 2) = %v, resync %v; want [1 2] and resync true", seqs(frames), resync)
	}
}

func TestRingAfterTheNewestFrameIsNotAResync(t *testing.T) {
	r := showcase.NewRing(10)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Add(seqFrame(seq))
	}
	frames, resync := r.After(5, 100)
	if resync || len(frames) != 0 {
		t.Errorf("After(newest) = %v, resync %v; want no frames and no resync", seqs(frames), resync)
	}
}

func TestRingIsSafeForConcurrentUse(t *testing.T) {
	r := showcase.NewRing(50)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for seq := uint64(1); seq <= 2000; seq++ {
			r.Add(seqFrame(seq))
		}
	}()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				frames, _ := r.After(uint64(j), 20)
				for k := 1; k < len(frames); k++ {
					if frames[k].Seq <= frames[k-1].Seq {
						t.Errorf("After returned frames out of order: %d after %d", frames[k].Seq, frames[k-1].Seq)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
