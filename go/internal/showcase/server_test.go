package showcase_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/exios66/mapreduce-reloaded/go/internal/showcase"
)

// ringSource serves the frames of a ring, with a fixed Info.
type ringSource struct {
	*showcase.Ring
	info showcase.Info
}

func (s ringSource) Info() showcase.Info { return s.info }

// frameAt is a frame with a status, one worker and the given Seq.
func frameAt(seq uint64) showcase.Frame {
	return showcase.Frame{
		Seq:      seq,
		At:       time.Unix(int64(seq), 0).UTC(),
		Status:   json.RawMessage(`{"jobs":[]}`),
		StatusOK: true,
		Workers:  []showcase.WorkerSample{{Address: "w1:8081", Host: "abc", OK: true}},
	}
}

// ringOf returns a ring of the given size holding frames with the given Seqs.
func ringOf(size int, seqs ...uint64) *showcase.Ring {
	r := showcase.NewRing(size)
	for _, seq := range seqs {
		r.Add(frameAt(seq))
	}
	return r
}

// serveSource starts Handler(src) on a test server.
func serveSource(t *testing.T, src showcase.Source) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(showcase.Handler(src))
	t.Cleanup(srv.Close)
	return srv
}

// doRequest sends one request and returns the response and its body.
func doRequest(t *testing.T, method, url string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

// framesReply is the body of GET /frames.
type framesReply struct {
	Frames []showcase.Frame `json:"frames"`
	Resync bool             `json:"resync"`
	Info   struct {
		Mode     string `json:"mode"`
		RunID    string `json:"run_id"`
		Recorder struct {
			Path    string `json:"path"`
			Bytes   int64  `json:"bytes"`
			Stopped bool   `json:"stopped"`
			Reason  string `json:"reason"`
		} `json:"recorder"`
		Skipped int `json:"skipped"`
	} `json:"info"`
}

// framesOf requests /frames with the given query and decodes the reply.
func framesOf(t *testing.T, srv *httptest.Server, query string) framesReply {
	t.Helper()
	resp, body := doRequest(t, http.MethodGet, srv.URL+"/frames?"+query)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /frames?%s = %d: %s", query, resp.StatusCode, body)
	}
	var reply framesReply
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return reply
}

// seqsOf lists the Seqs of frames.
func seqsOf(frames []showcase.Frame) []uint64 {
	out := make([]uint64, len(frames))
	for i, f := range frames {
		out[i] = f.Seq
	}
	return out
}

func TestFramesAfterSequence(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: ringOf(10, 1, 2, 3, 4, 5)})
	reply := framesOf(t, srv, "after=2&limit=2")
	if got := seqsOf(reply.Frames); fmt.Sprint(got) != "[3 4]" {
		t.Errorf("frames after 2 = %v, want [3 4]", got)
	}
	if reply.Resync {
		t.Error("resync is true for a client that is up to date")
	}
}

func TestFramesRejectsABadAfter(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: ringOf(10, 1)})
	for _, query := range []string{"after=-1", "after=x", "after=1.5", "after="} {
		if resp, _ := doRequest(t, http.MethodGet, srv.URL+"/frames?"+query); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /frames?%s = %d, want 400", query, resp.StatusCode)
		}
	}
}

func TestFramesRejectsABadLimit(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: ringOf(10, 1)})
	for _, query := range []string{"limit=0", "limit=-3", "limit=x", "limit="} {
		if resp, _ := doRequest(t, http.MethodGet, srv.URL+"/frames?"+query); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /frames?%s = %d, want 400", query, resp.StatusCode)
		}
	}
}

func TestFramesCapsTheLimit(t *testing.T) {
	ring := showcase.NewRing(3000)
	for seq := uint64(1); seq <= 2500; seq++ {
		ring.Add(frameAt(seq))
	}
	srv := serveSource(t, ringSource{Ring: ring})
	if n := len(framesOf(t, srv, "limit=99999").Frames); n != 2000 {
		t.Errorf("limit=99999 returned %d frames, want 2000", n)
	}
	if n := len(framesOf(t, srv, "after=0").Frames); n != 500 {
		t.Errorf("default limit returned %d frames, want 500", n)
	}
}

