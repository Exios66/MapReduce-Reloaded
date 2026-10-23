"""Runs a job in one process, one task at a time (§4.7), mirroring
go/internal/local.

Its output depends only on the job and its input, never on M: splits run in
input order, and equal keys keep that order through the sort. That makes it
the oracle the distributed runs are checked against.
"""

import glob
import os
from collections import Counter
from contextlib import ExitStack, contextmanager

from mapreduce_reloaded import combine
from mapreduce_reloaded.api import Context, JobSpec, TaskID
from mapreduce_reloaded.extsort import DEFAULT_BUDGET, Sorter, groups, merge
from mapreduce_reloaded.formats import (
    InputFormat,
    OutputFormat,
    Split,
    input_format,
    kv,
    output_format,
)

DEFAULT_SPLIT_SIZE = 64 << 20

# Framework counters, reported alongside the user's.
MAP_INPUT_RECORDS = "map.input.records"
MAP_OUTPUT_RECORDS = "map.output.records"
REDUCE_OUTPUT_RECORDS = "reduce.output.records"


def run(spec: JobSpec, work_dir: str) -> dict[str, int]:
    """Executes spec and returns its counters. Intermediate files go in
    work_dir, which the caller removes. Reduce task r writes
    <output>/part-r-<r>."""
    in_format = input_format(spec.input_format)
    out_format = output_format(spec.output_format)
    split_size = spec.split_size or DEFAULT_SPLIT_SIZE
    splits = [s for path in expand(spec.inputs) for s in in_format.splits(path, split_size)]
    os.makedirs(spec.output, exist_ok=True)

    job = _Job(spec, work_dir)
    for i, split in enumerate(splits):
        with _task("map", i):
            job.run_map(i, in_format, split)
    for r in range(spec.r):
        with _task("reduce", r):
            job.run_reduce(r, len(splits), out_format)
    return dict(job.counts)


def split_size(inputs: list[str], m: int) -> int:
    """Returns the split size that cuts the input into about m splits."""
    total = sum(os.path.getsize(path) for path in expand(inputs))
    return max(-(-total // m), 1)


def expand(patterns) -> list[str]:
    """Resolves input patterns to files, in sorted order per pattern."""
    files = []
    for pattern in patterns:
        matches = sorted(glob.glob(pattern))
        if not matches:
            raise FileNotFoundError(f"no input matches {pattern!r}")
        files += matches
    return files


@contextmanager
def _task(kind: str, index: int):
    """Notes the failing task on any exception raised inside it."""
    try:
        yield
    except Exception as e:
        e.add_note(f"in {kind} task {index}")
        raise


class _Job:
    def __init__(self, spec: JobSpec, work_dir: str):
        self.spec = spec
        self.work_dir = work_dir
        self.counts: Counter[str] = Counter()

    def run_map(self, i: int, in_format: InputFormat, split: Split) -> None:
        """Maps one split and writes one sorted, combined file per reduce task."""
        ctx = self._context("map", i)
        p, r = self.spec.program, self.spec.r
        combine_fn = combine.func(ctx, p.combiner) if p.combiner else None
        with ExitStack() as stack:
            sorters = [
                stack.enter_context(Sorter(self.work_dir, DEFAULT_BUDGET // r, combine_fn))
                for _ in range(r)
            ]
            for key, value in in_format.read(split):
                ctx.count(MAP_INPUT_RECORDS)
                for k, v in p.mapper(ctx, key, value):
                    ctx.count(MAP_OUTPUT_RECORDS)
                    sorters[p.partitioner(k, r)].add(k, v)
            for part, sorter in enumerate(sorters):
                with open(self._map_output(i, part), "wb") as f, kv.Writer(f) as w:
                    for k, v in sorter.sorted():
                        w.write(k, v)

    def run_reduce(self, r: int, m: int, out_format: OutputFormat) -> None:
        """Reduces partition r of all m map outputs."""
        ctx = self._context("reduce", r)
        with ExitStack() as stack:
            runs = [
                kv.read(stack.enter_context(open(self._map_output(i, r), "rb"))) for i in range(m)
            ]
            out = os.path.join(self.spec.output, f"part-r-{r:05d}")
            w = stack.enter_context(out_format.create(out))
            # In map task order, so equal keys stay in input order.
            for key, values in groups(merge(*runs)):
                for k, v in self.spec.program.reducer(ctx, key, values):
                    ctx.count(REDUCE_OUTPUT_RECORDS)
                    w.write(k, v)

    def _map_output(self, i: int, r: int) -> str:
        return os.path.join(self.work_dir, f"map-{i:05d}-r-{r:05d}.kv")

    def _context(self, kind: str, index: int) -> Context:
        return Context(TaskID(kind, index), params=self.spec.params, on_count=self._count)

    def _count(self, name: str, delta: int) -> None:
        self.counts[name] += delta
