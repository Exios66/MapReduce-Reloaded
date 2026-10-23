"""Runs MapReduce jobs, mirroring go/cmd/mr:

    python -m mapreduce_reloaded run --local [-m M] [-r R] PROGRAM OUTPUT_DIR INPUT...

PROGRAM is an example name, such as wordcount, or module:attribute naming a
Program. Inputs may be files or glob patterns. Counters are printed to
standard error. Only local mode exists so far (§4.7).
"""

import argparse
import importlib
import sys
import tempfile

from mapreduce_reloaded import local, split
from mapreduce_reloaded.api import JobSpec, Program


def main(argv: list[str] | None = None) -> None:
    parser = argparse.ArgumentParser(prog="python -m mapreduce_reloaded")
    commands = parser.add_subparsers(dest="command", required=True)
    run = commands.add_parser("run", help="run a job")
    run.add_argument("--local", action="store_true", help="run every task in this process")
    run.add_argument("-m", type=int, default=0, help="approximate number of map tasks")
    run.add_argument("-r", type=int, default=1, help="number of reduce tasks")
    run.add_argument("program")
    run.add_argument("output")
    run.add_argument("inputs", nargs="+")
    args = parser.parse_args(argv)

    if not args.local:
        parser.error("only --local is supported so far")
    try:
        inputs = split.expand(args.inputs)
        spec = JobSpec(
            program=load(args.program),
            inputs=tuple(inputs),
            output=args.output,
            r=args.r,
            split_size=split.size_for(inputs, args.m) if args.m > 0 else 0,
        )
    except (ImportError, AttributeError, TypeError, ValueError, OSError) as e:
        parser.error(str(e))
    with tempfile.TemporaryDirectory(prefix="mr-") as work:
        counts = local.run(spec, work)
    for name in sorted(counts):
        print(f"{name}\t{counts[name]}", file=sys.stderr)


def load(name: str) -> Program:
    """Returns the Program named by an example name or module:attribute."""
    module, _, attr = name.partition(":")
    if not attr:
        module, attr = f"mapreduce_reloaded.examples.{name}", "program"
    program = getattr(importlib.import_module(module), attr)
    if not isinstance(program, Program):
        raise TypeError(f"{name} is a {type(program).__name__}, not a Program")
    return program


if __name__ == "__main__":
    main()
