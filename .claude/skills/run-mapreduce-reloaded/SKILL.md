---
name: run-mapreduce-reloaded
description: Use when asked to run, start, stop, demo or screenshot the MapReduce-Reloaded cluster, submit a word count to it, kill a worker or restart the coordinator mid-job, or confirm a change works in the running system rather than only in tests.
---

# Running MapReduce-Reloaded

`drive.sh` starts the Docker Compose cluster (a coordinator and four
workers), runs jobs through `bin/mr`, disturbs a running job and checks its
output, and stops the cluster. Run it from the repository root; paths below
are relative to it.

```bash
bash .claude/skills/run-mapreduce-reloaded/drive.sh help
```

## One clone at a time

Every clone on a host drives the same Compose project, `mapreduce`, and the
same `mapreduce_output` volume, which holds the coordinator's WAL and the
experiments' inputs and outputs. Running `up` from a second clone
recreates the cluster under a running experiment, and `down` deletes that
volume. So `drive.sh` refuses everything except `status` and `page` (exit 3)
while a `bench/*.sh` or `bench/*.py` script is running, or while a cluster
started from another clone exists. If nothing in that clone is needed, stop
its cluster from there, or set `MR_DRIVE_FORCE=1`. Never set it while
`checking-campaign-status` shows a chain or campaign running.

## Commands

| Command | What it does | Time on the course VM |
| --- | --- | --- |
| `up` | `make cluster-up mr upload-example`: builds images, starts the cluster, builds `bin/mr`, uploads the four-book corpus | 37 s warm |
| `wordcount` | word count over the corpus; prints the job's counters and the top 5 words (`the 3444`) | 1 s |
| `big [MiB]` | writes a Zipf input (default 512 MiB) to `data/input/zipf/zipf.txt` | |
| `status JOB` | `bin/mr status JOB`: state, progress, backups, counters | |
| `page` | the status page at 127.0.0.1:18780 as text | |
| `kill-worker` | word count on the Zipf input (`big` first if missing), then the same job again, killing one worker once a map finishes; restores four workers; compares the outputs | about 1 min 45 s |
| `restart-coordinator` | the same, restarting the coordinator instead | about 1 min 45 s |
| `down` | `make cluster-down`: stops the cluster and deletes its volumes; keeps `data/` | |

A disturbance passes when the last line is `output matches the undisturbed
run`; otherwise it prints `OUTPUT DIFFERS` and exits 1.

## Screenshot of the status page

Headless Chromium in Docker (`chromedp/headless-shell`, Chromium 158) hangs
on the course VM with no output, even on a static page, so take the
screenshot on your own machine through a tunnel:

```bash
ssh -f -N -o ExitOnForwardFailure=yes -L 18780:127.0.0.1:18780 mr-vm
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless --disable-gpu \
  --hide-scrollbars --user-data-dir="$(mktemp -d)" --virtual-time-budget=5000 \
  --window-size=1280,900 --screenshot="$PWD/status.png" http://127.0.0.1:18780/
pkill -f 'ssh -f -N -o ExitOnForwardFailure=yes -L 18780'
```

Open the PNG to check it: a header with jobs, workers and lost counts, then
one card per job.

## Gotchas

- The status page lists every job in the WAL, including other clones' and
  past experiments' jobs, because the WAL is in the shared volume. Job IDs
  continue from there (`job-59` after an experiment), so read the ID that
  `drive.sh` prints.
- `page` always shows "No answer from the coordinator. Showing the last
  update." because that line is hidden by the page's script, not by HTML.
  It does not mean the coordinator is down; `status` tells you.
- After `restart-coordinator` the disturbed job took 84 s against 29 s
  after `kill-worker`; both outputs matched the undisturbed run's.
- `big N` writes a little more than N MiB, because it ends on a whole line.
- `--virtual-time-budget` is needed for the screenshot: the page polls the
  coordinator every few seconds, so the browser never goes idle on its own.
- Run the script with `bash`: a copy made with `scp` loses its exec bit.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `refusing: another clone started the cluster` | Run `down` from the clone it names, or `MR_DRIVE_FORCE=1` if nothing there is needed |
| `refusing: a bench script is running` | Wait for it; `checking-campaign-status` shows how far it has got |
