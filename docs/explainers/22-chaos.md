# Explainer — Chaos Testing (#29)

Each fault-tolerance feature has its own test, which provokes one failure
at one chosen moment. Real failures overlap and land at any moment. The
chaos test runs word count while it breaks the cluster at random, and then
checks the result against local mode, the oracle.

## What one run does

`TestChaos` (in `go/internal/worker/chaos_test.go`) starts a coordinator
with backups on and three workers, submits word count over the corpus, and
then makes six random choices, a few milliseconds apart:

| Event | How | What it exercises |
|-------|-----|-------------------|
| A machine dies, and another joins | the worker's context is cancelled and its shuffle server stops | heartbeat timeout, re-running its maps (#23), lost map output |
| A machine stalls for 50–250 ms | the worker's RPCs, in and out, are held up | stragglers, backups (#25), a live worker declared dead and its late reports rejected |
| The coordinator restarts | it stops, then recovers from its write-ahead log | recovery (#24), workers registering again |

The run passes if the output files equal the golden files, and the job's
counters equal local mode's (#27).

## Running it

```sh
go test ./internal/worker -run TestChaos                       # 3 runs
go test ./internal/worker -run TestChaos -chaos.runs=50        # the soak
go test ./internal/worker -run TestChaos -chaos.seed=7 -chaos.runs=1
```

Each run's choices come from its seed, so a failure can be run again. The
timing of those choices against the job is not fixed, so a replay may not
fail the same way.

## What it found

Its first runs failed on seed 7: the output held an extra `_temporary`
directory. A reduce attempt that had lost, to a backup or because its
worker was declared dead, kept running. It recreated the directory after
the job had cleaned it up. Now the job
creates `_temporary` when it starts (`task.Setup`), and attempts never do.
An attempt that runs after the job has finished fails, and leaves nothing.

## Limits

The test runs in one process, so a "dead" worker's task goroutine still
runs, like a machine cut off from the network rather than one powered off.
A stall holds up new RPCs but not a fetch already streaming. Killing
containers, `docker pause` and dropped packets need the Compose cluster
(#83). A driver for it can reuse this test's events and checks.
