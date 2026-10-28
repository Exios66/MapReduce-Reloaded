"""Checks and converts the JobSpec and InputSplit messages a coordinator
handles, mirroring the parts of go/internal/wire and go/pkg/api's
JobSpec.Validate that do not need the job's code.

The Python coordinator never runs user code, so it cannot load a job file
or look up a Go program. It accepts a job that names a built-in program or
carries a job file; a job file that does not load fails its first attempt
as a fatal task failure, and with it the job (#198).
"""

import os
import re
from urllib.parse import quote, unquote, urlsplit

from mapreduce_reloaded.formats import Split
from mapreduce_reloaded.gen.mapreduce.v1 import types_pb2
from mapreduce_reloaded.split import MAX_SPLITS

# The programs compiled into the Go workers (go/examples).
PROGRAMS = frozenset(
    ["distsort", "grep", "invertedindex", "reverselinks", "termvector", "urlfreq", "wordcount"]
)

# The most reduce tasks a job may have, as go/pkg/api.MaxReduceTasks.
MAX_REDUCE_TASKS = 10_000

_INPUT_FORMATS = ("", "text", "kv")
_CODECS = ("", "none", "zstd", "snappy")


def validate(m: types_pb2.JobSpec, programs=PROGRAMS) -> list[str]:
    """Returns every problem with the job m describes, as
    go/pkg/api.JobSpec.Validate words them, or an empty list if it can
    run. programs names the built-in programs the workers have."""
    errs = []
    if not m.program_source and m.program not in programs:
        errs.append(f"wire: unknown program {_q(m.program)}")
    if m.partitioner not in ("", "hash"):
        errs.append(f"wire: unknown partitioner {_q(m.partitioner)}")
    for uri in [*m.inputs, m.output]:
        try:
            path(uri)
        except ValueError as e:
            errs.append(str(e))
    if not m.inputs:
        errs.append("no Inputs")
    if not m.output:
        errs.append("no Output")
    if m.num_reduce_tasks == 0:
        errs.append("number of reduce tasks R must be at least 1")
    if m.num_reduce_tasks > MAX_REDUCE_TASKS:
        errs.append(
            f"number of reduce tasks R is {m.num_reduce_tasks}; the most is {MAX_REDUCE_TASKS}"
        )
    if m.num_map_tasks > MAX_SPLITS:
        errs.append(f"number of map tasks M is {m.num_map_tasks}; the most is {MAX_SPLITS}")
    if m.input_format not in _INPUT_FORMATS:
        errs.append(f"formats: unknown format {_q(m.input_format)}")
    if m.intermediate_codec not in _CODECS:
        errs.append(f"intermediate codec: mrkv: unsupported_codec: {_q(m.intermediate_codec)}")
    if m.output_codec not in _CODECS:
        errs.append(f"output codec: mrkv: unsupported_codec: {_q(m.output_codec)}")
    elif m.output_format not in _INPUT_FORMATS:
        errs.append(f"formats: unknown format {_q(m.output_format)}")
    elif m.output_codec not in ("", "none") and m.output_format != "kv":
        name = m.output_format or "text"
        errs.append(f"formats: output format {_q(name)} takes no codec; only kv does")
    return errs


def path(uri: str) -> str:
    """Returns the local path of a file:// URI, which must name no host but
    localhost. A string with no scheme is already a path. The Python
    coordinator stores nothing else, so any other scheme is an error."""
    _parse(uri)
    u = urlsplit(uri)
    if u.scheme == "":
        return uri
    if u.scheme != "file":
        raise ValueError(f"wire: {_q(uri)}: the Python coordinator stores only local files")
    if u.netloc not in ("", "localhost"):
        raise ValueError(f"wire: {_q(uri)} names a remote host")
    return unquote(u.path)


def uri(p: str) -> str:
    """Returns the file:// URI of path p, made absolute."""
    return "file://" + quote(os.path.abspath(p))


def input_split(s: Split) -> types_pb2.InputSplit:
    """Returns the wire form of s, naming its file by absolute URI."""
    return types_pb2.InputSplit(uri=uri(s.path), offset=s.start, length=s.end - s.start)


_CONTROL = re.compile("[\x00-\x1f\x7f]")
_BAD_ESCAPE = re.compile("%(?![0-9A-Fa-f]{2})")


def _parse(uri: str):
    """Rejects what Go's url.Parse rejects in a URI or a path: control
    characters, and a % not followed by two hex digits."""
    if _CONTROL.search(uri):
        raise ValueError(f"wire: parse {_q(uri)}: net/url: invalid control character in URL")
    if m := _BAD_ESCAPE.search(uri):
        bad = uri[m.start() : m.start() + 3]
        raise ValueError(f"wire: parse {_q(uri)}: invalid URL escape {_q(bad)}")


_ESCAPES = {
    "\a": r"\a",
    "\b": r"\b",
    "\f": r"\f",
    "\n": r"\n",
    "\r": r"\r",
    "\t": r"\t",
    "\v": r"\v",
    "\\": "\\\\",
    '"': '\\"',
}


def _q(s: str) -> str:
    """Quotes s as Go's %q does: backslash escapes for quotes, backslashes
    and control characters, and \x, \u or \U for other unprintable ones."""
    out = []
    for c in s:
        if c in _ESCAPES:
            out.append(_ESCAPES[c])
        elif c.isprintable():
            out.append(c)
        elif ord(c) < 0x80:
            out.append(f"\\x{ord(c):02x}")
        elif ord(c) <= 0xFFFF:
            out.append(f"\\u{ord(c):04x}")
        else:
            out.append(f"\\U{ord(c):08x}")
    return '"' + "".join(out) + '"'
