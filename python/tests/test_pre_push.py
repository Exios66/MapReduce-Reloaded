"""The full push gate must not redirect tests' Git commands into the pushed repo."""

import os
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]


@pytest.mark.parametrize("check_exit", [0, 7])
def test_pre_push_clears_git_repository_environment(tmp_path, check_exit):
    """Temporary-repository tests stay isolated, and a failed gate still blocks push."""
    repo = tmp_path / "pushed"
    repo.mkdir()
    hooks = repo / ".githooks"
    hooks.mkdir()
    shutil.copyfile(ROOT / ".githooks/pre-push", hooks / "pre-push")
    clean_env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}

    def git(*args, cwd=repo):
        return subprocess.check_output(["git", *args], cwd=cwd, env=clean_env, text=True)

    git("init", "-q")
    git("add", ".githooks/pre-push")
    git(
        "-c",
        "user.name=Jack J Burleson // LJM",
        "-c",
        "user.email=exios4@protonmail.com",
        "commit",
        "-qm",
        "Initial hook fixture",
    )
    sha = git("rev-parse", "HEAD").strip()
    other = tmp_path / "independent"
    other.mkdir()
    (other / "separate.txt").write_text("not in the pushed repository\n")
    tools = tmp_path / "tools"
    tools.mkdir()
    make = tools / "make"
    make.write_text(
        "#!/bin/sh\nset -eu\n"
        '[ "$#" -eq 1 ] && [ "$1" = check ]\n'
        '[ -z "${GIT_DIR+x}" ] && [ -z "${GIT_WORK_TREE+x}" ]\n'
        '[ -z "${GIT_INDEX_FILE+x}" ]\n'
        'git -C "$OTHER_REPO" init -q\n'
        'git -C "$OTHER_REPO" add separate.txt\n'
        '[ "$(git -C "$OTHER_REPO" ls-files)" = separate.txt ]\n'
        'exit "$CHECK_EXIT"\n'
    )
    make.chmod(0o755)
    env = {
        **clean_env,
        "PATH": str(tools) + os.pathsep + clean_env["PATH"],
        "GIT_DIR": str(repo / ".git"),
        "GIT_WORK_TREE": str(repo),
        "GIT_INDEX_FILE": str(repo / ".git/index"),
        "OTHER_REPO": str(other),
        "CHECK_EXIT": str(check_exit),
    }
    result = subprocess.run(
        ["sh", str(hooks / "pre-push")],
        cwd=repo,
        env=env,
        input=f"refs/heads/main {sha} refs/heads/main {'0' * 40}\n",
        text=True,
        capture_output=True,
        timeout=15,
    )
    assert result.returncode == check_exit, result.stdout + result.stderr
    assert git("ls-files").splitlines() == [".githooks/pre-push"]
    assert git("ls-files", cwd=other).splitlines() == ["separate.txt"]
