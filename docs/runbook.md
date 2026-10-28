# Runbook: from SSH to a full demo

Every step to bring the cluster up on the course VM and run the demo, in
order. [Cluster VM](cluster-vm.md) explains the budgets and the setup in
more detail; this page is the checklist. Every command here was run on the
course VM on 2026-10-26.

The VM: 4 vCPUs (Intel Xeon Silver 4516Y+), 8 GB of RAM,
a 35 GB disk with a 33 GB root filesystem, Ubuntu 24.04.4 LTS, Docker 29.8.1
and Compose 5.5.1. See [the VM](cluster-vm.md#the-vm) for the full list.

## 1. Connect

Connect to the campus VPN first; the VM is not reachable without it.

On your laptop, add an alias to `~/.ssh/config` once, with your own host
and user name (they stay out of this public repository):

```
Host mr-vm
    HostName <your-vm-hostname>
    User <your-username>
```

Then connect, forwarding the status page (18780) and Grafana (13000) so
they open in your laptop's browser:

```sh
ssh -L 18780:127.0.0.1:18780 -L 13000:127.0.0.1:13000 mr-vm
```

## 2. One-time setup on the VM

Skip this section if `~/MapReduce-Reloaded` already exists.

```sh
sudo apt-get install -y make gcc             # not on the VM by default; gcc is for go test -race
git clone https://github.com/Exios66/MapReduce-Reloaded.git
cd MapReduce-Reloaded
scripts/vm-setup.sh --install                # Go, the Python venv, golangci-lint, shellcheck, buf
echo 'export PATH="$HOME/.local/go/bin:$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
scripts/vm-setup.sh                          # every line should say OK
make check PYTHON=python/.venv/bin/python    # the full Go and Python quality gate
```

No reboot is needed after installing `make` or Go.

## 3. Start of every session

```sh
cd ~/MapReduce-Reloaded
git pull
scripts/vm-setup.sh          # checks memory and disk too; read any WARN lines
free -m && df -h /           # other coursework shares the VM
```

The cluster needs about 3.3 GB of memory and a few GB of disk. If the
`memory` line warns, stop other coursework containers you do not need.

## 4. Start the cluster

```sh
make cluster-up              # build the image; start 1 coordinator + 4 workers
docker compose -p mapreduce -f deploy/compose/compose.yaml ps
```

All five containers should be `Up`. To shorten the commands below:

```sh
C="docker compose -p mapreduce -f deploy/compose/compose.yaml"
```

## 5. The demo

### 5.1 Each worker is limited to one CPU

```sh
docker inspect -f '{{.Name}} cpus={{.HostConfig.NanoCpus}}' $($C ps -q worker)
docker stats --no-stream | grep mapreduce
```

Each worker shows `cpus=1000000000` (one CPU) and a 768 MiB memory limit.

### 5.2 Correct output on the cluster

```sh
make e2e                     # word count; output must match the golden files byte for byte
make e2e-s3                  # the same job, input and output in the S3 store
```

Each ends with `e2e: output matches the golden output`. The golden output
is what local mode produces, so this also shows the cluster agrees with
the single-process oracle (§4.7).

```sh
make chaos RUNS=5 SEED=1     # worker kills and pauses, coordinator crashes, mid-job
```

`make chaos` runs word count over a 256 MiB Zipf input once undisturbed,
then RUNS times with three to five seeded events each, and ends with
`chaos: 5 runs from seed 1, each with the reference output`. A failing
run prints its seed; `scripts/chaos.sh 1 SEED` replays it.

### 5.3 Upload data, submit a job file and read its output

```sh
make mr                      # the host client, bin/mr
make upload-example          # the corpus to /data/input/corpus, a web log to /data/input/weblog
OUT=/data/out/wc-$(date +%s) # a new path each time: output directories are never overwritten
JOB=$(bin/mr submit -m 8 -r 4 examples/wordcount.go "$OUT" '/data/input/corpus/*.txt')
bin/mr status "$JOB"
bin/mr cat "$OUT" | sort -t$'\t' -k2 -nr | head
```

The client runs on the VM, outside every container, and `/data` is the
repository's `data/` directory, mounted in every container. `mr submit`
prints `mr: 8 map tasks (-m 8)` and the job's id; run `mr status` again
until the job says `SUCCEEDED`. `mr status` prints the job's state, task
progress, backup tasks and counters. While a job runs, open
<http://127.0.0.1:18780/> on your laptop (through the SSH forward) for the
status page; `/metrics` is next to it.

### 5.4 A worker dies in the middle of a job

A job must outlast the 10-second heartbeat timeout for the coordinator to
re-run a lost worker's tasks, so this uses a 1 GiB sort (about 13 s on the
VM; generating the input takes about 15 s and needs about 3 GB of disk, so
check `df -h /` first).

