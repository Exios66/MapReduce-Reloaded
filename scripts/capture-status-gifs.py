#!/usr/bin/env python3
"""Capture the status page while a real job runs, into dark and light GIFs.

Tools on PATH: a Chrome-family browser (CHROME, else Google Chrome's macOS
path or chromium, plus ffprobe), ffmpeg (FFMPEG env) and ffprobe. The status
page must already be running at --status-url. Capture frames are the page's
own server-rendered refreshes; the script never draws progress it did not
capture.

With --submit the script starts the job, captures the whole run at
--interval seconds, and keeps --hold extra frames after the job ends so the
final state (complete, counters, workers idle) stays readable. Time lapse
is honest by construction: consecutive frames are real page states
--interval apart, the page's own duration clock is the visible elapsed-time
indicator, and the provenance records the speed factor. The script refuses
a capture shorter than 3 frames, which would read as a looped fragment.

Each GIF lands next to a <prefix>-provenance.json sidecar (commit, machine,
job, input, timings, tool versions), and the script prints frame count,
duration, dimensions and file size per variant.

Example (cluster up, then):
  scripts/capture-status-gifs.py --status-url http://127.0.0.1:18780/ \
    --program examples/wordcount.go -m 32 -r 4 \
    --out data/out/gif-wordcount-1g \
    --inputs data/input/capture-1g/access.log \
    --prefix status-wordcount-1g --assets docs/assets
"""

import argparse
import glob
import json
import os
import platform
import shlex
import shutil
import subprocess
import sys
import time
from datetime import UTC, datetime

CHROME_DEFAULTS = [
    os.environ.get("CHROME", ""),
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    shutil.which("chromium") or shutil.which("chromium-browser") or "",
]
FFMPEG = os.environ.get("FFMPEG", "ffmpeg")
FFPROBE = os.environ.get("FFPROBE", "ffprobe")


def chrome_bin() -> str:
    for c in CHROME_DEFAULTS:
        if c and os.path.exists(c):
            return c
    sys.exit("no Chrome found: set CHROME=/path/to/chrome")


def shot(chrome: str, url: str, theme: str, path: str, profile: str, window: str) -> None:
    subprocess.run(
        [
            chrome,
            "--headless",
            "--hide-scrollbars",
            "--force-color-profile=srgb",
            "--no-first-run",
            "--disable-extensions",
            "--disable-gpu",
            "--user-data-dir=" + profile,
            "--window-size=" + window,
            "--screenshot=" + path,
            url + "?" + theme,
        ],
        check=True,
        capture_output=True,
    )


def _local(spec: str) -> str:
    """The host path of a shared-storage spec: /data/... under MR_DATA_DIR."""
    if spec.startswith("/data"):
        return os.path.join(os.environ.get("MR_DATA_DIR", "data"), spec[len("/data") :].lstrip("/"))
    return spec


def submit_cmd(args) -> list[str] | None:
    if not args.submit:
        return None
    if not (args.program and args.out):
        sys.exit("--submit needs --program and --out")
    paths: list[str] = []
    for spec in args.inputs:
        hits = sorted(glob.glob(os.path.expanduser(_local(spec))))
        if not hits:  # not a local glob: pass the spec through to mr
            hits = [spec]
        for p in hits:
            p = os.path.abspath(p)
            if p not in paths:
                paths.append(p)
    if not paths:
        sys.exit("no input files match " + " ".join(args.inputs))
    cmd = shlex.split(args.submit)
    cmd += ["-m", str(args.m)] if args.m else []
    cmd += ["-r", str(args.r)] if args.r else []
    cmd += [os.path.abspath(args.program), os.path.abspath(args.out)] + paths
    return cmd


def capture(args, work: str, submit: list[str] | None) -> list[tuple[str, str]]:
    """Capture (dark, light) frame pairs until the job ends (or --max-frames)."""
    chrome = chrome_bin()
    frames_dir = work + "-frames"
    os.makedirs(frames_dir, exist_ok=True)
    frames = {}
    proc = None
    if submit:
        print("submit:", shlex.join(submit), flush=True)
        proc = subprocess.Popen(submit, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)

    n = 0
    hold = 0
    started = time.monotonic()
    while n < args.max_frames:
        n += 1
        base = os.path.join(frames_dir, f"{n:04d}")
        print(f"  frame {n:3d} at +{time.monotonic() - started:6.1f}s", flush=True)
        # Same tick for both variants; the page picks dark via ?theme=dark.
        shot(
            chrome,
            args.status_url,
            "theme=dark",
            base + "-dark.png",
            os.path.join(work, "d"),
            args.window,
        )
        shot(
            chrome,
            args.status_url,
            "",
            base + "-light.png",
            os.path.join(work, "l"),
            args.window,
        )
        frames[n] = (base + "-dark.png", base + "-light.png")
        if proc is None:
            if n >= 3:
                break  # watch-only: one pass, no looped fragment
        elif proc.poll() is not None:
            print(f"submit exited: {proc.returncode}", flush=True)
            print(proc.stdout.read() if proc.stdout else "", end="", flush=True)
            hold += 1
            if hold >= args.hold:
                break
        time.sleep(args.interval)
    if len(frames) < 3:
        sys.exit(
            f"only {len(frames)} frames captured: that reads as a looped "
            "fragment; choose --interval to cover the whole job"
        )
    return list(frames.values())


