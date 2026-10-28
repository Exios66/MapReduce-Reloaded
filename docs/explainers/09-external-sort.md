# Explainer — External Sort and Grouping (#10)

## What it is for

A reducer needs all values for a key together, and keys in order (§4.2).
That means sorting the map output, which can be bigger than memory. The
paper: "if the amount of intermediate data is too large to fit in memory,
an external sort is used" (§3.1).

## How it works

1. **Buffer** records until a memory budget is reached: 64 MiB per map task by default, shared
   evenly among its R sorters.
2. **Spill:** sort the buffer and write it to disk as a *run*, an MRKV file
   (#8). Repeat until all records are in.
3. **Merge** the runs. Each run is already sorted, so only the smallest
   front record of each run needs comparing, kept in a heap. Memory is one
   block per run, however much data there is.
4. **Group:** walk the merged stream and hand the reducer each key with an
   iterator over its values.

If everything fits in the budget, steps 2–3 are skipped and nothing touches
the disk.

## Details that matter

- **Stable:** records with the same key come out in the order they went
  in, even across runs. When keys tie, the merge takes from the earlier
  run. This keeps the local runner (#12) deterministic, which is what makes
  it usable as an oracle.
- **Values are streamed:** a key with a million values never needs a
  million values in memory.
- **A reducer may stop early.** Values it doesn't read are skipped before
  the next key. The tests run reducers that read all, one or none of their
  values and check that every key still appears once, in order.

| | Go (`go/internal/extsort`) | Python (`mapreduce_reloaded.extsort`) |
|---|---|---|
| Sort | `Sorter` with `Add`, `Sorted`, `Close` | `Sorter` with `add`, `sorted`, `close` |
| Merge | `container/heap`, ties by run index | `heapq.merge` (stable) |
| Group | `Groups`, an `iter.Seq2` | `groups`, built on `itertools.groupby` |

## Proof the tests can fail

Reversing the tie-break, or skipping unread values incorrectly, fails the
tests. The sort is checked against a plain in-memory stable sort at budgets
from 1 byte (one run per record) to 64 MB (no runs).
