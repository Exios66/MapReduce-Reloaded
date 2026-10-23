"""The paper's distributed grep (§2.3), mirroring go/examples/grep.

Outputs each input line that contains the job's "pattern" parameter, a
literal string, so the Go and Python versions agree.
"""

from mapreduce_reloaded.api import Program, mapper, reducer


@mapper
def grep_map(ctx, offset, line):
    pattern = ctx.params.get("pattern", "")
    if not pattern:
        raise ValueError("grep: no pattern; set one with --param pattern=<text>")
    if pattern.encode() in line:
        yield line, b""


@reducer
def identity(ctx, key, values):
    for value in values:
        yield key, value


# The reducer is the identity, so a line that occurs twice is output twice.
program = Program(mapper=grep_map, reducer=identity)
