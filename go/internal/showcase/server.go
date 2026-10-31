package showcase

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// The modes an Info can report.
const (
	ModeLive   = "live"   // the sampler is polling the cluster
	ModeReplay = "replay" // a recording is served with no cluster
)

// Source is what the HTTP handler serves: the frames after a Seq, as Ring.After
// returns them, and what the service is doing.
type Source interface {
	After(seq uint64, limit int) ([]Frame, bool)
	Info() Info
}

// Info says what the service is doing. RunID is new each time the service
// starts; a replay uses its recording's file name. The page drops its history
// when RunID changes, because Seq numbers start again with each run.
type Info struct {
	Mode     string
	RunID    string
	Recorder RecorderState
	Skipped  int // lines of a replayed recording that were skipped
}

// MarshalJSON writes i with the keys the page reads. RecorderState has no
// tags of its own, so it is copied into recorderJSON.
func (i Info) MarshalJSON() ([]byte, error) {
	return json.Marshal(infoJSON{
		Mode:     i.Mode,
		RunID:    i.RunID,
		Recorder: recorderJSON(i.Recorder),
		Skipped:  i.Skipped,
	})
}

// infoJSON is the form of an Info that the page reads.
type infoJSON struct {
	Mode     string       `json:"mode"`
	RunID    string       `json:"run_id"`
	Recorder recorderJSON `json:"recorder"`
	Skipped  int          `json:"skipped"`
}

// recorderJSON is the form of a RecorderState that the page reads.
type recorderJSON struct {
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Stopped bool   `json:"stopped"`
	Reason  string `json:"reason"`
}

const (
	defaultLimit = 500
	maxLimit     = 2000
)

// page is the one page the service serves. It loads nothing external.
//
//go:embed page.html
var page []byte

// framesReply is the body of GET /frames.
type framesReply struct {
	Frames []Frame `json:"frames"`
	Resync bool    `json:"resync"`
	Info   Info    `json:"info"`
}

// Handler serves the page at / and the frames at /frames. It has no other
// routes, and no method but GET: anything else is 405, so the service cannot
// be written to.
func Handler(src Source) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /frames", func(w http.ResponseWriter, r *http.Request) {
		after, limit, err := framesQuery(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		frames, resync := src.After(after, limit)
		if frames == nil {
			frames = []Frame{} // the page reads an empty list, not null
		}
		body, err := json.Marshal(framesReply{Frames: frames, Resync: resync, Info: src.Info()})
		if err != nil {
			http.Error(w, "frames: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
	return mux
}

// framesQuery reads after (default 0) and limit (default 500, at most 2000).
// A value that is not a number, or a limit below 1, is an error.
func framesQuery(q url.Values) (after uint64, limit int, err error) {
	limit = defaultLimit
	if q.Has("after") {
		n, err := strconv.ParseUint(q.Get("after"), 10, 64)
		if err != nil {
			return 0, 0, errors.New("after must be a frame sequence number")
		}
		after = n
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if errors.Is(err, strconv.ErrRange) && n > 0 {
			err = nil // too large for an int: it is a large limit, capped below
		}
		if err != nil || n < 1 {
			return 0, 0, errors.New("limit must be a number of at least 1")
		}
		limit = min(n, maxLimit)
	}
	return after, limit, nil
}
