# Showcase Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only service that samples a running cluster, records the run, and serves one offline page for the live demo and for repeatable captures.

**Architecture:** A sampler turns polls of `/status.json` and each worker's `/metrics` into frames. Frames go to a bounded in-memory ring and a capped JSONL recording. A web server hands frames to one page, which derives everything it shows from frames, so live and replay share their code.

**Tech Stack:** Go 1.26 standard library, one embedded HTML file with inline CSS and JavaScript, Docker Compose, Python pytest for deployment checks.

**Spec:** `docs/design/0023-showcase-service.md`

## Global Constraints

- Read-only: no write endpoint, and the cluster runs the same with or without the service.
- No new Go module dependency; the page loads nothing external (no URL, `src=`, `<link` or second `<script`).
- Sample every 250 ms by default; the page polls every 500 ms with one request in flight.
- A source that does not answer is a gap, never a zero.
- Compose profile `showcase`, `mem_limit: 128m`, published only on `127.0.0.1:18781`; `cluster-up` and `e2e` do not start it.
- Recordings: a new `data/showcase/<start-time>.jsonl` per start, inside a size cap.
- Commits are authored by the owner, with no agent footers and `[skip ci]` until the final push; each visible change adds a `CHANGELOG.md` bullet.
- Dropping later phases is allowed for the Nov 16 code deadline; S1 to S4 are the order of priority.

## Review Focus

- A worker replaced during a run: same address family, new worker ID. It must appear as joined, and the old one must stay lost.
- Counters that fall when a worker restarts: rates must treat the drop as a reset, never a negative rate.
- A coordinator restart while the sampler runs: `at` jumps and jobs may vanish. The timeline must say so, not join the runs up.
- Two jobs in one run: rates are cluster-wide, so the view must not credit one job with the other's bytes.
- A tab left open for an hour: the page's client history must stay bounded, and asking for a frame older than the ring must resync, not fail.

---

## File Structure

| File | Responsibility |
|---|---|
| `go/cmd/showcase/main.go` | Flags and startup, live or replay |
| `go/internal/showcase/frame.go` | Frame types, parsing of `/status.json` and worker metrics |
| `go/internal/showcase/sampler.go` | Polling rounds, discovery, skipped rounds |
| `go/internal/showcase/ring.go` | Bounded frames by sequence number |
| `go/internal/showcase/recorder.go` | Capped JSONL writer and reader |
| `go/internal/showcase/server.go` | HTTP endpoints |
| `go/internal/showcase/page.html` | The one renderer: derive state from frames, draw |

`Frame` carries raw source data. State such as rates, ETA and timeline is derived on the page, because replay must derive it the same way. Tests of derivation run the page's script in Task 6.

### Task 1: Prove worker matching

`os.Hostname()` in a Compose worker is its container ID, while DNS discovery gives IP addresses. This task decides how a metrics target maps to a `/status.json` worker, before other code assumes it.

**Files:**
- Create: `docs/superpowers/plans/2026-10-30-showcase-worker-matching.md` (a short note of the result)

**Interfaces:**
- Produces: the decision that Task 3's `Frame.Workers[].Host` relies on.

- [ ] **Step 1:** Run `make cluster-up`, then from a container on the Compose network compare `getent hosts worker`, reverse lookups of each IP, and `curl -s coordinator:8080/status.json` worker `host` values.
- [ ] **Step 2:** Choose one: (a) match reverse-DNS names to hostnames; (b) add the worker's hostname to its `/metrics` as `mr_worker_info{host="..."} 1`; (c) keep throughput cluster-wide and show worker reachability by address only. Prefer (b) if (a) fails, because it is one line in each worker and is exact.
- [ ] **Step 3:** Write the result and evidence in the note, and update Task 3 if the choice is not (b).
- [ ] **Step 4:** Commit: `git commit -m "docs: record how the showcase matches workers [skip ci]"`.

### Task 2: Frames and parsing

**Files:**
- Create: `go/internal/showcase/frame.go`, `go/internal/showcase/frame_test.go`

**Interfaces:**
- Produces:
  - `type Frame struct { Seq uint64; At time.Time; Status json.RawMessage; StatusOK bool; Workers []WorkerSample; SampleInterval time.Duration }`, with JSON keys `seq`, `at`, `status`, `status_ok`, `workers`, `sample_interval_seconds`
  - `type WorkerSample struct { Address, Host string; OK bool; Bytes map[string]int64 }`
  - `func ParseMetrics(text string) (bytes map[string]int64, host string)`

