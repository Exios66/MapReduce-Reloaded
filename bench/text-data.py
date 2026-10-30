"""Stream text into shards and derive an independent, disk-backed word-count oracle.

Usage: text-data.py prepare SIZE M R SOURCE DIR < generator-output
       text-data.py verify MANIFEST OUTDIR
The target is a minimum: generators finish whole lines/chunks; actual bytes
are recorded. Native input/output with zstd intermediate is a named variant.
"""

import hashlib
import json
import sqlite3
import sys
from collections import Counter
from pathlib import Path

MAX_LINE = 16 << 20


def partition(key, reduces):
    value = 0xCBF29CE484222325
    for byte in key:
        value = ((value ^ byte) * 0x100000001B3) & 0xFFFFFFFFFFFFFFFF
    return value % reduces


def prepare(stream, directory, target, shards, reduces, source):
    if min(target, shards, reduces) <= 0 or target < shards:
        raise ValueError("require positive target, M and R, with target >= M")
    directory.mkdir()  # Never overwrite input or a failed preparation.
    database = directory / "oracle.sqlite"
    counts = Counter()
    records = logical = tokens = 0
    files = []
    with sqlite3.connect(database) as db:
        db.execute("CREATE TABLE counts (word BLOB PRIMARY KEY, n INTEGER NOT NULL)")

        def flush():
            db.executemany(
                "INSERT INTO counts VALUES (?, ?) ON CONFLICT(word) DO UPDATE SET n=n+excluded.n",
                counts.items(),
            )
            db.commit()
            counts.clear()

        for shard in range(shards):
            path = directory / f"part-m-{shard:05d}.txt"
            digest = hashlib.sha256()
            with path.open("xb") as output:
                while True:
                    line = stream.readline(MAX_LINE + 1)
                    if not line:
                        break
                    if len(line) > MAX_LINE or not line.endswith(b"\n"):
                        raise ValueError("require LF-terminated lines of at most 16 MiB")
                    output.write(line)
                    digest.update(line)
                    logical += len(line)
                    records += 1
                    words = line.split()  # ASCII whitespace, independent of the job helpers.
                    counts.update(words)
                    tokens += len(words)
                    if tokens >= 50_000:
                        flush()
                        tokens = 0
                    if shard < shards - 1 and logical >= target * (shard + 1) // shards:
                        break
            if path.stat().st_size == 0:
                raise ValueError("not enough input for M nonempty shards")
            files.append(
                {"name": path.name, "bytes": path.stat().st_size, "sha256": digest.hexdigest()}
            )
        if not target <= logical <= target + MAX_LINE:
            raise ValueError("generator bytes outside [target, target + 16 MiB]")
        flush()
        digests = [hashlib.sha256() for _ in range(reduces)]
        keys = 0
        for word, count in db.execute("SELECT word, n FROM counts ORDER BY word"):
            digests[partition(word, reduces)].update(word + b"\t" + str(count).encode() + b"\n")
            keys += 1
    database.unlink()  # Only this invocation's closed scratch database.
    manifest = dict(
        target_bytes=target,
        logical_bytes=logical,
        physical_bytes=logical,
        records=records,
        shards=shards,
        format="text",
        source=source,
        files=files,
        oracle=dict(reduces=reduces, keys=keys, sha256=[h.hexdigest() for h in digests]),
    )
    (directory / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest


def verify(manifest, output):
    oracle = manifest["oracle"]
    parts = [output / f"part-r-{i:05d}" for i in range(oracle["reduces"])]
    if set(output.glob("part-*")) != set(parts):
        raise ValueError("unexpected output parts")
    physical = 0
    for path, expected in zip(parts, oracle["sha256"], strict=True):
        if path.is_symlink() or not path.is_file():
            raise ValueError("output is not a regular file")
        digest = hashlib.sha256()
        with path.open("rb") as stream:
            while block := stream.read(1 << 20):
                digest.update(block)
                physical += len(block)
        if digest.hexdigest() != expected:
            raise ValueError("word-count output differs from the input-derived oracle")
    return dict(
        verified=True,
        records=oracle["keys"],
        physical_output_bytes=physical,
        method="independent streaming ASCII-word counts; partition SHA-256 agreement",
    )


def main():
    if len(sys.argv) == 7 and sys.argv[1] == "prepare":
        result = prepare(sys.stdin.buffer, Path(sys.argv[6]), *map(int, sys.argv[2:5]), sys.argv[5])
    elif len(sys.argv) == 4 and sys.argv[1] == "verify":
        result = verify(json.loads(Path(sys.argv[2]).read_text()), Path(sys.argv[3]))
    else:
        raise SystemExit(__doc__)
    print(json.dumps(result))


if __name__ == "__main__":
    main()
