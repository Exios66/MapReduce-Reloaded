---
name: checking-campaign-status
description: Use when asked how a running MapReduce-Reloaded benchmark campaign or experiment chain is going, whether it is stuck, how far it has got, or whether the experiment host has disk space left.
---

# Checking campaign status

One read-only snapshot of the experiment host; it starts, stops and deletes nothing.

```bash
bash .claude/skills/checking-campaign-status/status.sh HOST [REPO] [RUNS]
```

`HOST` is an ssh alias, or `-` for this machine. `REPO` is the clone the experiments run in (default `~/mr-exp`); `RUNS` holds a gated chain's `logs/chain.log`, per-chunk logs and `<commit>/*.tar.gz` archives (default `~/runs`).

## Reading it

| Output | Meaning |
|---|---|
| `GATE PASS NAME` | the chunk's runs were all valid, at the expected commit, and archived |
| `FAIL` or `ABORT` | the chain stopped: read the chunk's log tail before anything else |
| runs started not increasing between two snapshots several minutes apart | possibly stuck: check the chunk's log and `docker ps` |
| free on `/` under 6 GB | the gate will fail; under 5 GB `bench/experiments.sh` stops itself |
| `bench/results/*` counts from hours ago | earlier runs, not the current chain |

## Common mistakes

- Polling every few seconds: a 1 GiB run takes 2 to 400 s; check every few minutes.
- Freeing disk by deleting other projects' containers, images or volumes: only regenerable caches you own (unused Docker build cache, your own temp files).
- Running heavy work (`make check`, plotting) on the host while a chain runs: it perturbs the measurements.
