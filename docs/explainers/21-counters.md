# Explainer — Counters (#27)

A counter is a named number that a job's tasks add to, such as how many
records were read or how many lines were malformed. The paper's workers
send their counters to the master with each task's report, and the
master adds them up (§4.9). User code calls `ctx.Count(name, delta)` (Python:
`ctx.count(name, delta)`).

## Each task counts once

A task can run more than once: it is retried after a failure, re-run after
its worker dies, or backed up near the end of a phase. Adding every
attempt's counters would count its records several times. So the
coordinator keeps only the counters of each task's accepted attempt, keyed
by task, and sums them when asked (`job.total`). A map task that runs
again after its worker dies replaces its counters, rather than adding to
them.

`TestClusterCountersMatchLocal` checks this. A job with a backup that wins
and an attempt that fails late reports the same totals as local mode,
where every task runs once.

## Built-in counters

| Counter | What it counts |
|---------|----------------|
| `map.input.records` | records read by map tasks |
| `map.input.bytes` | the size of the input splits |
| `map.output.records` | records emitted by the mapper |
| `map.output.bytes` | bytes of map output, after the combiner |
| `combine.input.records`, `combine.output.records` | records into and out of the combiner |
| `reduce.input.records` | records read by reduce tasks |
| `reduce.output.records` | records emitted by the reducer |
| `skipped.records` | bad records skipped (#26); not counted by Python's local mode, which never retries |

`mr run -local` prints the totals when the job ends. `mr status` and
`mr submit -wait` print them from the coordinator.
