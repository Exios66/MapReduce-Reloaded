#!/usr/bin/env python3
"""Drops files from the page cache: python3 drop-cache.py PATH...

E0 runs it in a container that mounts the output volume, before each run,
on the inputs of the programs that are not running, so a run starts with
only its own input cached (#347). posix_fadvise(POSIX_FADV_DONTNEED) needs
no root, unlike writing to /proc/sys/vm/drop_caches; it drops only clean
pages, and the inputs are never written after they are generated.
"""

import os
import sys


def main(argv: list[str]) -> int:
    if not argv:
        print("usage: drop-cache.py PATH...", file=sys.stderr)
        return 2
    for path in argv:
        try:
            fd = os.open(path, os.O_RDONLY)
        except OSError as err:
            print(f"drop-cache: {path}: {err.strerror}", file=sys.stderr)
            return 1
        try:
            os.posix_fadvise(fd, 0, 0, os.POSIX_FADV_DONTNEED)
        finally:
            os.close(fd)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
