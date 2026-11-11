"""bench/drop-cache.py, which E0 uses to drop other inputs from the page cache (#347)."""

import importlib.util
import os
import subprocess
import sys
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "bench" / "drop-cache.py"


@pytest.fixture
def drop_cache():
    spec = importlib.util.spec_from_file_location("drop_cache", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_drops_each_file(drop_cache, tmp_path, monkeypatch):
    a, b = tmp_path / "a", tmp_path / "b"
    a.write_text("a")
    b.write_text("b")
    calls = []

    def fadvise(fd, offset, length, advice):
        calls.append((os.fstat(fd).st_ino, offset, length, advice))

    monkeypatch.setattr(os, "posix_fadvise", fadvise, raising=False)
    monkeypatch.setattr(os, "POSIX_FADV_DONTNEED", 4, raising=False)
    assert drop_cache.main([str(a), str(b)]) == 0
    assert calls == [(a.stat().st_ino, 0, 0, 4), (b.stat().st_ino, 0, 0, 4)]


def test_missing_file_exits_1_and_names_it(drop_cache, tmp_path, monkeypatch, capsys):
    monkeypatch.setattr(os, "posix_fadvise", lambda *a: None, raising=False)
    monkeypatch.setattr(os, "POSIX_FADV_DONTNEED", 4, raising=False)
    assert drop_cache.main([str(tmp_path / "nope")]) == 1
    err = capsys.readouterr().err
    assert "drop-cache:" in err
    assert "nope" in err


def test_no_arguments_prints_usage_and_exits_2(drop_cache, capsys):
    assert drop_cache.main([]) == 2
    assert "usage: drop-cache.py PATH..." in capsys.readouterr().err


@pytest.mark.skipif(not hasattr(os, "posix_fadvise"), reason="posix_fadvise is Linux only")
def test_real_fadvise_on_linux(tmp_path):
    f = tmp_path / "input.txt"
    f.write_bytes(b"x" * 65536)
    result = subprocess.run([sys.executable, SCRIPT, f], capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
