"""The default partition function: fnv1a64(key) mod R.

Specified in docs/file-format.md ("Partitioning"). It must give the same
answer as the Go implementation in go/internal/partition, so the hash is
written out here. Python's built-in hash() cannot be used: it is randomly
seeded per process.
"""

_OFFSET = 0xCBF29CE484222325  # FNV-1a 64 offset basis
_PRIME = 0x100000001B3  # FNV 64 prime
_MASK = (1 << 64) - 1  # Python ints don't wrap; mask to emulate uint64


def fnv1a64(key: bytes | bytearray | memoryview) -> int:
    """Returns the 64-bit FNV-1a hash of key's raw bytes.

    Raises TypeError for str: keys are bytes, and the caller chooses the
    encoding.
    """
    if not isinstance(key, bytes | bytearray | memoryview):
        raise TypeError(f"key must be bytes-like, not {type(key).__name__}")
    h = _OFFSET
    for b in bytes(key):
        h = ((h ^ b) * _PRIME) & _MASK
    return h


def partition(key: bytes | bytearray | memoryview, r: int) -> int:
    """Returns the reduce task, in 0..r-1, that receives key.

    r is the job's number of reduce tasks and must be at least 1.
    """
    if r < 1:
        raise ValueError(f"number of reduce tasks must be at least 1, got {r}")
    return fnv1a64(key) % r
