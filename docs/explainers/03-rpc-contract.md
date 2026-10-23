# Explainer — The RPC Contract (#3)

A plain-language walkthrough of `proto/mapreduce/v1/`. The design rationale
is in [ADR 0003](../design/0003-rpc-contract.md).

## What problem it solves

The coordinator, workers and clients are separate programs, some in Go and
some in Python, talking over a network. They have to agree on **exactly what
bytes are sent, and what those bytes mean**. That agreement is the contract.

Think of a restaurant's order ticket. The waiter and the cook don't need to
speak the same language if both understand the ticket format.

## The tools

- **Protocol Buffers ("protobuf")** describe data shapes:

  ```proto
  message TaskId { string job_id = 1; TaskKind kind = 2; uint32 index = 3; }
  ```

  The `= 1`, `= 2`, `= 3` are **field numbers, not values**. On the wire,
  protobuf sends "field #1, here is the data", never the field's name. So a
  field's number must never change once it is in use. `buf breaking`
  enforces this.
- **gRPC** describes the conversations: "call `RequestTask` with a
  `RequestTaskRequest`, get back a `RequestTaskResponse`". It handles
  networking, timeouts and streaming.
- **Code generation** (`make proto`) turns the `.proto` files into Go and
  Python classes and client/server code. Nobody writes networking code by
  hand, and the two languages cannot drift apart.

## The three files

### `types.proto` — shared vocabulary

| Type | Meaning | Paper |
|---|---|---|
| `TaskKind` | Map or reduce | §3.1 |
| `TaskState` | Idle, in progress, or completed | §3.2 |
| `TaskId` | "Job X, map task #7": a piece of work | |
| `TaskAttemptId` | "…attempt #2": one *try* at that work | §3.3, §3.6 |
| `InputSplit` | A byte range of one input file | §3.1 |
| `PartitionLocation` | Where one map task's data for one reducer lives | §3.2 |
| `JobSpec` | The whole job request: program, inputs, output, R, formats | |
| `Counters` | Named tallies | §4.9 |
| `WorkerInfo` | A worker's address, host, zone and capacity | §3.4 |

**Why both tasks and attempts?** The same task can run more than once:
- re-run after its worker dies (§3.3);
- duplicated as a backup when it runs slowly (§3.6).

When two copies both report "done", the coordinator accepts the report from
the attempt that completes first and rejects the other. The attempt number
identifies each execution. It does not decide which report wins.

### `coordinator.proto` — the coordinator's interface

A worker's life:

1. **`RegisterWorker`**: "I'm at address A." The reply gives it an ID and a
   heartbeat interval.
2. **`RequestTask`**: "Anything for me?" The reply is exactly one of `map_task`,
   `reduce_task`, `wait` or `shutdown`.
3. The worker does the work.
4. **`ReportTask`**: the worker reports `success` (with output locations and
   counters) or `failure` (with the error and the record it crashed on).
   `accepted: false` means another attempt already won (§3.3).
5. **`Heartbeat`**: every few seconds the worker says it is still alive and
   how far along it is. The reply can `cancel` a losing backup task or ask
   the worker to `reregister`.
6. **`SubmitJob` / `GetJobStatus`**: the client's side, to start a job and
   check on it.

**Workers pull work; the coordinator never calls a worker.** This keeps the
coordinator simple and works through firewalls and container networks.

### `shuffle.proto` — workers handing data to each other

Map output stays on the map worker's disk (§3.1). Each reducer calls
`FetchPartition` on every map worker. The data **streams back in chunks**,
so a large partition never has to fit in memory. An `offset` field lets an
interrupted fetch resume where it stopped.

## Safety checks (in `make check`)

- **`buf lint`**: naming rules.
- **`buf breaking`**: fails on changes that would break running programs.
- **Stale-code check**: fails if someone edits a `.proto` file but forgets
  to run `make proto`.
- **Golden vectors**: Go and Python must turn
  `testdata/proto/map_task.txtpb` into the exact bytes in
  `map_task.binpb`.
- **Round-trip tests**: each language's generated client talks to its
  generated server.

These checks were confirmed to **fail** when something is deliberately
broken: one changed byte in the golden file, or a `.proto` edit without
regenerating.

## Likely review questions

- **Why gRPC rather than REST?** Typed contracts, code generation for both
  languages, and streaming for the shuffle.
- **How are duplicate completions handled?** Attempt IDs plus
  `ReportTaskResponse.accepted`: the first completion wins.
- **Why does map output stay on workers?** It avoids a central bottleneck.
  The cost is that a dead worker's finished map tasks must be re-run, as in
  the paper (§3.3).
- **How does the protocol evolve?** Add fields with new numbers and never
  reuse old ones. `buf breaking` enforces this.
