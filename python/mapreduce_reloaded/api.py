"""The public programming model, mirroring §2 of Dean & Ghemawat (OSDI 2004).

    map    (k1, v1)       -> list(k2, v2)
    reduce (k2, list(v2)) -> list(v2)

The paper's word count, in this API:

    @mapper
    def wc_map(ctx, offset, line):
        for word in line.split():
            yield word, b"1"

    @reducer
    def wc_reduce(ctx, word, counts):
        yield word, str(sum(int(c) for c in counts)).encode()

User functions may be generators, as above, or return an iterable of
(key, value) pairs, or None for no output. Keys and values are bytes; user
code chooses the encoding. The Go equivalent is go/pkg/api.
"""

import functools
import logging
from collections.abc import Callable, Iterable, Iterator, Mapping
from dataclasses import dataclass, field

from mapreduce_reloaded.partition import partition

Pair = tuple[bytes, bytes]
Partitioner = Callable[[bytes, int], int]


@dataclass(frozen=True)
class TaskID:
    """One attempt at one task. A task may be attempted more than once."""

    kind: str  # "map" or "reduce"
    index: int
    attempt: int = 0


class Context:
    """Gives user code access to its task and job. Built by the framework."""

    def __init__(
        self,
        task: TaskID,
        *,
        params: Mapping[str, str] | None = None,
        logger: logging.Logger | None = None,
        on_count: Callable[[str, int], None] | None = None,
    ):
        self.task = task
        self.params = dict(params or {})
        self.logger = logger or logging.getLogger("mapreduce_reloaded.user")
        self._on_count = on_count

    def count(self, name: str, delta: int = 1) -> None:
        """Adds delta to a named counter (§4.9)."""
        if self._on_count:
            self._on_count(name, delta)


class _UserFunction:
    """Wraps a user function so that calling it always yields checked pairs."""

    def __init__(self, fn: Callable[..., Iterable[Pair] | None]):
        functools.update_wrapper(self, fn)
        self._fn = fn

    def __call__(self, ctx: Context, key: bytes, arg) -> Iterator[Pair]:
        for k, v in self._fn(ctx, key, arg) or ():
            if not isinstance(k, bytes) or not isinstance(v, bytes):
                raise TypeError(
                    f"{self.__name__} output ({type(k).__name__}, {type(v).__name__}); "
                    "keys and values must be bytes"
                )
            yield k, v


class Mapper(_UserFunction):
    """fn(ctx, key, value) -> pairs. For text input, key is the line's offset."""


class Reducer(_UserFunction):
    """fn(ctx, key, values) -> pairs. values is an iterable, consumed once."""


mapper = Mapper
reducer = Reducer
# A combiner is a reducer applied to parts of one map task's output (§4.3).
# It may run zero, one or more times on any subset of a key's values, so it
# must be commutative and associative, emit only the key it was given, and
# leave the job's result unchanged: reducing its output must give what
# reducing its input would.
combiner = Reducer


@dataclass(frozen=True)
class Program:
    """A user's MapReduce code: what runs, as opposed to the data it runs on."""

    mapper: Mapper
    reducer: Reducer
    combiner: Reducer | None = None
    partitioner: Partitioner = partition


@dataclass(frozen=True)
class JobSpec:
    """A Program and the data to run it on. Raises ValueError if invalid.

    The in-process form of the wire message mapreduce.v1.JobSpec. M, the
    number of map tasks, follows from split_size (§3.1); 0 means 64 MiB.
    """

    program: Program
    inputs: tuple[str, ...]
    output: str
    r: int
    split_size: int = 0
    input_format: str = "text"
    output_format: str = "text"
    params: Mapping[str, str] = field(default_factory=dict)

    def __post_init__(self):
        problems = [
            msg
            for failed, msg in [
                (not isinstance(self.program.mapper, Mapper), "mapper must be a @mapper"),
                (not isinstance(self.program.reducer, Reducer), "reducer must be a @reducer"),
                (not self.inputs, "no inputs"),
                (not self.output, "no output"),
                (self.r < 1, "number of reduce tasks r must be at least 1"),
                (self.split_size < 0, "split_size must not be negative"),
            ]
            if failed
        ]
        if problems:
            raise ValueError("; ".join(problems))
