package showcase_test

import (
	"encoding/json"
	"strings"
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

// sizedFrame is a frame that costs size bytes in the ring: its status.
func sizedFrame(seq uint64, size int) showcase.Frame {
	f := seqFrame(seq)
	f.Status = json.RawMessage(`"` + strings.Repeat("x", size-2) + `"`)
	return f
}

func TestRingEvictsTheOldestPastItsByteLimit(t *testing.T) {
	r := showcase.NewRing(100)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Add(sizedFrame(seq, 300))
	}
	r.LimitBytes(1000) // three frames of 300 bytes
	frames, _ := r.After(0, 100)
	if got, want := seqs(frames), []uint64{3, 4, 5}; !sameSeqs(got, want) {
		t.Errorf("after LimitBytes(1000) seqs = %v, want %v", got, want)
	}
	r.Add(sizedFrame(6, 300))
	frames, _ = r.After(0, 100)
	if got, want := seqs(frames), []uint64{4, 5, 6}; !sameSeqs(got, want) {
		t.Errorf("after an Add over the limit seqs = %v, want %v", got, want)
	}
}

func TestRingLimitBytesEvictsAtOnce(t *testing.T) {
	r := showcase.NewRing(100)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Add(sizedFrame(seq, 300))
	}
	r.LimitBytes(450) // one frame of 300 fits; two would be 600
	frames, _ := r.After(0, 100)
	if got, want := seqs(frames), []uint64{5}; !sameSeqs(got, want) {
		t.Errorf("after LimitBytes(450) seqs = %v, want %v", got, want)
	}
}

func TestRingKeepsItsNewestFrameOverTheLimit(t *testing.T) {
	r := showcase.NewRing(10)
	r.LimitBytes(10)
	r.Add(sizedFrame(1, 300))
	r.Add(sizedFrame(2, 300))
	frames, _ := r.After(0, 100)
	if got, want := seqs(frames), []uint64{2}; !sameSeqs(got, want) {
		t.Errorf("seqs = %v, want only the newest %v", got, want)
	}
}

func TestRingCostsWorkersByAddressAndHost(t *testing.T) {
	// One worker costs len(Address) + len(Host) + 64: 13 + 2 + 64 = 79.
	worker := func(seq uint64) showcase.Frame {
		return showcase.Frame{Seq: seq, Workers: []showcase.WorkerSample{{Address: "10.0.0.1:9000", Host: "w1", OK: true}}}
	}
	r := showcase.NewRing(10)
	for seq := uint64(1); seq <= 3; seq++ {
		r.Add(worker(seq))
	}
	r.LimitBytes(158) // two frames of 79
	frames, _ := r.After(0, 100)
	if got, want := seqs(frames), []uint64{2, 3}; !sameSeqs(got, want) {
		t.Errorf("after LimitBytes(158) seqs = %v, want %v", got, want)
	}
	r.LimitBytes(157) // one frame of 79 fits; two would be 158
	frames, _ = r.After(0, 100)
	if got, want := seqs(frames), []uint64{3}; !sameSeqs(got, want) {
		t.Errorf("after LimitBytes(157) seqs = %v, want %v", got, want)
	}
}

// Byte eviction is an eviction like any other: a client that last saw a
// frame that is gone must resync, and one that saw the frame before the
// oldest kept one must not.
func TestRingAfterResyncsWhenBytesEvictedTheClientsFrame(t *testing.T) {
	r := showcase.NewRing(100)
	for seq := uint64(1); seq <= 5; seq++ {
		r.Add(sizedFrame(seq, 300))
	}
	r.LimitBytes(1000) // keeps 3, 4 and 5
	frames, resync := r.After(1, 100)
	if !resync {
		t.Errorf("After(1) resync = false, want true: frame 2 was evicted")
	}
	if got, want := seqs(frames), []uint64{3, 4, 5}; !sameSeqs(got, want) {
		t.Errorf("After(1) seqs = %v, want %v", got, want)
	}
	if _, resync := r.After(2, 100); resync {
		t.Errorf("After(2) resync = true, want false: nothing after 2 was evicted")
	}
}

func TestRingDefaultsToDefaultRingBytes(t *testing.T) {
	if showcase.DefaultRingBytes != 64<<20 {
		t.Fatalf("DefaultRingBytes = %d, want 64 MiB", showcase.DefaultRingBytes)
	}
	// Sixteen frames of 4 MiB fill the default limit exactly; the seventeenth
	// evicts the first. One shared status keeps the test's own memory small.
	big := json.RawMessage(strings.Repeat("x", 4<<20))
	r := showcase.NewRing(showcase.DefaultRingSize)
	for seq := uint64(1); seq <= 17; seq++ {
		f := seqFrame(seq)
		f.Status = big
		r.Add(f)
	}
	frames, _ := r.After(0, 100)
	if got := seqs(frames); len(got) != 16 || got[0] != 2 || got[15] != 17 {
		t.Errorf("default ring kept seqs %v, want 16 frames from 2 to 17", got)
	}
}
