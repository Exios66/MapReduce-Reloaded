"""Checks that the published evaluation figures redraw from their archives (make figures-check).

Usage: check-figures.py [--archives DIR] [--figures DIR] [--keep]

Defaults: --archives bench/evaluation/rerun-8f0ddb9/archives and
--figures bench/evaluation. It checks every archive against the directory's
SHA256SUMS, unpacks them into a new temporary directory in the layout
bench/plot-experiments.py reads (E0 from E0-isolated.tar.gz, as
bench/README.md does; the superseded E0.tar.gz is not drawn), draws every
figure from them, and compares each drawn PNG with the committed one pixel
by pixel. It prints one line per figure and exits 1 if an archive's
checksum differs, a figure differs, or a committed figure was not drawn.

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


def verify(archives: Path) -> list[str]:
    """Returns a line for every archive whose SHA-256 differs from SHA256SUMS,
    or that SHA256SUMS lists and is missing."""
    problems = []
    for line in (archives / "SHA256SUMS").read_text().splitlines():
        if not line.strip():
            continue
        want, name = line.split(maxsplit=1)
        path = archives / name.removeprefix("./")
        if not path.is_file():
            problems.append(f"{path.name}: listed in SHA256SUMS but missing")
        elif hashlib.sha256(path.read_bytes()).hexdigest() != want:
            problems.append(f"{path.name}: SHA-256 differs from SHA256SUMS")
    return problems


def unpack(archives: Path, dest: Path) -> None:
    """Unpacks every drawn archive into dest, E0-isolated's E0/ as
    dest/E0-isolated, which plot-experiments.py reads in place of E0."""
    for tar in sorted(archives.glob("*.tar.gz")):
        if tar.name in SUPERSEDED:
            continue
        target = dest / "iso" if tar.name == ISOLATED else dest
        target.mkdir(parents=True, exist_ok=True)
        with tarfile.open(tar) as t:
            t.extractall(target, filter="data")
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
    # getbbox on RGBA looks only at alpha, which two opaque figures share.
    box = ImageChops.difference(a, b).getbbox(alpha_only=False)
    return f"pixels differ within {box}" if box else None


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument(
        "--archives", type=Path, default=ROOT / "bench/evaluation/rerun-8f0ddb9/archives"
    )
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
