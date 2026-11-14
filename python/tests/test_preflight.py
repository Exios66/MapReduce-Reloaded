"""The preflight script knows the owner by email, under any spelling of the name."""

import os
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
PREFLIGHT = ROOT / ".claude/skills/saving-ci-credits/preflight.sh"

OWNER = ("Jack J Burleson", "exios4@protonmail.com")
CODERABBIT = ("coderabbitai[bot]", "136622811+coderabbitai[bot]@users.noreply.github.com")
GITHUB = ("GitHub", "noreply@github.com")
AGENT = ("Claude", "noreply@anthropic.com")


@pytest.fixture
def branch(tmp_path):
    """A clone with a branch off origin/main, and commit(author, committer)."""
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    # No GitHub here: a gh that always fails, so no lookup leaves the machine.
    tools = tmp_path / "tools"
    tools.mkdir()
    (tools / "gh").write_text("#!/bin/sh\nexit 1\n")
    (tools / "gh").chmod(0o755)
    env["PATH"] = str(tools) + os.pathsep + env["PATH"]
    origin, clone = tmp_path / "origin.git", tmp_path / "clone"

    def git(*args, cwd=clone, who=OWNER, by=OWNER):
        identity = {
            "GIT_AUTHOR_NAME": who[0],
            "GIT_AUTHOR_EMAIL": who[1],
            "GIT_COMMITTER_NAME": by[0],
            "GIT_COMMITTER_EMAIL": by[1],
        }
        return subprocess.run(
            ["git", *args],
            cwd=cwd,
            env={**env, **identity},
            check=True,
            capture_output=True,
            text=True,
        ).stdout

    git("init", "-q", "--bare", "-b", "main", str(origin), cwd=tmp_path)
    git("clone", "-q", str(origin), str(clone), cwd=tmp_path)
    git("commit", "-q", "--allow-empty", "-m", "main")
    git("push", "-q", "origin", "HEAD:main")
    git("switch", "-q", "-c", "topic")

    def commit(who=OWNER, by=OWNER):
        git("commit", "-q", "--allow-empty", "-m", "work [skip ci]", who=who, by=by)

    def run():
        result = subprocess.run(
            ["bash", str(PREFLIGHT)], cwd=clone, env=env, capture_output=True, text=True
        )
        identity = [line for line in result.stdout.splitlines() if "commit" in line]
        return result.returncode, identity

    return commit, run


def test_owner_under_any_name_and_github_web_merges_pass(branch):
    commit, run = branch
    for name in [
        "Jack J Burleson",
        "Jack J Burleson // LJM",
        "Jack J Burleson // Lucius Morningstar",
        "Jack | LJM",  # a name may hold the old field separator
    ]:
        commit(who=(name, OWNER[1]), by=(name, OWNER[1]))
    commit(who=("Lucius Morningstar", "148591095+Exios66@users.noreply.github.com"))
    commit(by=GITHUB)  # a merge or squash made in GitHub's web interface
    commit(who=CODERABBIT, by=CODERABBIT)
    commit(who=CODERABBIT)  # the owner commits CodeRabbit's suggestion
    code, identity = run()
    assert code == 0, identity
    assert "  ok    commits: authored and committed by the owner" in identity


@pytest.mark.parametrize(
    ("who", "by"),
    [
        (AGENT, AGENT),
        (OWNER, AGENT),
        (AGENT, OWNER),
        (("Jack J Burleson", "someone@example.com"), OWNER),
        (CODERABBIT, AGENT),
    ],
)
def test_any_other_identity_fails(branch, who, by):
    # The name alone is not enough: the last case spells it right with
    # another address.
    commit, run = branch
    commit()
    commit(who=who, by=by)
    code, identity = run()
    assert code == 1
    failures = [line for line in identity if line.startswith("  FAIL")]
    assert len(failures) == 1, identity
    assert f"<{who[1]}>" in failures[0] and f"<{by[1]}>" in failures[0]
    assert not any("authored and committed by the owner" in line for line in identity)