func TestFramesWithNoFramesIsAnEmptyList(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: showcase.NewRing(10)})
	_, body := doRequest(t, http.MethodGet, srv.URL+"/frames?after=0")
	if !strings.Contains(string(body), `"frames":[]`) {
		t.Errorf("empty ring reply lacks an empty frames list: %s", body)
	}
}

func TestFramesReportsAResync(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: ringOf(3, 5, 6, 7)})
	if reply := framesOf(t, srv, "after=1"); !reply.Resync {
		t.Error("resync is false for a client behind the ring")
	}
	if reply := framesOf(t, srv, "after=0"); reply.Resync {
		t.Error("resync is true for seq 0")
	}
}

func TestFramesReplyCarriesTheInfo(t *testing.T) {
	info := showcase.Info{
		Mode:     showcase.ModeLive,
		RunID:    "run-42",
		Recorder: showcase.RecorderState{Path: "/data/showcase/a.jsonl", Bytes: 99, Stopped: true, Reason: "size cap"},
		Skipped:  3,
	}
	srv := serveSource(t, ringSource{Ring: ringOf(10, 1), info: info})
	reply := framesOf(t, srv, "after=0")
	got := reply.Info
	if got.Mode != "live" || got.RunID != "run-42" || got.Skipped != 3 {
		t.Errorf("info = %+v", got)
	}
	if got.Recorder.Path != "/data/showcase/a.jsonl" || got.Recorder.Bytes != 99 || !got.Recorder.Stopped || got.Recorder.Reason != "size cap" {
		t.Errorf("recorder = %+v", got.Recorder)
	}
}

func TestFramesReplyUsesTheDocumentedKeys(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: ringOf(10, 1)})
	_, body := doRequest(t, http.MethodGet, srv.URL+"/frames?after=0")
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatal(err)
	}
	if got := keysOf(top); got != "frames info resync" {
		t.Errorf("top-level keys = %q", got)
	}
	var info map[string]json.RawMessage
	if err := json.Unmarshal(top["info"], &info); err != nil {
		t.Fatal(err)
	}
	if got := keysOf(info); got != "mode recorder run_id skipped" {
		t.Errorf("info keys = %q", got)
	}
	var recorder map[string]json.RawMessage
	if err := json.Unmarshal(info["recorder"], &recorder); err != nil {
		t.Fatal(err)
	}
	if got := keysOf(recorder); got != "bytes path reason stopped" {
		t.Errorf("recorder keys = %q", got)
	}
}

// keysOf returns the sorted keys of m, space separated.
func keysOf(m map[string]json.RawMessage) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

func TestFramesAreNotCached(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: ringOf(10, 1)})
	resp, _ := doRequest(t, http.MethodGet, srv.URL+"/frames?after=0")
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

func TestOnlyGetIsServed(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: ringOf(10, 1)})
	for _, path := range []string{"/", "/frames?after=0"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			if resp, _ := doRequest(t, method, srv.URL+path); resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405", method, path, resp.StatusCode)
			}
		}
	}
}

func TestPageLoadsNothingExternal(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: ringOf(10, 1)})
	resp, body := doRequest(t, http.MethodGet, srv.URL+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	page := string(body)
	for _, bad := range []string{"http://", "https://", "@import", "<link", "<img", "src=", "//cdn"} {
		if strings.Contains(page, bad) {
			t.Errorf("page contains %q; it must load nothing external", bad)
		}
	}
	if n := len(regexp.MustCompile(`<script[\s>]`).FindAllString(page, -1)); n != 1 {
		t.Errorf("page has %d scripts, want exactly one inline script", n)
	}
	for _, want := range []string{`fetch("/frames?`, "run_id", "resync"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPageIsServedAtTheRootOnly(t *testing.T) {
	srv := serveSource(t, ringSource{Ring: ringOf(10, 1)})
	if resp, _ := doRequest(t, http.MethodGet, srv.URL+"/nope"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404", resp.StatusCode)
	}
}
