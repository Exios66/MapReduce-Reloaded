# The demo

A walkthrough for the course review, in the order that tells the story: the
cluster, a job, a smaller shuffle, the output, a lost worker and a restarted
coordinator. The core runs in about six minutes;
[questions and the feature tour](#questions-and-the-feature-tour) can fill
any time left. [`examples/demo.ipynb`](../examples/demo.ipynb) runs the same
sequence cell by cell, each claim checked by an `assert`, and ends with the
evaluation figures. Timings are for the course VM.

## What each moment shows

| Time | Moment | Requirement or paper section |
|---|---|---|
| 0:00 | Four workers, one CPU each, and the coordinator | Cluster of 1 coordinator and 4 one-CPU workers (§5.1 of the assignment) |
| 0:30 | `mr submit` of a Go job file, from the host | Client submits a job from a file; map and reduce are user code (paper §2) |
| 1:15 | The same job with the combiner off | Combiner (paper §4.3), measured in E1 and E6 |
| 2:00 | The output, byte for byte the local oracle's | Correct output; local execution (paper §4.7) |
| 2:45 | A worker killed mid-sort; the job succeeds | Worker failure (paper §3.3), E3 |
| 4:00 | The coordinator restarted mid-sort; the job succeeds with the same output | Master failure, which the paper aborts on; our write-ahead log (ADR 0007), E4 |
| 5:15 | The fault-tolerance figure and the status page | Performance evaluation (paper §5), status pages (paper §4.8) |

## Before the day

- [ ] On the VM: `git pull` on `main`, `make check`, then `make cluster-up mr`.
- [ ] `make upload-example`, and generate the sort input once, so the demo
      does not wait 20 seconds for it:
      `python3 bench/gen-sort.py 1073741824 42 > data/input/sort-1g.txt`.
- [ ] `df -h /` shows at least 6 GB free. The first sort's 1.1 GB of output
      stays while the second sort peaks at 2.75 GB, and a worker takes no
      task with under 2 GiB free
      ([ADR 0017](design/0017-disk-and-memory-bounds.md)).
- [ ] Rehearse the whole script once, start to finish, with a timer.
- [ ] After the rehearsal's `make cluster-down`, run `make cluster-up` again,
      then run the notebook top to bottom (*Kernel → Restart & Run All*);
      every cell's `assert` passes.
- [ ] Open the status page, <http://127.0.0.1:18780/>, through the SSH
      forward, and this page, the README's results, and
      [`bench/evaluation/faults.png`](../bench/evaluation/faults.png) in tabs.
- [ ] Optional for questions: `make monitoring-up` for Grafana on
      <http://127.0.0.1:13000/>.

## Before the clock starts

```sh
make cluster-up mr        # build the image, start the coordinator and four workers
make upload-example       # the corpus to /data/input/corpus and a 10 MB web log
```

Keep a terminal on the VM for the client, and the status page in a browser.

## 0:00 — The cluster

```sh
docker compose -p mapreduce -f deploy/compose/compose.yaml ps
docker inspect -f '{{.Name}} cpus={{.HostConfig.NanoCpus}}' mapreduce-worker-1
```

Point at the four workers and the coordinator, the one-CPU and 768 MB
limits from [cluster-vm.md](cluster-vm.md), and the status page: four live
workers and no jobs yet. Say what runs where: the client is on the VM, every
container sees `data/` at `/data`, and map output stays inside each worker.

## 0:30 — Submit a job

```sh
OUT=/data/out/demo-$(date +%s)
bin/mr submit -m 8 -r 4 -wait examples/wordcount.go "$OUT" '/data/input/corpus/*.txt'
```

The program is a Go job file the workers interpret: nothing is compiled or
uploaded. Show [`examples/wordcount.go`](../examples/wordcount.go): `Map`,
`Reduce`, and `var Combine = api.Sum`. Read the status lines aloud: 8/8
maps, 4/4 reduces, the counters. The job is over in about a second.

## 1:15 — The combiner

```sh
bin/mr submit -m 8 -r 4 -wait -combiner=false examples/wordcount.go "$OUT-nocombiner" '/data/input/corpus/*.txt'
diff <(bin/mr cat "$OUT") <(bin/mr cat "$OUT-nocombiner") && echo same output
```

Compare `map.output.bytes` in the two replies: about 235 kB with the
combiner and 607 kB without, for the same output. That is the paper's
§4.3: each map task sums its own counts before the shuffle. At 1 GiB the
saving is 15× to 78× the bytes and 18–44% of the job time (E1), and 3× to
33× on real text (E6).

## 2:00 — Read the output, and check the oracle

```sh
bin/mr cat "$OUT" | sort -t$'\t' -k2 -nr | head -5
bin/mr run -local -m 8 -r 4 examples/wordcount.go /tmp/demo-local 'testdata/corpus/*.txt'
diff <(bin/mr cat "$OUT") <(cat /tmp/demo-local/part-r-*) && echo cluster matches local
diff <(cat testdata/corpus/expected/part-r-*) <(cat /tmp/demo-local/part-r-*) && echo local matches golden
```

The cluster's output is byte for byte what local mode, the single-process
oracle, writes, and both match the committed golden output. `make e2e` checks the same equality on every run.

## 2:45 — Kill a worker in the middle of a job

The coordinator notices a dead worker after a 10-second heartbeat timeout,
so use a job that lasts longer: a 1 GiB sort, about 13 s undisturbed.

```sh
[ -f data/input/sort-1g.txt ] || python3 bench/gen-sort.py 1073741824 42 > data/input/sort-1g.txt
KILLED=/data/out/sort-kill-$(date +%s)
( sleep 5; docker kill mapreduce-worker-4 ) &
bin/mr submit -m 64 -r 4 -wait distsort "$KILLED" /data/input/sort-1g.txt
docker logs mapreduce-coordinator-1 2>&1 | grep 'worker lost' | tail -1
docker compose -p mapreduce -f deploy/compose/compose.yaml up -d worker
```

The job still ends `SUCCEEDED` with 64/64 maps and 4/4 reduces, in about
35 s. The log line `worker lost ... tasks_reset=N` counts the tasks that
ran again elsewhere: the dead worker's running tasks and its finished map
tasks, whose output was on its disk (paper §3.3). The status page marks the
worker dead, then shows the replacement.

## 4:00 — Restart the coordinator in the middle of a job

The paper's master is a single point of failure: when it dies, the job is
aborted. Here every decision is in a write-ahead log before the coordinator
replies, so a restarted coordinator replays it and carries on.

```sh
RESTARTED=/data/out/sort-restart-$(date +%s)
JOB=$(bin/mr submit -m 64 -r 4 distsort "$RESTARTED" /data/input/sort-1g.txt)
sleep 6; bin/mr status "$JOB" | head -1
docker kill mapreduce-coordinator-1 && docker start mapreduce-coordinator-1
until bin/mr status "$JOB" 2>/dev/null | grep -qE 'SUCCEEDED|FAILED'; do sleep 2; done
bin/mr status "$JOB" | head -4
cat data/out/${KILLED#/data/out/}/part-r-* | md5sum
cat data/out/${RESTARTED#/data/out/}/part-r-* | md5sum
```

The first status line must say `MAPPING` or `REDUCING`; if the job already
finished, there was nothing to recover. `docker kill` crashes the
coordinator, as `make e2e` does; a `docker restart` would stop it cleanly,
and a clean stop tells idle workers to exit. Submit without `-wait`: the
client's connection ends when the coordinator does, so poll `mr status`
instead. The job ends `SUCCEEDED`, about 1.6 s slower than an undisturbed
run (E4), and both checksums match: a lost worker and a lost coordinator
give the same output as each other.

## 5:15 — The results

Show [`bench/evaluation/faults.png`](../bench/evaluation/faults.png): the
job time each disturbance added, measured three times each on this VM, and
every disturbed run's output identical to an undisturbed run's. Then the README's E0 (word count runs
3.8× faster on four workers) and E1 figures. Point at the status page for
the jobs just run.

