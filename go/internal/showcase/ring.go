package showcase

import (
	"sort"
	"sync"
)

// DefaultRingSize holds one hour of frames at the sampler's 250 ms interval.
const DefaultRingSize = 14400

// DefaultRingBytes caps what a ring's frames may cost, as frameBytes counts
// them. It keeps the showcase within its 128 MB memory limit, whatever the
// size of the status answers.
const DefaultRingBytes = 64 << 20

// Ring keeps the newest frames in memory, oldest first. It drops the oldest
// when it holds its frame count, and also when its frames cost more than its
// byte limit (see LimitBytes). Frames must be added in increasing Seq order.
// It is safe for concurrent use: the sampler adds while HTTP handlers read.
type Ring struct {
	mu    sync.RWMutex
	buf   []Frame
	cost  []int64 // cost[i] is frameBytes of buf[i]
	start int     // index in buf of the oldest frame
	n     int     // number of frames held
	bytes int64   // the sum of cost over the frames held
	limit int64   // the most the frames held may cost
}

// NewRing returns a ring that holds the newest n frames, and at most
// DefaultRingBytes of them. An n below 1 is treated as 1.
func NewRing(n int) *Ring {
	if n < 1 {
		n = 1
	}
	return &Ring{buf: make([]Frame, n), cost: make([]int64, n), limit: DefaultRingBytes}
}

// LimitBytes sets the most the frames held may cost, as frameBytes counts
// them, and evicts the oldest frames at once to meet it. The newest frame is
// always kept, even when it alone costs more than n.
func (r *Ring) LimitBytes(n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limit = n
	r.trim()
}

// Add appends f, evicting the oldest frames when the ring is full or over
// its byte limit. The frame's slices are shared with the caller, and with
// every reader of the ring, so neither may change them after Add; readers
// must not edit a frame's nested data (Status, Workers, Bytes).
func (r *Ring) Add(f Frame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := frameBytes(f)
	if r.n < len(r.buf) {
		i := (r.start + r.n) % len(r.buf)
		r.buf[i], r.cost[i] = f, c
		r.n++
	} else {
		r.bytes -= r.cost[r.start]
		r.buf[r.start], r.cost[r.start] = f, c
		r.start = (r.start + 1) % len(r.buf)
	}
	r.bytes += c
	r.trim()
}

// trim evicts the oldest frames while the frames cost more than the limit,
// and always keeps the newest. The caller holds the lock.
func (r *Ring) trim() {
	for r.n > 1 && r.bytes > r.limit {
		r.bytes -= r.cost[r.start]
		r.buf[r.start] = Frame{}
		r.cost[r.start] = 0
		r.start = (r.start + 1) % len(r.buf)
		r.n--
	}
}

// frameBytes is what a frame costs the ring: its raw status, and for each
// worker its address and host, plus 64 bytes for its counters and flags.
func frameBytes(f Frame) int64 {
	n := int64(len(f.Status))
	for _, w := range f.Workers {
		n += int64(len(w.Address) + len(w.Host) + 64)
	}
	return n
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
