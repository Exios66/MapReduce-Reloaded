"""scripts/ci-params.py: which CI jobs a branch's changes run (docs/ci.md, ADR 0020)."""

import importlib.util
import json
import re
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
_spec = importlib.util.spec_from_file_location("ci_params", ROOT / "scripts/ci-params.py")
ci_params = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(ci_params)
MAPPING = ci_params.load_mapping((ROOT / ".circleci/mapping.txt").read_text())


def params(*paths, status="M"):
    return ci_params.select([(status, p) for p in paths], MAPPING)[0]


def test_every_tracked_file_is_mapped():
    tracked = subprocess.run(
        ["git", "ls-files", "-z"], cwd=ROOT, capture_output=True, text=True, check=True
    ).stdout.split("\0")
    _, unmapped = ci_params.select([("M", f) for f in tracked if f], MAPPING)
    assert unmapped == []


def test_the_mapping_names_only_declared_parameters():
    cont = (ROOT / ".circleci/continue-config.yml").read_text()
    declared = re.findall(r"^  (run-[a-z]+):$", cont, re.M)
    assert sorted(ci_params.PARAMETERS) == sorted(declared)
    assert {param for _, param in MAPPING} == set(ci_params.PARAMETERS)


@pytest.mark.parametrize(
    "line",
    ["^go/ run-go true", "^go/", "^go/ run-everything"],
)
def test_a_malformed_line_is_an_error(line):
    with pytest.raises(ValueError, match="want `<regex> <parameter>`"):
        ci_params.load_mapping(line)


def test_comments_and_blank_lines_are_ignored():
    assert ci_params.load_mapping("# a comment\n\n^go/ run-go\n")[0][1] == "run-go"


@pytest.mark.parametrize(
    "path, want",
    [
        ("docs/runbook.md", {"run-lint"}),
        ("LICENSE", {"run-lint"}),
        ("go/internal/worker/worker.go", {"run-go"}),
        ("python/mapreduce_reloaded/local.py", {"run-python", "run-go"}),
        ("proto/mapreduce/v1/types.proto", {"run-proto"}),
        ("go/go.mod", {"run-all", "run-go"}),
        ("scripts/e2e.sh", {"run-all", "run-go"}),
    ],
)
def test_a_change_runs_its_own_job(path, want):
    assert params(path) == want


# Each file here is read by a job outside its own directory: found by tracing
# every file the Go and Python test suites open, and by reading what the
# proto and lint jobs check. A change to one must run that job.
@pytest.mark.parametrize(
    "path, job",
    [
        ("go/gen/mapreduce/v1/types.pb.go", "run-proto"),
        ("python/mapreduce_reloaded/gen/mapreduce/v1/types_pb2.py", "run-proto"),
        ("buf.yaml", "run-proto"),
        ("testdata/corpus/alice.txt", "run-go"),
        ("testdata/corpus/alice.txt", "run-python"),
        ("testdata/format/vectors.json", "run-python"),
        ("README.md", "run-go"),
        ("examples/wordcount.go", "run-go"),
        ("bench/jobs/wordcount-naive.go", "run-go"),
        ("deploy/compose/monitoring/grafana/dashboards/mapreduce.json", "run-go"),
        ("docs/paper-mapping.md", "run-go"),
        (".githooks/pre-push", "run-go"),
        (".coderabbit.yaml", "run-python"),
        (".codeant/configuration.json", "run-python"),
        (".github/workflows/ci.yml", "run-python"),
        ("bench/run.sh", "run-python"),
        ("bench/experiments.sh", "run-python"),
        ("examples/demo.ipynb", "run-python"),
        ("docs/README.md", "run-python"),
        (".gitignore", "run-all"),
    ],
)
def test_a_cross_directory_input_runs_the_job_that_reads_it(path, job):
    assert job in params(path)


def test_a_deleted_file_runs_everything():
    # Other directories may name it: the paper-mapping table, a Markdown link.
    assert "run-all" in params("docs/evaluation.md", status="D")


def test_an_unmapped_file_runs_everything_and_is_reported():
    got, unmapped = ci_params.select([("A", "newarea/file.txt")], MAPPING)
    assert got == {"run-all"}
    assert unmapped == ["newarea/file.txt"]


def git(repo, *args):
    subprocess.run(
        ["git", "-c", "user.name=t", "-c", "user.email=t@t", *args],
        cwd=repo,
        check=True,
        capture_output=True,
    )


@pytest.fixture
def repo(tmp_path, monkeypatch):
    """A repository whose origin/main is its first commit."""
    git(tmp_path, "init", "-q")
    (tmp_path / "go").mkdir()
    (tmp_path / "go" / "x_test.go").write_text("".join(f"line {i}\n" for i in range(50)))
    git(tmp_path, "add", ".")
    git(tmp_path, "commit", "-qm", "base")
    git(tmp_path, "update-ref", "refs/remotes/origin/main", "HEAD")
    monkeypatch.chdir(tmp_path)
    return tmp_path