## 5:45 — Close

```sh
rm -r "data/out/${OUT#/data/out/}" "data/out/${OUT#/data/out/}-nocombiner" \
  "data/out/${KILLED#/data/out/}" "data/out/${RESTARTED#/data/out/}" /tmp/demo-local
make cluster-down
```

## Questions and the feature tour

Each answer has a command or a page to show.

| Question | Show |
|---|---|
| How do you know it is correct? | `make chaos RUNS=5 SEED=1`: seeded worker kills and pauses and coordinator crashes, each run checked against the reference output. [Testing](testing.md) lists the rest: golden vectors shared by Go and Python, a model-based test of the coordinator, fuzzing, mutation testing |
| Which paper features are built? | [Paper mapping](paper-mapping.md): F1–F24, each with its code and the test that proves it; `internal/papermap` fails the build if one is missing |
| What about stragglers? | [`faults.png`](../bench/evaluation/faults.png) and [E2](../bench/README.md#evaluation-e0e6): backups cut a straggler's job from 17.6 s to 11.2 s. `deploy/compose/backups-off.yaml` turns them off; `speculation-late.yaml` switches to LATE (ADR 0019) |
| Does it scale? | [E0](../bench/README.md#evaluation-e0e6): 3.8× for word count on four workers; grep only 2.3×, because a 2 s job is mostly fixed costs |
| Can it use object storage? | `make e2e-s3`: the same job with input and output in an S3-compatible store; the commit is a copy and a delete ([ADR 0013](design/0013-commit-on-object-storage.md)) |
| Is the Python side real? | `make py-e2e`: the Python coordinator drives Go workers to the same output; local mode in Python writes the same bytes as Go |
| How is it observed? | The status page (paper §4.8), `/metrics` for Prometheus, JSON logs, and `make monitoring-up` for Grafana |
| What if the data does not fit in memory? | `make soak`: 1 GiB word count, combiner off, on two workers limited to 256 MB; the workers spill to disk and none is killed for memory ([compose README](../deploy/compose/README.md)) |
| How was it built? | [Report, process and review](report.md#process-and-review): small pull requests, CI on each, review by CodeRabbit and CodeAnt |

## If something goes wrong

- `mr submit` says the coordinator is unavailable: `make cluster-up`. During
  the restart step this is expected for a few seconds; keep polling.
- A job says `FAILED`: `bin/mr status <id>` prints its counters and error;
  `docker logs mapreduce-coordinator-1` and the workers' logs say why.
- The status page shows fewer than four live workers after a step:
  `docker compose -p mapreduce -f deploy/compose/compose.yaml up -d worker`.
- Disk: `df -h /`; the experiments' inputs and outputs live in the Compose
  `output` volume, and `make cluster-down` removes it.

## Recording the README GIFs

`scripts/capture-status-gifs.py` regenerates `docs/assets/status-*.gif` from
a real job running on the cluster, in the reader's light or dark color
scheme (the status page's own `?theme=dark` parameter). It captures the
page's server-rendered refreshes while one whole job runs — a submit's
start to its last frame, never a looped fragment — and reports frame count,
duration, dimensions and file size for each variant, plus a
`<prefix>-provenance.json` sidecar recording the commit, machine, job, input
and capture settings. A minutes-long job ships as an honest time-lapse:
consecutive frames are `--interval` seconds apart, the page's own duration
clock is the visible elapsed-time indicator, and the speed factor (interval
over GIF frame delay) is in the provenance.

Requirements: a Chrome-family browser (`CHROME` if not in the default
location), `ffmpeg` and `ffprobe` on `PATH`, and the cluster running, e.g.
via `make cluster-up` on the course VM or Compose.

```sh
# words: a 1 GiB web log, word count with 32 maps and 4 reduces (E0-like)
mkdir -p data/input/capture-1g
python3 bench/gen-weblog.py 1073741824 > data/input/capture-1g/access.log
scripts/capture-status-gifs.py \
  --submit 'bin/mr submit -wait' \
  --program examples/wordcount.go -m 32 -r 4 \
  --out /data/out/gif-wordcount-1g \
  --inputs /data/input/capture-1g/access.log \
  --prefix status-wordcount-1g --assets docs/assets
```

`--status-url` defaults to the local page, `http://127.0.0.1:18780/`; pass
it to capture through an SSH-forwarded page. `--watch` (no `--submit`)
captures a job someone else already submitted. The script refuses a capture
shorter than 3 frames as a looped fragment; it never synthesizes progress
values.
