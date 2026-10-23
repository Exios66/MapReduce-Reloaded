# Explainer — Side Outputs (#28)

Some jobs write more than their key/value output: an index, a sample, a
report. The paper leaves those files to the user. It asks that each be
written under a temporary name and renamed once complete (§4.5). Here,
`ctx.SideOutput(name)` (Python: `ctx.side_output(name)`) does that for you.

## What happens

| Step | Where the file is |
|------|-------------------|
| The task writes it | `<output>/_temporary/<name>.<task>.attempt-<n>` |
| The attempt fails | removed |
| The attempt succeeds | renamed to `<output>/<name>` |

A reduce task commits its side files just before its part file, so a
visible `part-r-*` means its side files are in place too. Map tasks commit
theirs when they finish. Readers skip names starting with `.` or `_`, so
names like those are refused, and so is `part-`, which belongs to the
framework.

## What is not promised

As in the paper, there is no atomicity across tasks or files:

- If two attempts of one task both succeed (a re-run map task, or a
  duplicate attempt), each renames its file into place, and the later one
  wins. That is only safe if every attempt writes the same bytes, which is
  why user code must be deterministic.
- Two tasks that pick the same name overwrite each other. Put
  `ctx.Task().Index` in the name.
- A task with several side files can fail between two renames, leaving
  some in place. Its retry writes them all again.
