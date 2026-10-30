package showcase

import (
	"sort"
	"sync"
)

// DefaultRingSize holds one hour of frames at the sampler's 250 ms interval.
const DefaultRingSize = 14400

// Ring keeps the newest frames in memory, oldest first, and drops the oldest
// when it is full. Frames must be added in increasing Seq order. It is safe
// for concurrent use: the sampler adds while HTTP handlers read.
type Ring struct {
	mu    sync.RWMutex
	buf   []Frame
	start int // index in buf of the oldest frame
	n     int // number of frames held
}

// NewRing returns a ring that holds the newest n frames. An n below 1 is
// treated as 1.
func NewRing(n int) *Ring {
	if n < 1 {
		n = 1
	}
	return &Ring{buf: make([]Frame, n)}
}

// Add appends f, evicting the oldest frame when the ring is full. The frame's
// slices are shared with the caller, and with every reader of the ring, so
// neither may change them after Add; readers must not edit a frame's nested
// data (Status, Workers, Bytes).
func (r *Ring) Add(f Frame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n < len(r.buf) {
		r.buf[(r.start+r.n)%len(r.buf)] = f
		r.n++
		return
	}
	r.buf[r.start] = f
	r.start = (r.start + 1) % len(r.buf)
}

// at returns the i-th oldest frame. The caller holds the lock.
func (r *Ring) at(i int) Frame {
	return r.buf[(r.start+i)%len(r.buf)]
}

// After returns up to limit frames newer than seq, oldest first. A client
// passes the Seq of the last frame it saw; seq 0 means from the oldest frame
// kept. resync is true when seq is above 0 and either frames after it have
// been evicted, or seq is newer than every frame kept (the client is from an
// earlier run of the sampler). In both cases the client has a gap or a
// splice, so it must start over from the frames returned.
func (r *Ring) After(seq uint64, limit int) (frames []Frame, resync bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.n == 0 {
		return nil, seq > 0
	}
	oldest := r.at(0).Seq
	newest := r.at(r.n - 1).Seq
	i := 0
	switch {
	case seq == 0:
		// from the oldest frame kept; not a resync
	case seq > newest:
		resync = true // ahead of the ring: start over from the oldest kept
	default:
		resync = oldest > seq && oldest-seq > 1
		i = sort.Search(r.n, func(i int) bool { return r.at(i).Seq > seq })
	}
	for ; i < r.n && len(frames) < limit; i++ {
		frames = append(frames, r.at(i))
	}
	return frames, resync
}