def test_a_moved_file_is_seen_at_both_paths(repo):
    # git's default rename detection would list only docs/x.txt, and the go
    # job, whose file left, would be skipped.
    (repo / "docs").mkdir()
    git(repo, "mv", "go/x_test.go", "docs/x.txt")
    git(repo, "commit", "-qm", "move")
    changes = ci_params.changed_files()
    assert sorted(changes) == [("A", "docs/x.txt"), ("D", "go/x_test.go")]
    assert {"run-go", "run-all"} <= ci_params.select(changes, MAPPING)[0]


def test_a_non_ascii_path_is_not_quoted(repo):
    (repo / "docs").mkdir()
    (repo / "docs" / "café.md").write_text("x\n")
    git(repo, "add", ".")
    git(repo, "commit", "-qm", "accent")
    assert ci_params.changed_files() == [("A", "docs/café.md")]


def test_the_diff_starts_at_the_merge_base(repo):
    # main moving on does not put main's own changes in the branch's diff.
    git(repo, "switch", "-qc", "topic")
    (repo / "notes.md").write_text("x\n")
    git(repo, "add", ".")
    git(repo, "commit", "-qm", "topic")
    git(repo, "switch", "-q", "--detach", "refs/remotes/origin/main")
    (repo / "go" / "y.go").write_text("package x\n")
    git(repo, "add", ".")
    git(repo, "commit", "-qm", "main moves")
    git(repo, "update-ref", "refs/remotes/origin/main", "HEAD")
    git(repo, "switch", "-q", "topic")
    assert ci_params.changed_files() == [("A", "notes.md")]


@pytest.fixture
def offline(monkeypatch):
    """Run main() from the repository root against a fixed list of changes."""
    monkeypatch.chdir(ROOT)
    monkeypatch.delenv("CIRCLE_BRANCH", raising=False)
    monkeypatch.delenv("MR_CI_PARAMS", raising=False)
    monkeypatch.setattr(ci_params, "fetch_main", lambda: None)
    changes = []
    monkeypatch.setattr(ci_params, "changed_files", lambda: changes)
    return changes


def run(capsys, *argv):
    code = ci_params.main(list(argv))
    out, err = capsys.readouterr()
    return code, out, err


def test_the_setup_job_gets_every_parameter(offline, capsys):
    offline.append(("M", "docs/runbook.md"))
    code, out, _ = run(capsys)
    assert code == 0
    assert json.loads(out) == {
        "run-all": False,
        "run-go": False,
        "run-python": False,
        "run-proto": False,
        "run-lint": True,
    }


def test_main_runs_everything(offline, capsys, monkeypatch):
    monkeypatch.setenv("CIRCLE_BRANCH", "main")
    monkeypatch.setattr(ci_params, "changed_files", lambda: pytest.fail("main is not diffed"))
    code, out, _ = run(capsys)
    assert code == 0 and json.loads(out)["run-all"] is True


def test_check_passes_when_the_jobs_got_the_selection(offline, capsys, monkeypatch):
    offline.append(("M", "go/internal/worker/worker.go"))
    monkeypatch.setenv("MR_CI_PARAMS", json.dumps({p: p == "run-go" for p in ci_params.PARAMETERS}))
    code, out, _ = run(capsys, "--check")
    assert code == 0
    assert "1 changed file(s), 0 deleted, all mapped; selected ['run-go']" in out


def test_check_fails_when_the_jobs_got_less(offline, capsys, monkeypatch):
    offline.append(("M", "go/internal/worker/worker.go"))
    monkeypatch.setenv(
        "MR_CI_PARAMS", json.dumps({p: p == "run-lint" for p in ci_params.PARAMETERS})
    )
    code, _, err = run(capsys, "--check")
    assert code == 1
    assert "need ['run-go']: the selection did not reach the jobs" in err


def test_check_accepts_run_all_for_anything(offline, capsys, monkeypatch):
    offline.extend([("M", "go/x.go"), ("M", "proto/a.proto")])
    monkeypatch.setenv(
        "MR_CI_PARAMS", json.dumps({p: p == "run-all" for p in ci_params.PARAMETERS})
    )
    assert run(capsys, "--check")[0] == 0


def test_check_fails_on_an_unmapped_file(offline, capsys):
    offline.append(("A", "newarea/file.txt"))
    code, _, err = run(capsys, "--check")
    assert code == 1
    assert "newarea/file.txt" in err


def test_an_unknown_argument_is_a_usage_error(offline, capsys):
    assert run(capsys, "--frobnicate")[0] == 2
