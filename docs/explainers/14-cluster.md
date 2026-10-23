# Explainer — Running on a Cluster (#16)

## The loop

Workers pull work (§3.1). Each worker:

1. registers with the coordinator and gets an ID;
2. asks for a task with `RequestTask`;
3. runs it with the same code local mode uses (`go/internal/task`);
4. reports the result with `ReportTask`, and goes back to step 2.

The coordinator answers each request with a map task, a reduce task, "wait
and ask again", or "shut down". It hands out map tasks first and reduce
tasks only once every map task has completed, because each reducer needs a
piece of every map output.

```
coordinator -addr :7070
worker -coordinator host:7070     # as many as you like
mr submit -coordinator host:7070 -wait wordcount out/ 'in/*.txt'
mr status -coordinator host:7070 job-1
```

## What travels in the messages

- A **map task** carries the job, its attempt ID and its split.
- A **reduce task** carries the job, its attempt ID and where partition r of
  every map output lives.
- A **report** carries the attempt's counters and, for a map task, the
  location and size of each of its R output files.

The coordinator accepts the first successful report for a task and rejects
later ones; only accepted attempts' counters count (§3.3, §4.9).

## Where map output lives

Each worker keeps its map output on its own disk and serves it to reducers;
see [15-shuffle.md](15-shuffle.md). Workers started with `-listen ""` and
one shared `-work-dir` skip the network: reducers read map output from the
shared directory by the `uri` field of `PartitionLocation`.

Map task m writes partition r to `<work-dir>/<job>/map-<m>-r-<r>.kv`, the
same name local mode uses. A test checks that the cluster's map output is
byte for byte local mode's for the same splits, so the two runners share
one map path (`task.Map`), not just a final answer.

## What happens when things go wrong

- A task that fails is reported as failed and becomes idle again, so
  another attempt runs it.
- Workers may start before the coordinator; they wait for it.
- Workers may join at any time, and leave between tasks.
- A worker that dies *during* a task is not detected yet: heartbeat expiry
  and re-execution are #23.

## How we know it works

- An integration test runs an in-process coordinator and three workers,
  which join one after another, over localhost gRPC. The word count output
  matches the golden files byte for byte.
- A second test runs a program whose map tasks all fail on their first
  attempt; the job still succeeds, with the right counters.
- Unit tests cover each RPC's edge cases: unknown workers and jobs,
  duplicate reports, failures, a second job while one runs, and the default
  R of 2 per worker (§3.5).
