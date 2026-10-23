"""Runs a job in one process, one task at a time (§4.7), mirroring
go/internal/local.

Its output depends only on the job and its input, never on M: splits run in
input order, and equal keys keep that order through the sort. That makes it
the oracle the distributed runs are checked against.
"""

import glob
import os
import shutil
from collections import Counter
from collections.abc import Iterator
from contextlib import ExitStack, contextmanager
from typing import BinaryIO

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
from mapreduce_reloaded.split import plan

# Framework counters, reported alongside the user's.
MAP_INPUT_RECORDS = "map.input.records"
MAP_OUTPUT_RECORDS = "map.output.records"
REDUCE_OUTPUT_RECORDS = "reduce.output.records"

# Where reduce tasks write under the output before committing, as in Go.
TEMP_DIR = "_temporary"


def run(spec: JobSpec, work_dir: str) -> dict[str, int]:
    """Executes spec and returns its counters. Intermediate files go in
    work_dir, which the caller removes. Reduce task r writes
    <output>/part-r-<r>."""
    in_format = input_format(spec.input_format)
    out_format = output_format(spec.output_format)
    splits = plan(in_format, spec.inputs, spec.split_size)
    if not splits:
        raise ValueError("the input is empty")
    if glob.glob(os.path.join(glob.escape(spec.output), "part-r-*")):
        raise FileExistsError(f"{spec.output} already holds job output")
    os.makedirs(os.path.join(spec.output, TEMP_DIR), exist_ok=True)

    job = _Job(spec, work_dir)
    for i, split in enumerate(splits):
        with _task("map", i):
            job.run_map(i, in_format, split)
    for r in range(spec.r):
        with _task("reduce", r):
            job.run_reduce(r, len(splits), out_format)
    shutil.rmtree(os.path.join(spec.output, TEMP_DIR))
    return dict(job.counts)


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
        p, r = self.spec.program, self.spec.r
        with self._context("map", i) as ctx, ExitStack() as stack:
            combine_fn = combine.func(ctx, p.combiner) if p.combiner is not None else None
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
        """Reduces partition r of all m map outputs. The part file is written
        in TEMP_DIR and renamed into place, so a failed task leaves none."""
        name = f"part-r-{r:05d}"
        tmp = os.path.join(self.spec.output, TEMP_DIR, name)
        try:
            with self._context("reduce", r) as ctx, ExitStack() as stack:
                runs = [
                    kv.read(stack.enter_context(open(self._map_output(i, r), "rb")))
                    for i in range(m)
                ]
                w = stack.enter_context(out_format.create(tmp))
                # In map task order, so equal keys stay in input order.
                for key, values in groups(merge(*runs)):
                    for k, v in self.spec.program.reducer(ctx, key, values):
                        ctx.count(REDUCE_OUTPUT_RECORDS)
                        w.write(k, v)
        except BaseException:
            if os.path.exists(tmp):
                os.remove(tmp)
            raise
        os.replace(tmp, os.path.join(self.spec.output, name))

    def _map_output(self, i: int, r: int) -> str:
        return os.path.join(self.work_dir, f"map-{i:05d}-r-{r:05d}.kv")

    @contextmanager
    def _context(self, kind: str, index: int) -> Iterator[Context]:
        with _SideFiles(self.spec.output, f"{kind}-{index}") as side:
            yield Context(
                TaskID(kind, index),
                params=self.spec.params,
                on_count=self._count,
                on_side_output=side.open,
            )

    def _count(self, name: str, delta: int) -> None:
        self.counts[name] += delta


class _SideFiles:
    """One task's side outputs (§4.5). They are written in TEMP_DIR and
    renamed into the output if the task succeeds, or removed if it fails."""

    def __init__(self, output: str, task: str):
        self._output = output
        self._task = task
        self._files: dict[str, BinaryIO] = {}

    def open(self, name: str) -> BinaryIO:
        if not name or name != os.path.basename(name) or name.startswith((".", "_", "part-")):
            raise ValueError(f"bad side output name {name!r}")
        if name not in self._files:
            tmp = os.path.join(self._output, TEMP_DIR, f"{name}.{self._task}")
            self._files[name] = open(tmp, "wb")
        return self._files[name]

    def __enter__(self) -> "_SideFiles":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        for name, f in self._files.items():
            f.close()
            if exc_type is None:
                os.replace(f.name, os.path.join(self._output, name))
            else:
                os.remove(f.name)