```sh
python3 bench/gen-sort.py 1073741824 42 > data/input/sort-1g.txt

( sleep 5; docker kill mapreduce-worker-4 ) &
bin/mr submit -m 64 -r 4 -wait distsort "/data/out/sort-kill-$(date +%s)" /data/input/sort-1g.txt
docker logs mapreduce-coordinator-1 2>&1 | grep 'worker lost' | tail -1
$C up -d worker              # bring the fourth worker back
```

The job still ends `SUCCEEDED` with 64/64 maps and 4/4 reduces. The
coordinator's log shows `worker lost ... tasks_reset=8`: the dead worker's
map output was lost with it, so those map tasks ran again elsewhere
(§3.3). On 2026-10-26 the job took 38 s with the kill and 13 s without.

### 5.5 A benchmark with data rates over time

```sh
SIZE=67108864 make bench SCENARIO=grep       # 64 MiB; also sort and zipf
ls bench/results/
```

Each run writes `rates.csv`, `samples.csv`, `status.txt` and `result.json`
(the counters, the git commit and the machine) under `bench/results/`.
`make bench-codecs` compares the compression codecs.

### 5.6 The evaluation: experiments and plots

```sh
python/.venv/bin/pip install matplotlib     # once, for the plots
mkdir -p bench/results
nohup bench/experiments.sh all > bench/results/experiments.log 2>&1 &
tail -f bench/results/experiments.log        # about 90 minutes for all six
python/.venv/bin/python bench/plot-experiments.py bench/results bench/plots
```

`bench/experiments.sh E0|E1|E2|E3|E4|E5` runs one experiment of
[design.md](../design.md#performance-evaluation); `nohup` keeps it running if
SSH drops. The plots land in `bench/plots/E0.png` … `E5.png`, and the
script prints the tables that go in [`bench/README.md`](../bench/README.md#evaluation-e0e5).
`SIZE` and `REPEAT` shrink a trial run, e.g. `SIZE=268435456 REPEAT=1`.
E5 is the slowest: its three runs of the naive job file take about 7 minutes each.

### 5.7 Monitoring (optional)

```sh
make monitoring-up
```

Open <http://127.0.0.1:13000/> on your laptop for the Grafana dashboard.
This adds 512 MB of memory limits.

## 6. Shut down

```sh
make cluster-down
```

This removes only this project's containers and its `output` and
`s3-output` volumes, with the experiments' inputs and outputs. It never
touches other coursework containers, and it keeps `data/`: job output in
`data/out/` and inputs in `data/input/` are yours to delete with `rm -r`,
for example `rm -r data/out/sort-kill-* data/input/sort-1g.txt`. Delete old
`bench/results/` directories as you go to keep disk free. To drop old
output from the `output` volume while the cluster runs, select only
completed directories, for example `make clean-output
CLEAN_OUTPUTS='exp-E0-w4-1'`; it also prints the disk use. Inputs, the
write-ahead log, and all unselected directories are preserved.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `make: command not found` | `sudo apt-get install -y make` |
| `go: command not found` | `export PATH="$HOME/.local/go/bin:$PATH"` (step 2 adds it to `~/.bashrc`) |
| `ssh: Could not resolve hostname` or a timeout | Connect to the VPN |
| A container exits with `OOMKilled` | Check `free -m`; stop other coursework containers, or skip the monitoring profile |
| `shared storage data is not a directory` | Run `bin/mr` from the repository root, after `make cluster-up` |
| `no space left on device` | `rm -r data/out/<old jobs>`; `make clean-output CLEAN_OUTPUTS='completed-directory'`; if that is not enough, `make cluster-down`, delete `bench/results/*`, then `docker image prune` |
| A worker logs `disk nearly full; not taking tasks` | Free space as above; the worker resumes by itself |
| The status page does not load on the laptop | Reconnect with the `-L 18780:...` forward from step 1 |
