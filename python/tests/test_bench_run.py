"""Benchmark startup permissions without starting the Compose cluster."""

import os
import stat
import subprocess
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "bench" / "run.sh"


@pytest.mark.parametrize("mask", ["022", "027", "077"])
@pytest.mark.parametrize("existing_data", [False, True])
def test_input_is_accessible_to_nonroot_workers(tmp_path, mask, existing_data):
    startup, marker, _ = SCRIPT.read_text().partition("\nurls=()\n")
    assert marker, "benchmark startup boundary was not found"
    bench = tmp_path / "bench"
    bench.mkdir()
    (bench / "run.sh").write_text(startup)
    (bench / "gen-grep.py").write_text("print('input record')\n")
    data = tmp_path / "data"
    if existing_data:
        data.mkdir(mode=0o700)
    # At Compose startup, the bind mount must already belong to the caller
    # and be traversable. Docker must not create it as root.
    code = f"""
        umask {mask}
        docker() {{
            [ "$1" != compose ] || {{
                [ -d data ] && [ "$(stat -c %u data)" = "$(id -u)" ] || exit 91
                [ $(( 8#$(stat -c %a data) & 0111 )) -eq $((0111)) ] || exit 92
            }}
        }}
        source "$0" grep
        trap - EXIT
    """
    subprocess.run(
        ["bash", "-c", code, str(bench / "run.sh")],
        cwd=tmp_path,
        check=True,
        capture_output=True,
        text=True,
        timeout=10,
    )
    assert data.stat().st_uid == os.getuid()
    assert stat.S_IMODE(data.stat().st_mode) & 0o111 == 0o111
    (generated,) = data.glob("bench-input-grep-*.txt")
    assert generated.read_text() == "input record\n"
    assert stat.S_IMODE(generated.stat().st_mode) & 0o444 == 0o444