def assemble(frames: list[str], dur: float, out: str, work: str) -> dict:
    lst = os.path.join(work, "list.txt")
    with open(lst, "w") as f:
        for p in frames:
            f.write(f"file '{p}'\nduration {dur:g}\n")
        f.write(f"file '{frames[-1]}'\n")  # concat needs a final file line
    pa = os.path.join(work, "palette.png")
    subprocess.run(
        [
            FFMPEG,
            "-y",
            "-f",
            "concat",
            "-safe",
            "0",
            "-i",
            lst,
            "-vf",
            "palettegen=max_colors=128",
            pa,
        ],
        check=True,
        capture_output=True,
    )
    subprocess.run(
        [
            FFMPEG,
            "-y",
            "-f",
            "concat",
            "-safe",
            "0",
            "-i",
            lst,
            "-i",
            pa,
            "-lavfi",
            "paletteuse=dither=bayer:bayer_scale=5",
            out,
        ],
        check=True,
        capture_output=True,
    )
    probe = subprocess.run(
        [
            FFPROBE,
            "-v",
            "error",
            "-count_frames",
            "-select_streams",
            "v:0",
            "-show_entries",
            "stream=width,height,nb_read_frames",
            "-show_entries",
            "format=duration",
            "-of",
            "json",
            out,
        ],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    info = json.loads(probe)
    st = info["streams"][0]
    return {
        "frames": int(st["nb_read_frames"]),
        "width": st["width"],
        "height": st["height"],
        "duration_s": round(float(info["format"]["duration"]), 2),
        "bytes": os.path.getsize(out),
    }


def _workdir(prefix: str) -> str:
    # Capture scratch under the repo's data/ (gitignored): the shared /tmp
    # is swept while a minutes-long capture runs (MR-185).
    os.makedirs("data", exist_ok=True)
    return os.path.abspath(os.path.join("data", f".gif-{prefix}-{os.getpid()}"))


def main() -> None:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("--status-url", default="http://127.0.0.1:18780/")
    ap.add_argument(
        "--submit",
        default="",
        help="'bin/mr submit -wait' style prefix; empty needs --watch",
    )
    ap.add_argument("--program", default="")
    ap.add_argument("--inputs", nargs="+", default=[])
    ap.add_argument("--out", default="")
    ap.add_argument("-m", type=int, default=0)
    ap.add_argument("-r", type=int, default=0)
    ap.add_argument("--interval", type=float, default=5.0)
    ap.add_argument("--gif-delay", type=int, default=12, help="GIF frame delay, 1/100 s")
    ap.add_argument("--hold", type=int, default=4)
    ap.add_argument("--max-frames", type=int, default=200)
    ap.add_argument("--watch", action="store_true")
    ap.add_argument("--window", default="900x640")
    ap.add_argument("--assets", default="docs/assets")
    ap.add_argument("--prefix", default="status-capture")
    args = ap.parse_args()
    if not args.submit and not args.watch:
        sys.exit("give --submit or --watch")

    work = _workdir(args.prefix)
    os.makedirs(work, exist_ok=True)
    submit = submit_cmd(args)
    pairs = capture(args, work, submit)

    report = {}
    for theme in ("dark", "light"):
        fs = [p[0] if theme == "dark" else p[1] for p in pairs]
        out = os.path.join(args.assets, f"{args.prefix}-{theme}.gif")
        report[theme] = assemble(fs, args.gif_delay / 100, out, work)

    prov = {
        "recorded_at": datetime.now(UTC).isoformat(timespec="seconds"),
        "commit": subprocess.run(
            ["git", "rev-parse", "HEAD"], capture_output=True, text=True
        ).stdout.strip(),
        "machine": {
            "host": platform.node(),
            "arch": platform.machine(),
            "os": platform.platform(),
        },
        "status_url": args.status_url,
        "submit": shlex.join(submit) if submit else "watched a running job",
        "program": args.program,
        "inputs": args.inputs,
        "out": args.out,
        "map_tasks": args.m,
        "reduce_tasks": args.r,
        "capture_interval_s": args.interval,
        "gif_frame_delay_cs": args.gif_delay,
        "speed_factor": round(args.interval / (args.gif_delay / 100), 1),
        "window": args.window,
        "chrome": subprocess.run(
            [chrome_bin(), "--version"], capture_output=True, text=True
        ).stdout.strip(),
        "ffmpeg": subprocess.run(
            [FFMPEG, "-version"], capture_output=True, text=True
        ).stdout.splitlines()[0],
        "gifs": report,
    }
    prov_json = os.path.join(args.assets, f"{args.prefix}-provenance.json")
    with open(prov_json, "w") as f:
        json.dump(prov, f, indent=2)
    for theme, r in report.items():
        print(
            f"{theme:5s}: {r['frames']} frames, {r['width']}x{r['height']}, "
            f"{r['duration_s']} s, {r['bytes']:,} bytes"
        )
    print("provenance:", prov_json)
    shutil.rmtree(work + "-frames", ignore_errors=True)
    shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    main()
