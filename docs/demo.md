# The five-minute demo

A walkthrough for a course review or a portfolio, in the order that tells the
story: the cluster, a job, the output, a failure, and the recovery.
[`examples/demo.ipynb`](../examples/demo.ipynb) runs the same sequence cell by
cell: it submits the compiled programs and the job file, plots the output,
kills a worker during a 1 GiB sort and checks local mode.
Timings are for the course VM.

## Before the clock starts

```sh
make cluster-up mr        # build the image, start the coordinator and four workers
make upload-example       # the corpus to /data/input/corpus and a 10 MB web log
```

Open <http://127.0.0.1:18780/> on your laptop, through the SSH forward, for
the status page. Keep a terminal on the VM and a second one for the client.

## 0:00 — The cluster

```sh
docker compose -p mapreduce -f deploy/compose/compose.yaml ps
```

Point at the four workers and the coordinator, the 256 MB / 768 MB limits
from [cluster-vm.md](cluster-vm.md), and the status page: four live workers
and no jobs yet. Say what runs where: the client is on the VM, every
container sees `data/` at `/data`, and map output stays inside each worker.

## 0:30 — Submit a job

```sh
OUT=/data/out/demo-$(date +%s)
bin/mr submit -m 8 -r 4 -wait examples/wordcount.go "$OUT" '/data/input/corpus/*.txt'
```

The program is a Go job file the workers interpret: nothing is compiled or
uploaded. Read the status lines aloud: 8/8 maps, 4/4 reduces, the counters
(`map.input.records`, `map.output.records`, `reduce.output.records`). The
job is over in about a second; the status page showed it while it ran.

## 1:30 — Read the output, and check the oracle

```sh
bin/mr cat "$OUT" | sort -t$'\t' -k2 -nr | head -5
bin/mr run -local -m 8 -r 4 examples/wordcount.go /tmp/demo-local 'testdata/corpus/*.txt'
diff <(cat testdata/corpus/expected/part-r-*) <(cat /tmp/demo-local/part-r-*)
```

The cluster's output is byte for byte what local mode, the single-process
oracle, writes. `make e2e` checks the same equality on every run.

## 2:30 — Kill a worker in the middle of a job

The coordinator notices a dead worker after a 10-second heartbeat timeout,
so use a job that lasts longer (a 1 GiB sort, about 13 s; generating the
input takes about 15 s and 3 GB of disk, so check `df -h /` first).

```sh
python3 bench/gen-sort.py 1073741824 42 > data/input/sort-1g.txt
( sleep 5; docker kill mapreduce-worker-4 ) &
bin/mr submit -m 64 -r 4 -wait distsort "/data/out/sort-kill-$(date +%s)" /data/input/sort-1g.txt
docker logs mapreduce-coordinator-1 2>&1 | grep 'worker lost' | tail -1
docker compose -p mapreduce -f deploy/compose/compose.yaml up -d worker
```

The job still ends `SUCCEEDED` with 64/64 maps and 4/4 reduces. The log line
`worker lost ... tasks_reset=8` says the dead worker's map output went with
it and those tasks ran again elsewhere (§3.3 of the paper, §3.3 here).

## 4:00 — Recovery is the point

The output is the same as a run with no kill; the coordinator's write-ahead
log is why a restarted coordinator still knows the job. Point at the status
page again: the job is `SUCCEEDED`, with its counters and the lost worker
replaced.

## 4:30 — Close

```sh
rm -r data/out/demo-* data/out/sort-kill-* data/input/sort-1g.txt
make cluster-down
```

If there is time: `make chaos RUNS=5 SEED=1` runs word count with worker
kills and pauses and coordinator crashes, seeded and repeatable, and must
end with `chaos: 5 runs from seed 1, each with the reference output`.

## If something goes wrong

- `mr submit` says the coordinator is unavailable: `make cluster-up`.
- A job says `FAILED`: `bin/mr status <id>` prints its counters and error;
  `docker logs mapreduce-coordinator-1` and the workers' logs say why.
- Disk: `df -h /`; the experiments' inputs and outputs live in the Compose
  `output` volume, and `make cluster-down` removes it.