- [ ] **Step 1: Write the failing tests**

```go
func TestParseMetricsKeepsTheFourByteCounters(t *testing.T) // map_input, map_output, shuffle_fetched, reduce_output
func TestParseMetricsIgnoresOtherFamiliesAndComments(t *testing.T) // mr_worker_pruned_bytes_total is not in the map
func TestFrameOfAnUnansweredSourceHasNoZeroes(t *testing.T) // OK false, Bytes nil, and "bytes" absent from the JSON
```

- [ ] **Step 2:** Run `cd go && go test ./internal/showcase` and expect a build failure.
- [ ] **Step 3:** Implement `frame.go`. Keep exactly `mr_worker_map_input_bytes_total`, `mr_worker_map_output_bytes_total`, `mr_worker_shuffle_fetched_bytes_total` and `mr_worker_reduce_output_bytes_total`. Keep `Status` as raw JSON so a new coordinator field needs no sampler change.
- [ ] **Step 4:** Run the tests and expect PASS.
- [ ] **Step 5:** Commit: `git commit -m "showcase: frames and metrics parsing [skip ci]"`.

### Task 3: Sampler

**Files:**
- Create: `go/internal/showcase/sampler.go`, `go/internal/showcase/sampler_test.go`
- Modify: `go/internal/statusweb/statusweb_test.go` and `python/tests/test_coordinator_status.py`, to name the keys the showcase reads

**Interfaces:**
- Consumes: `Frame`, `ParseMetrics`.
- Produces:
  - `type Config struct { StatusURL string; Discover func(context.Context) ([]string, error); Interval, Timeout time.Duration; Now func() time.Time; Client *http.Client }`
  - `func Sample(ctx context.Context, c Config, out func(Frame)) error`, which returns when `ctx` ends
  - `func DNSDiscover(name string, port int) func(context.Context) ([]string, error)`

- [ ] **Step 1: Write the failing tests**

```go
func TestSampleRecordsAStatusOutageAsAGap(t *testing.T)     // a closed server gives StatusOK false, and Seq still rises
func TestSampleRecordsAnUnansweredWorker(t *testing.T)      // one of two: OK true and OK false in one frame
func TestSampleKeepsWorkersDiscoveryLost(t *testing.T)      // a worker gone from DNS stays in the frame with OK false until it was seen answering again or 60 s pass
func TestSampleSkipsRoundsWhenSlow(t *testing.T)            // a source slower than Interval: frames are not queued, SampleInterval shows the real gap
func TestSampleReadsARealCoordinatorAndWorker(t *testing.T) // statusweb.Handler and a worker metrics handler over httptest
```

- [ ] **Step 2:** Run `cd go && go test ./internal/showcase` and expect failures.
- [ ] **Step 3:** Implement `sampler.go`. Each round polls all sources concurrently with `Timeout` (default 200 ms). A round overrunning `Interval` delays the next one, without queuing. Keep a discovered worker for 60 s after it leaves DNS, because Docker removes a killed container's name and the page must still show it as unreachable.
- [ ] **Step 4:** Run the tests and expect PASS, then run `go test -race ./internal/showcase`.
- [ ] **Step 5:** In the Go and Python status tests, add a comment that the showcase reads these keys, so a rename points to `go/internal/showcase`.
- [ ] **Step 6:** Commit: `git commit -m "showcase: sample the coordinator and workers [skip ci]"`.

### Task 4: Ring and recorder

**Files:**
- Create: `go/internal/showcase/ring.go`, `go/internal/showcase/recorder.go`, and their `_test.go` files

**Interfaces:**
- Consumes: `Frame`.
- Produces:
  - `func NewRing(n int) *Ring`, `func (r *Ring) Add(f Frame)`, `func (r *Ring) After(seq uint64, limit int) (frames []Frame, resync bool)`
  - `func NewRecorder(path string, maxBytes int64) (*Recorder, error)`, `func (r *Recorder) Write(f Frame)`, `func (r *Recorder) State() RecorderState`
  - `type RecorderState struct { Path string; Bytes int64; Stopped bool; Reason string }`
  - `func ReadRecording(path string) (frames []Frame, skipped int, err error)`

- [ ] **Step 1: Write the failing tests**

