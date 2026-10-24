# ADR 0003 — The gRPC/protobuf contract

**Status:** accepted · **Phase:** 0 · **Issue:** #3

## Context

The paper's master and workers talk through Google's internal RPC system.
We need a replacement that:

- works identically from Go and Python, because the two implementations must
  interoperate (ADR 0001);
- streams large amounts of data for the shuffle (§3.1 step 5);
- can evolve over 7 phases without breaking components already written.

## Decision

The contract lives in `proto/mapreduce/v1/` as four files.

| File | Contents |
|---|---|
| `types.proto` | Shared data types: `TaskId`, `TaskAttemptId`, `InputSplit`, `PartitionLocation`, `JobSpec`, `Counters`, `WorkerInfo`, and the `TaskKind` / `TaskState` enums |
| `coordinator.proto` | `CoordinatorService` (`RegisterWorker`, `RequestTask`, `ReportTask`, `Heartbeat`, `SubmitJob`, `GetJobStatus`) |
| `shuffle.proto` | `ShuffleService.FetchPartition`, a server-streaming RPC that every worker serves |
| `log.proto` | `LogRecord`, the coordinator's write-ahead log records (ADR 0007, added in Phase 3) |

The main design choices:

1. **Workers pull, the coordinator never pushes.** An idle worker calls
   `RequestTask` and receives a `oneof` of `map_task`, `reduce_task`, `wait`
   or `shutdown`.
   - The coordinator never needs to open connections *to* workers, which
     keeps it simpler and makes it work behind NAT or container networking.
   - Cancellation of losing backup tasks (§3.6) rides back on the heartbeat
     response.
2. **Tasks and attempts are different things.** A `TaskId` (job, kind,
   index) is stable. A `TaskAttemptId` adds an attempt number.
   - Re-execution after failures (§3.3) and backup tasks (§3.6) both create
     new attempts of the same task.
   - Every report names its attempt, so the coordinator can accept the first
     completion and reject duplicates (`ReportTaskResponse.accepted`).
3. **Map output stays on the worker.** A successful map task's `ReportTask`
   request carries one `PartitionLocation` per partition in
   `TaskSuccess.outputs` (§3.2); the response only says whether the report
   was accepted. Reducers fetch the data directly
   from the map worker with `FetchPartition`. The coordinator only stores
   locations and never touches data.
   - `PartitionLocation.uri` names the file instead when workers share a
     work directory and serve no shuffle (`worker -listen ""`). External
     shuffle mode (#43) will reuse it.
4. **Streaming shuffle with resume.** `FetchPartition` streams chunks, so
   memory stays bounded on both ends. An `offset` field lets an interrupted
   fetch continue instead of restarting.
5. **Paper features have fields from day one**, even where the behaviour
   comes later:
   - `skip_records` / `failed_record` for skipping bad records (§4.6);
   - `Counters` for counters (§4.9);
   - `TaskProgress.fraction_done` for stragglers (§3.6);
   - `InputSplit.locations` and `WorkerInfo.zone` for locality (§3.4).

   Adding fields later is also safe, but defining them now makes the design
   reviewable as a whole.
6. **`buf lint` (STANDARD rules) and `buf breaking` (FILE rules)** run in
   `make check`.
   - Lint enforces consistent naming: `_UNSPECIFIED` enum zero values, and
     unique `XRequest`/`XResponse` types per RPC.
   - The breaking check stops us from renumbering or retyping fields that
     already-deployed components depend on.
7. **Code generation** uses the `protoc` bundled in the `grpcio-tools` Python
   package for both languages. Go plugins are pinned with `go install` into
   `.tools/bin`.
   - Generated code is **committed**, so anyone can build the project
     without the generators.
   - `make check` regenerates the code and fails if anything changed.
8. **Golden wire vectors.** `testdata/proto/*.txtpb` (human-readable) and
   `*.binpb` (bytes). Both test suites must produce exactly those bytes.
   This is the first cross-language correctness check.

## Alternatives considered

- **Push scheduling** (the coordinator calls workers): more control over
  placement, but the coordinator must track worker addresses and
  reachability, and handle slow calls. Pull is the standard choice in
  comparable systems, e.g. the MIT 6.824 lab and Hadoop's TaskTracker
  heartbeats.
- **REST/JSON:** easy to inspect by hand, but untyped, with no streaming and
  no code generation. A REST facade for job submission arrives in Phase 6
  (#52).
- **Cap'n Proto / Thrift:** viable, but gRPC has the best Go and Python
  support and the most familiar tooling.
- **Generating with buf's remote plugins:** needs network access to the Buf
  Schema Registry at build time. The local `protoc` keeps builds offline and
  reproducible.

## Consequences

- Any protocol change starts in `proto/`, then `make proto`, then code in both
  languages.
- Generated Python imports are rewritten by `scripts/gen-proto.sh` so the
  code lives inside the `mapreduce_reloaded` package.
- Python requires `grpcio>=1.84.1` and `protobuf>=7.35.1`, matching the
  generator.
- Go requires Go 1.26+. gRPC v1.84 needs 1.25, but the patched
  `golang.org/x/net` and `golang.org/x/text` releases need 1.26.
