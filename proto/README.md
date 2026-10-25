# Protocol contract

The protobuf definitions every component shares, in package `mapreduce.v1`
([ADR 0003](../docs/design/0003-rpc-contract.md)). The Go and Python code is
generated from them and committed.

| File | Contents |
|---|---|
| [`coordinator.proto`](mapreduce/v1/coordinator.proto) | `CoordinatorService`: `RegisterWorker`, `RequestTask`, `ReportTask`, `Heartbeat`, `SubmitJob`, `GetJobStatus`. Workers pull tasks; clients submit and monitor jobs. |
| [`shuffle.proto`](mapreduce/v1/shuffle.proto) | `ShuffleService.FetchPartition`: a stream from the worker that holds a map task's output |
| [`types.proto`](mapreduce/v1/types.proto) | Messages shared by both: `JobSpec`, tasks, attempts, counters, worker info |
| [`log.proto`](mapreduce/v1/log.proto) | The records of the coordinator's write-ahead log ([ADR 0007](../docs/design/0007-write-ahead-log.md)) |

Comments in the files cite the sections of the paper they implement.

## Changing the contract

```sh
make proto            # regenerate go/gen and python/mapreduce_reloaded/gen
make proto-lint       # buf lint
make proto-breaking   # buf breaking: no change may break an existing field
make proto-check      # all three, and fails if the generated code is stale
```

Commit the regenerated code with the change: `make check` compares it with
git. `scripts/gen-proto.sh` runs the generator, and `make tools` installs the
pinned tools. Golden messages for both languages are in
[`testdata/proto`](../testdata/proto/).
