# ADR 0013 — Committing output on object storage

**Status:** accepted · **Phase:** 6 · **Issue:** #41

## Context

The paper commits a reduce task's output with an atomic rename in GFS: the
task writes a private temporary file, then renames it to its final name
(§3.3). Duplicate attempts of a deterministic task then leave one final
file, written whole. The local backend does the same with `os.Rename`.

An S3-compatible store has no rename and no directories. A single `PUT`
or `CopyObject` is atomic, though: readers see the old object or all of the
new one, never part. AWS S3 and the stores this project tests against are
strongly consistent, so a listing shows every object written before it.

Locally, a late attempt is kept out by the file system: the job's
`_temporary` directory is gone after `Cleanup`, and `os.Create` fails in a
missing directory (`task.TempDir`).

## Decision

1. **Commit is a server-side copy.** An attempt writes
   `_temporary/<name>.attempt-<A>` with one `PUT` when it closes the file,
   then `Commit` copies it to `<name>` and deletes the temporary object.
   The copy is the commit: if the delete fails, the attempt still
   succeeds, and `Cleanup` removes the object with the rest of
   `_temporary/`.
2. **Directories are marker objects.** `MkdirAll(dir)` writes an empty
   object `dir/`, and one for each parent, and `Create` fails with `fs.ErrNotExist` unless the
   file's directory has one. `Setup` therefore creates `_temporary/`, and
   `Cleanup` deletes everything under it, marker included. A late attempt
   then fails at `Create`, as it does locally.
3. **The rest of the protocol is unchanged.** Side files commit before the
   part file, `CheckOutput` lists `part-r-*`, and the task code does not
   know which backend it writes to.

## Alternatives rejected

- **Multipart upload straight to the final key.** The object would appear
  only when the upload completes, with no copy. But a late attempt could
  not be stopped, and a dead worker's unfinished uploads need a lifecycle
  rule to be removed.
- **A `_SUCCESS` marker or a manifest that readers trust.** This works on
  any store, but every reader must then know to look for it. The final
  objects here are complete, so plain listings are already correct.
- **Writing to the final key directly.** Duplicate attempts produce the same
  bytes, so readers would not see wrong data. But a failed attempt could
  leave a partial object under a final name.

## Consequences

- A commit costs a copy inside the store: the output is written twice
  there, but crosses the network once.
- One `PUT` and one `CopyObject` each take at most 5 GB, so a part file or
  side file is limited to 5 GB. A larger file would need multipart uploads
  and `UploadPartCopy`.
- `Create` costs one `HEAD` for the marker. The check is not atomic with
  the upload: an attempt that checks just before `Cleanup` and uploads just
  after leaves one object under `_temporary/`. Nothing reads it, but it
  stays until the output is deleted. A final object written that late has
  the same content as the one already there.
- Input written by other tools needs no markers: only `Create` checks them.
