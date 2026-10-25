# Go implementation

The primary implementation: the coordinator and worker that run jobs on a
cluster, the `mr` command, local mode, and the example programs. The module is
`github.com/exios66/mapreduce-reloaded/go`.

```sh
cd go
go build ./...                # everything
go test -race ./...           # the unit and cluster tests (make go-test runs these)
go run ./cmd/mr run -local -m 3 -r 4 wordcount /tmp/wc '../testdata/corpus/*.txt'
```

## Commands (`cmd/`)

| Command | What it does |
|---|---|
| [`mr`](cmd/mr/) | Runs jobs: `mr run -local` in one process, `mr submit` on a cluster, `mr status` for a job's state and counters |
| [`coordinator`](cmd/coordinator/) | The paper's master (§3.2). Flags: `-addr` (default `:7070`), `-http` (status page and `/metrics`, default `:8080`), `-data-dir` (write-ahead log; without it a restart loses the job), `-backups` (default on), `-log-format text\|json` |
| [`worker`](cmd/worker/) | Runs tasks for a coordinator. Flags: `-coordinator`, `-listen` (serves map output, default `:7071`), `-advertise`, `-work-dir`, `-http` (serves `/metrics`), `-log-format` |

The Dockerfile in [`deploy/docker`](../deploy/docker/) builds all three into one
image.

## Public package (`pkg/api`)

[`pkg/api`](pkg/api/) is the programming model: `Mapper`, `Reducer`, an
optional `Combiner` and `Partitioner`, a `Context` for parameters, counters and
side outputs, and a registry that maps a program's name to its code. A program
registers itself from an `init` function, and `mr` imports it. Keys and values
are raw bytes ([ADR 0005](../docs/design/0005-programming-api.md)).

## Example programs (`examples/`)

One package for each program: `wordcount`, `grep`, `invertedindex`,
`urlfreq`, `reverselinks`, `termvector` and `distsort`. `examples/all` imports
them all, as `mr` does. See the [root README](../README.md#example-programs)
for what each takes and gives.

## Internal packages (`internal/`)

| Package | Role |
|---|---|
| `coordinator` | Task table, worker registry, scheduling, recovery from the log, backup tasks, bad-record skip lists |
| `worker` | The worker's task loop and heartbeats; holds the cluster and chaos tests |
| `task` | Runs one attempt of a map or reduce task, shared by local mode and workers; owns the commit protocol and the built-in counters |
| `local` | Local mode (§4.7), the oracle that cluster runs are checked against |
| `shuffle` | The `FetchPartition` server and client that move map output |
| `extsort`, `combine` | External sort and grouping; the combiner |
| `formats`, `formats/kv` | Input and output formats; the MRKV block format with its codecs |
| `partition`, `split` | The partition hash; division of input into splits |
| `storage` | Local and S3 files behind one interface |
| `wal`, `wire` | The write-ahead log; conversion between RPC messages and Go types |
| `statusweb`, `promtext`, `logs` | Status page and `/metrics`; the Prometheus text format; structured logging |
| `papermap` | Checks that every code path and test that `docs/paper-mapping.md` names exists |

`gen/` holds the Go code generated from [`proto/`](../proto/). It is committed,
so building needs no code generators; `make proto` regenerates it.

## Tests

Every package has tests next to its code. The slower ones are in
`internal/worker`: they start a coordinator and several workers in one
process and check that jobs finish with the right output when workers die, the
coordinator restarts, tasks hang, or records are bad. `TestChaos` does this at
random from a seed:

```sh
go test ./internal/worker -run TestChaos -chaos.runs=50           # 50 seeded runs
go test ./internal/worker -run TestChaos -chaos.seed=7 -chaos.runs=1   # replay seed 7
```
