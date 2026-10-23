"""The paper's distributed sort (§5.3), mirroring go/examples/distsort.

The output is the input's lines in byte order across all R output files, so
concatenating them in order gives the whole input sorted. The partitioner
splits the key space into R ranges by a key's first byte.
"""

from mapreduce_reloaded.api import Program, mapper
from mapreduce_reloaded.examples.grep import identity


@mapper
def sort_map(ctx, offset, line):
    yield line, b""


def range_partition(key: bytes, r: int) -> int:
    """Sends a key to the range of r that holds its first byte."""
    return key[0] * r // 256 if key else 0


program = Program(mapper=sort_map, reducer=identity, partitioner=range_partition)
