# Showcase worker matching

Task 3's `Frame.Workers[].Host` needs to match a metrics target to a worker in
`/status.json`. This note records how.

## Evidence

The Docker daemon was not running when this was decided, so the live
comparison (`getent hosts worker`, reverse lookups, `/status.json` from a
container) was not run. These facts come from the code:

- `go/cmd/worker/main.go` sets the worker's `host` to `os.Hostname()`. In a
  Compose container that is the container ID, and it is the `host` that
  `/status.json` lists.
- DNS discovery of `worker` returns IP addresses, so a scrape target is an
  address, not a hostname.
- Reverse DNS of those addresses is not guaranteed to return the container ID,
  so option (a) cannot be relied on.

## Decision

Option (b). Each Go worker serves `mr_worker_info{host="<hostname>"} 1` on
`/metrics`, from the same `host` it registers with. The sampler reads `host`
from the scrape, so the match is exact whatever the address.

The Python implementation has no worker yet, only a coordinator, so there is
no Python `/metrics` to change. A Python worker must serve the same line.

Task 3 needs no change: `ParseMetrics` already returns the `host`.
