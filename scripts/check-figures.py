"""Checks that the published evaluation figures redraw from their archives (make figures-check).

Usage: check-figures.py [--archives DIR] [--figures DIR] [--keep]

Defaults: --archives bench/evaluation/raw and --figures bench/evaluation.
It checks every archive against the directory's SHA256SUMS, unpacks them
into a new temporary directory in the layout bench/plot-experiments.py
reads (E0 from E0-isolated.tar.gz, as bench/README.md does; the replaced
E0.tar.gz, archived in bench/evaluation/archive/, is never drawn), draws every
figure from them, and compares each drawn PNG with the committed one pixel
by pixel. Only archives SHA256SUMS lists are unpacked. It prints one line
per figure and exits 1 if an archive's checksum differs, an archive is not
listed, a figure differs, or a committed figure was not drawn.

It needs matplotlib, which make venv does not install, so make check does
not run it; run it on the experiment VM (.claude/skills/analyzing-bench-results).
--keep leaves the temporary directory and names it.
"""

import argparse
import hashlib
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
# The archives the figures are drawn from, and the one they replaced.
SUPERSEDED = {"E0.tar.gz"}
ISOLATED = "E0-isolated.tar.gz"


def listed(archives: Path) -> dict[str, str]:
    """Returns SHA256SUMS as {archive file name: SHA-256}."""
    sums = {}
    for line in (archives / "SHA256SUMS").read_text().splitlines():
        if not line.strip():
            continue
        want, name = line.split(maxsplit=1)
        # sha256sum marks binary-mode lines with a leading *.
        sums[Path(name.lstrip("*")).name] = want
    return sums


def verify(archives: Path) -> list[str]:
    """Returns a line for every archive whose SHA-256 differs from SHA256SUMS,
    that SHA256SUMS lists and is missing, or that is present and not listed."""
    sums = listed(archives)
    problems = []
    for name, want in sums.items():
        path = archives / name
        if not path.is_file():
            problems.append(f"{name}: listed in SHA256SUMS but missing")
        elif hashlib.sha256(path.read_bytes()).hexdigest() != want:
            problems.append(f"{name}: SHA-256 differs from SHA256SUMS")
    for path in sorted(archives.glob("*.tar.gz")):
        if path.name not in sums:
            problems.append(f"{path.name}: not listed in SHA256SUMS, so not unpacked")
    return problems


def unpack(archives: Path, dest: Path) -> None:
    """Unpacks every drawn archive SHA256SUMS lists into dest, E0-isolated's
    E0/ as dest/E0-isolated, which plot-experiments.py reads in place of E0.
    An unlisted archive is never unpacked, so it cannot replace checked data."""
    for name in sorted(listed(archives)):
        tar = archives / name
        if name in SUPERSEDED or not name.endswith(".tar.gz") or not tar.is_file():
            continue
        target = dest / "iso" if name == ISOLATED else dest
        target.mkdir(parents=True, exist_ok=True)
        with tarfile.open(tar) as t:
            if hasattr(tarfile, "data_filter"):  # Python 3.11.4 and later
                t.extractall(target, filter="data")
                continue
            # Earlier 3.11 releases lack filter=: refuse what "data" would.
            for m in t.getmembers():
                member = Path(m.name)
                if member.is_absolute() or ".." in member.parts or not (m.isfile() or m.isdir()):
                    raise ValueError(f"{name}: unsafe member {m.name}")
            t.extractall(target)
    if (dest / "iso" / "E0").is_dir():
        (dest / "iso" / "E0").rename(dest / "E0-isolated")
        shutil.rmtree(dest / "iso")


def differs(drawn: Path, committed: Path) -> str | None:
    """Returns why two PNGs differ in their pixels, or None if they match."""
    from PIL import Image, ImageChops  # matplotlib depends on Pillow

    a = Image.open(drawn).convert("RGBA")
    b = Image.open(committed).convert("RGBA")
    if a.size != b.size:
        return f"size {a.size} against {b.size}"
    # getbbox on RGBA looks only at alpha, which two opaque figures share,
    # and older Pillow has no alpha_only=False: check color and alpha apart.
    diff = ImageChops.difference(a, b)
    box = diff.convert("RGB").getbbox() or diff.getchannel("A").getbbox()
    return f"pixels differ within {box}" if box else None


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--archives", type=Path, default=ROOT / "bench/evaluation/raw")
    p.add_argument("--figures", type=Path, default=ROOT / "bench/evaluation")
    p.add_argument("--keep", action="store_true")
    args = p.parse_args(argv)

    problems = verify(args.archives)
    work = Path(tempfile.mkdtemp(prefix="check-figures-"))
    try:
        results, out = work / "results", work / "out"
        unpack(args.archives, results)
        subprocess.run(
            [sys.executable, str(ROOT / "bench/plot-experiments.py"), str(results), str(out)],
            check=True,
            stdout=subprocess.DEVNULL,
        )
        drawn = {p.name for p in out.glob("*.png")}
        for name in sorted(drawn | {p.name for p in args.figures.glob("*.png")}):
            if name not in drawn:
                problems.append(f"{name}: committed but not drawn")
            elif not (args.figures / name).exists():
                problems.append(f"{name}: drawn but not committed")
            elif why := differs(out / name, args.figures / name):
                problems.append(f"{name}: {why}")
            else:
                print(f"check-figures: {name}: matches")
    finally:
        if args.keep:
            print(f"check-figures: kept {work}")
        else:
            shutil.rmtree(work)
    for line in problems:
        print(f"check-figures: {line}")
    print(f"check-figures: {len(problems)} problems", file=sys.stderr)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
