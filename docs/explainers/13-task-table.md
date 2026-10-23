# Explainer — The Coordinator's Task Table (#15)

## What the master remembers

§3.2: for every task, the master stores its state (idle, in progress or
completed) and which worker has it; for every completed map task, where its
R output files are and how big they are. `TaskTable` holds exactly that,
and `Registry` holds the live workers.

## The state machine

```
IDLE --Assign--> IN_PROGRESS --Complete--> COMPLETED
  ^                   |                        |
  +--- Fail, or worker lost -------------------+ (map tasks only)
```

- **Next** hands out map tasks first. Reduce tasks are offered only once
  every map task has completed, because each reducer needs a piece of every
  map output (§3.1).
- **Assign** starts a numbered attempt. Attempt numbers only grow, so a
  report from an old attempt can always be told apart.
- **Complete** accepts the first attempt to finish. Later reports, and
  reports from attempts that were stopped, return "not accepted" (§3.3).
- **ResetWorker** handles a lost worker. Its running tasks go back to idle.
  So do its *completed map* tasks: their output was on its local disk, which
  is now unreachable. Its completed reduce tasks stay done, because their
  output is in the global file system (§3.3).

## Workers

Each worker registers and gets a fresh ID. A worker that stops sending
heartbeats is expired. If it comes back, it registers again under a new ID,
so nothing it reports under the old ID is trusted.

## Concurrency

The table and registry do no locking; the coordinator guards all of its
state with one mutex ([ADR 0006](../design/0006-coordinator-concurrency.md)).

## How we know it works

- Unit tests for each transition: the full lifecycle, first completion
  wins, failure and retry, losing a worker, and bad input.
- A property test runs 200 random sequences of operations and checks after
  every step that:
  - the progress counters match the task states;
  - exactly the completed map tasks have output locations;
  - a task is in progress exactly when an attempt is running;
  - attempt numbers only grow;
  - no reduce task is offered before the maps complete;
  - a lost worker never undoes a completed reduce.
