# Explainer — Input Splits (#14)

## What a split is

A split is a byte range of one input file: the work of one map task
(§3.1). A job's M is simply its number of splits.

`split.Plan` (Go) and `split.plan` (Python) expand the job's file names and
globs, then ask the input format to cut each file into pieces of about
`SplitSize` bytes (64 MiB by default; the paper uses 16 to 64 MB). Local
mode and the coordinator (#16) both use it, so both cut the same input the
same way.

## Record boundaries

Splits are plain byte ranges; they ignore lines. The reader handles the
boundary instead: a line belongs to the split it starts in (see
[08-formats.md](08-formats.md)). So planning stays cheap: it only needs
each file's size, never its contents.

## Choosing M and R

- **M:** set by the split size. `SizeFor(inputs, m)` returns the size that
  gives about m splits, which is what `mr run -m` uses.
- **R:** the paper makes R "a small multiple of the number of worker
  machines" (§3.5). `DefaultR(workers)` is 2 per worker. The coordinator
  applies it to a job submitted without R, since it knows how many workers
  there are; local mode needs R set explicitly.

## How we know it works

A property test, in both languages, builds random sets of files and split
sizes and checks that the splits cover every file exactly: in order,
starting at 0, each starting where the last ended, none empty or larger
than the split size, and nothing left over. Making splits one byte too
large fails it.

## Likely review questions

- **Where are the location hints?** The wire message `InputSplit` already
  has a `locations` field. Nothing can fill or use it until locality
  scheduling (#31), so the in-process split type does not carry it yet.
- **Why does a KV file give one split?** MRKV blocks are not
  self-synchronising from an arbitrary byte, so a reader cannot start in
  the middle of a file.