```go
func TestRingAfterReturnsOnlyNewerFrames(t *testing.T)
func TestRingReportsResyncWhenTheFrameIsGone(t *testing.T) // resync true and the oldest frames kept
func TestRecorderStopsAtTheCapAndSaysSo(t *testing.T)      // Stopped true, Reason "size cap", file size <= maxBytes
func TestRecorderSurvivesAFailedWrite(t *testing.T)        // Stopped true, Write does not panic
func TestReadRecordingRoundTrip(t *testing.T)
func TestReadRecordingSkipsACutOffLine(t *testing.T)       // skipped == 1, the other frames kept
```

- [ ] **Step 2:** Run the tests and expect failures.
- [ ] **Step 3:** Implement both files. The default ring holds 14,400 frames (one hour at 250 ms) and the default cap is 256 MiB. A frame that would pass the cap is not written. `Recorder` never returns an error from `Write`, because recording must not stop the live view.
- [ ] **Step 4:** Run the tests and expect PASS.
- [ ] **Step 5:** Commit: `git commit -m "showcase: bounded frames and capped recordings [skip ci]"`.

### Task 5: Server, command and deployment

**Files:**
- Create: `go/internal/showcase/server.go`, `go/internal/showcase/server_test.go`, `go/internal/showcase/page.html` (a stub that fetches frames), `go/cmd/showcase/main.go`, `go/cmd/showcase/main_test.go`
- Modify: `deploy/docker/Dockerfile:11`, `deploy/compose/compose.yaml`, `Makefile`, `docs/cluster-vm.md:75`, `python/tests/test_deploy.py`, `go/cover-floors.txt`

**Interfaces:**
- Consumes: `Sample`, `Ring`, `Recorder`, `ReadRecording`.
- Produces:
  - `func Handler(src Source) http.Handler`
  - `type Source interface { After(seq uint64, limit int) ([]Frame, bool); Info() Info }`
  - `type Info struct { Mode string; Recorder RecorderState; Skipped int }`, where `Mode` is `live` or `replay`
  - `GET /` the page; `GET /frames?after=<seq>&limit=<n>` returns `{"frames":[...],"resync":false,"info":{...}}`; default limit 500, maximum 2000
  - Flags: `-http :8080`, `-status http://coordinator:8080/status.json`, `-workers worker`, `-worker-port 8081`, `-interval 250ms`, `-out /data/showcase`, `-max-record-bytes 268435456`, `-replay <file>`

- [ ] **Step 1: Write the failing tests**

```go
func TestFramesAfterSequence(t *testing.T)
func TestFramesRejectsABadAfter(t *testing.T)    // 400
func TestFramesCapsTheLimit(t *testing.T)        // limit=99999 returns 2000
func TestOnlyGetIsServed(t *testing.T)           // POST, PUT, DELETE on / and /frames give 405
func TestPageLoadsNothingExternal(t *testing.T)  // same checks as statusweb
func TestReplayServesARecordingWithNoCluster(t *testing.T) // in cmd/showcase, with -replay and no reachable status URL
```

```python
def test_showcase_is_optional_and_on_loopback():
    service = load(COMPOSE)["services"]["showcase"]
    assert service["profiles"] == ["showcase"]
    assert service["ports"] == ["127.0.0.1:18781:8080"]
    assert service["mem_limit"] == "128m"
```

- [ ] **Step 2:** Run `cd go && go test ./internal/showcase ./cmd/showcase` and the deploy test, and expect failures.
- [ ] **Step 3:** Implement the server and command. `-replay` opens only the named file.
- [ ] **Step 4:** Add `/dist/showcase` to the `COPY` in `deploy/docker/Dockerfile:11`. Without it the image has no binary, though `./cmd/...` builds it.
- [ ] **Step 5:** Add the Compose service using `*node`, `command: [showcase]`, and the profile, port and limit above.
- [ ] **Step 6:** Add `showcase-up` beside `monitoring-up` (`$(COMPOSE) --profile showcase up -d showcase`), add it to `.PHONY`, and add `--profile showcase` to `cluster-down`.
- [ ] **Step 7:** Add the service to the memory table in `docs/cluster-vm.md`, and update the totals that `test_documented_memory_totals_include_replicas_and_profiles` reads. Include `showcase` in that test's sum.
- [ ] **Step 8:** Add `cmd/showcase` and `internal/showcase` to `go/cover-floors.txt`, one point under the measured value.
- [ ] **Step 9:** Run `make cluster-up showcase-up`, then `curl -s '127.0.0.1:18781/frames?after=0&limit=1'`. Expect a frame with `status_ok` true and four workers with `ok` true.
- [ ] **Step 10:** Run the tests and expect PASS.
- [ ] **Step 11:** Commit: `git commit -m "showcase: serve frames, replay, and a Compose profile [skip ci]"`.

