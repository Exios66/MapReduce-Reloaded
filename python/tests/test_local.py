"""Tests for local mode and the command line, mirroring go/internal/local
and go/cmd/mr. The expected files come from scripts/make-corpus-expected.py."""

import dataclasses
from pathlib import Path

import pytest

from mapreduce_reloaded import local, split
from mapreduce_reloaded.__main__ import load, main
from mapreduce_reloaded.api import JobSpec, Program, mapper, reducer
from mapreduce_reloaded.examples.wordcount import program

CORPUS = Path(__file__).resolve().parents[2] / "testdata" / "corpus"
INPUTS = (str(CORPUS / "*.txt"),)
RAVEN = str(CORPUS / "raven.txt")


def assert_same_files(want_dir: Path, got_dir: Path) -> None:
    want = sorted(p.name for p in want_dir.iterdir())
    assert sorted(p.name for p in got_dir.iterdir()) == want
    for name in want:
        assert (got_dir / name).read_bytes() == (want_dir / name).read_bytes(), name


@pytest.mark.parametrize("m", [1, 3, 7])
def test_wordcount_golden(tmp_path, m):
    spec = JobSpec(
        program=program,
        inputs=INPUTS,
        output=str(tmp_path / "out"),
        r=4,
        split_size=split.size_for(INPUTS, m),
    )
    counts = local.run(spec, str(tmp_path))
    assert_same_files(CORPUS / "expected", tmp_path / "out")
    assert counts[local.MAP_OUTPUT_RECORDS] == 59372
    assert counts[local.REDUCE_OUTPUT_RECORDS] == 11786


def test_without_combiner(tmp_path):
    no_combiner = dataclasses.replace(program, combiner=None)
    spec = JobSpec(program=no_combiner, inputs=INPUTS, output=str(tmp_path / "out"), r=4)
    local.run(spec, str(tmp_path))
    assert_same_files(CORPUS / "expected", tmp_path / "out")


def test_value_order_independent_of_m(tmp_path):
    """A key's values reach the reducer in input order, whatever M is. The
    reducer concatenates them, so any reordering changes the output."""

    @mapper
    def first_byte(ctx, offset, line):
        if line:
            yield line[:1], offset

    @reducer
    def join(ctx, key, values):
        yield key, b" ".join(values)

    outputs = []
    for m in [1, 3, 7]:
        out = tmp_path / f"m{m}"
        spec = JobSpec(
            program=Program(mapper=first_byte, reducer=join),
            inputs=INPUTS,
            output=str(out),
            r=2,
            split_size=split.size_for(INPUTS, m),
        )
        local.run(spec, str(tmp_path))
        outputs.append(out)
    assert_same_files(outputs[0], outputs[1])
    assert_same_files(outputs[0], outputs[2])


def test_no_input(tmp_path):
    spec = JobSpec(program=program, inputs=(str(tmp_path / "*.txt"),), output=str(tmp_path), r=1)
    with pytest.raises(FileNotFoundError, match="no input matches"):
        local.run(spec, str(tmp_path))


def test_error_names_task(tmp_path):
    @mapper
    def fail(ctx, key, value):
        raise RuntimeError("boom")

    spec = JobSpec(
        program=dataclasses.replace(program, mapper=fail),
        inputs=(RAVEN,),
        output=str(tmp_path / "out"),
        r=1,
    )
    with pytest.raises(RuntimeError, match="boom") as e:
        local.run(spec, str(tmp_path))
    assert e.value.__notes__ == ["in map task 0"]


def test_cli(tmp_path, capsys):
    out = tmp_path / "out"
    main(["run", "--local", "-m", "2", "-r", "3", "wordcount", str(out), RAVEN])
    assert len(list(out.glob("part-r-*"))) == 3
    assert "map.input.records\t" in capsys.readouterr().err


@pytest.mark.parametrize(
    "args, want",
    [
        (["run", "wordcount", "out", RAVEN], "only --local"),
        (["run", "--local", "nope", "out", RAVEN], "No module named"),
        (["run", "--local", "wordcount", "out", "missing"], "no input matches"),
        (["run", "--local", "-r", "0", "wordcount", "out", RAVEN], "at least 1"),
    ],
)
def test_cli_errors(capsys, args, want):
    with pytest.raises(SystemExit):
        main(args)
    assert want in capsys.readouterr().err


def test_load():
    assert load("wordcount") is program
    assert load("mapreduce_reloaded.examples.wordcount:program") is program
    with pytest.raises(TypeError, match="not a Program"):
        load("mapreduce_reloaded.examples.wordcount:wc_map")