### Task 6: The page

**Files:**
- Modify: `go/internal/showcase/page.html`, `go/internal/showcase/server_test.go`
- Create: `go/internal/showcase/page_test.go`, `python/tests/test_showcase_page.py` if Node is available in `make check`; otherwise a pure-Go test of the embedded fixture list only

**Interfaces:**
- Consumes: `GET /frames`.
- Produces, in the page's script, pure functions tested apart from drawing:
  - `derive(frames, opts) -> { job, map, reduce, rates, workers, timeline, eta, health }`
  - `rate(prev, next)`, `null` across a gap or a counter drop

- [ ] **Step 1: Choose the test harness.** Run `node --version` in the CircleCI image from `.circleci/continue-config.yml`. If Node is absent, do not add it: keep `derive` in JavaScript, and check it during `make showcase-e2e` with the browser's console output. Record the choice in the commit message.
- [ ] **Step 2: Write the failing tests** with fixture frames:

```text
progress counts finished tasks plus running fractions
a task with a backup counts once, at its furthest attempt
rate across a gap is null; a counter drop is a reset, not a negative rate
eta is "estimating" under 10% progress or with unsteady throughput
worker: unreachable with a timer, then lost once /status.json lists it; a new ID is joined
coordinator gap: outage length in the timeline; a job gone or changed afterwards says so
two jobs: rates are labeled cluster-wide while both overlap
client history stays bounded; resync: true drops history and restarts
```

- [ ] **Step 3:** Run them and expect failures.
- [ ] **Step 4:** Implement the header, progress, throughput (inline SVG), workers and timeline as in ADR 0023, with the banner "No answer from showcase; check the SSH tunnel". Reuse the status page's color tokens, and follow `prefers-color-scheme`. Keep the last 7,200 frames in the browser.
- [ ] **Step 5:** Add `?stage=1`, and in replay the play and pause, 1x, 2x and 4x, scrubber, failure markers, and `?t=<seconds>&autoplay=1`. Label replay "Replay".
- [ ] **Step 6:** Run the tests, then view the page at phone width and on a projector-sized window, in both themes.
- [ ] **Step 7:** Commit: `git commit -m "showcase: the page, its modes and its failure states [skip ci]"`.

### Task 7: End-to-end check, demo and captures

**Files:**
- Create: `scripts/showcase-e2e.sh`
- Modify: `Makefile`, `docs/demo.md`, `docs/runbook.md`, `docs/cluster-vm.md`, `docs/explainers/24-status.md`, `docs/README.md`, `docs/design/0023-showcase-service.md`, `CHANGELOG.md`, `.circleci/mapping.txt` only if a check requires an entry (never narrow it)

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1:** Write `scripts/showcase-e2e.sh`, run by `make showcase-e2e` and not by `make check`: submit a sort, `docker kill` a worker, wait for `SUCCEEDED`, then assert that the recording's last frame equals `/status.json` for that job, and that a replay of the file serves the same last frame.
- [ ] **Step 2:** Run `make showcase-e2e` and expect success.
- [ ] **Step 3:** Update the demo and runbook with `ssh -L 18781:127.0.0.1:18781 mr-vm`, `make showcase-up`, and the page in each demo step.
- [ ] **Step 4:** On the VM, rehearse a worker kill, a coordinator crash and a capture from replay. Check `docker stats` stays under 128 MB, and compare one benchmark's time with and without the service.
- [ ] **Step 5:** Record the README captures from a replay with `?t=` and `?autoplay=1`, using `scripts/capture-status-gifs.py` if #329 has merged.
- [ ] **Step 6:** Mark ADR 0023 accepted, add changelog bullets, and commit: `git commit -m "showcase: demo, docs and end-to-end check [skip ci]"`.
- [ ] **Step 7:** Follow AGENTS.md's final push: merge `main`, run `make check`, push without `[skip ci]`, and merge when all six checks are green on the head.
