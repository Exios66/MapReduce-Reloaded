"""Checks the relative links in the repository's Markdown files (make lint, #159).

Usage: check-links.py [FILE.md ...]   default: every tracked *.md

For each link, image and reference definition outside code, a relative
target must name a file or directory in the repository. An anchor (#part)
on a Markdown target, or on the file itself, must name one of its headings
as GitHub makes them, or an explicit <a name> or id. Links with a scheme
(https:, mailto:) are not checked. It prints one line per broken link and
exits 1 if there are any.
"""

import re
import subprocess
import sys
from pathlib import Path
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parents[1]

FENCE = re.compile(r"^\s{0,3}(`{3,}|~{3,})")
CODE_SPAN = re.compile(r"(`+)(?:(?!\1).)+?\1")
INLINE = re.compile(r"!?\[(?:[^\[\]]|\[[^\]]*\])*\]\(\s*<?([^)\s>]*)>?(?:\s+[\"'(][^)]*)?\)")
REFERENCE = re.compile(r"^\s{0,3}\[[^\]]+\]:\s*<?([^\s>]+)>?")
HTML = re.compile(r"""<[a-zA-Z][^>]*?\s(?:href|src)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))""")
HEADING = re.compile(r"^\s{0,3}(#{1,6})\s+(.*?)(?:\s+#+)?\s*$")
SETEXT = re.compile(r"^\s{0,3}(=+|-+)\s*$")
ANCHOR = re.compile(r"""<[a-zA-Z][^>]*?\s(?:id|name)\s*=\s*["']([^"']+)["']""")
SCHEME = re.compile(r"^[a-zA-Z][a-zA-Z0-9+.-]*:|^//")


def lines_outside_code(text: str):
    """Yields (line number, line) for each line outside fenced code, with
    code spans blanked out. A fence ends at a line of only the same
    character, at least as many times, as in CommonMark."""
    fence = None
    for n, line in enumerate(text.splitlines(), 1):
        m = FENCE.match(line)
        if fence is None:
            if m:
                fence = m[1]
            else:
                yield n, CODE_SPAN.sub("", line)
        elif m and m[1][0] == fence[0] and len(m[1]) >= len(fence) and not line[m.end() :].strip():
            fence = None


def slug(heading: str) -> str:
    """Returns GitHub's anchor for a heading: its text as rendered,
    lowercased, without punctuation, and with spaces turned into hyphens."""
    text = re.sub(r"!?\[([^\]]*)\]\([^)]*\)", r"\1", heading)  # a link keeps its text
    text = re.sub(r"<[^>]+>", "", text).replace("`", "").replace("*", "")
    text = re.sub(r"(?<!\w)_+|_+(?!\w)", "", text)  # _emphasis_, not snake_case
    return re.sub(r"[^\w\- ]", "", text.strip().lower()).replace(" ", "-")


def anchors(text: str) -> tuple[set[str], set[str]]:
    """Returns the anchors a Markdown file defines: its headings, with -1,
    -2 added when one repeats, as GitHub does, and the explicit names and
    ids of its HTML tags, which are case-sensitive. Headings are read from
    the raw lines, as code spans count in them; tags in code spans do not."""
    found: set[str] = set()
    ids: set[str] = set()
    seen: dict[str, int] = {}
    raw = text.splitlines()
    prev = None
    for n, prose in lines_outside_code(text):
        line = raw[n - 1]
        title = None
        if m := HEADING.match(line):
            title = m[2]
        elif prev is not None and SETEXT.match(line):
            title = prev
        if title is not None:
            base = slug(title)
            count = seen.get(base, 0)
            seen[base] = count + 1
            found.add(base if count == 0 else f"{base}-{count}")
        ids.update(m[1] for m in ANCHOR.finditer(prose))
        prev = line.strip() if line.strip() and not HEADING.match(line) else None
    return found, ids


def links(text: str):
    """Yields (line number, target) for each link, image, reference
    definition and HTML href or src outside code."""
    for n, line in lines_outside_code(text):
        for pattern in (INLINE, REFERENCE, HTML):
            for m in pattern.finditer(line):
                yield n, m[m.lastindex]


def check(md: Path, cache: dict[Path, tuple[set[str], set[str]]]) -> list[str]:
    """Returns a message for each broken relative link in md."""
    errors = []
    rel = md.relative_to(ROOT)
    for n, target in links(md.read_text(encoding="utf-8")):
        if not target or SCHEME.match(target):
            continue
        path, _, anchor = target.partition("#")
        path = unquote(path.partition("?")[0])
        if path:
            dest = (ROOT / path.lstrip("/") if path.startswith("/") else md.parent / path).resolve()
            if not dest.is_relative_to(ROOT.resolve()):
                errors.append(f"{rel}:{n}: {target}: outside the repository")
                continue
            if not dest.exists():
                errors.append(f"{rel}:{n}: {target}: no such file")
                continue
        else:
            dest = md
        if anchor and dest.suffix == ".md" and dest.is_file():
            if dest not in cache:
                cache[dest] = anchors(dest.read_text(encoding="utf-8"))
            headings, ids = cache[dest]
            if unquote(anchor).lower() not in headings and unquote(anchor) not in ids:
                errors.append(f"{rel}:{n}: {target}: no such heading or anchor")
    return errors


def tracked() -> list[Path]:
    """Returns every Markdown file git tracks."""
    out = subprocess.run(
        ["git", "ls-files", "-z", "*.md"], cwd=ROOT, capture_output=True, text=True, check=True
    ).stdout
    return [ROOT / p for p in out.split("\0") if p]


def main(argv: list[str]) -> int:
    files = [Path(a).resolve() for a in argv] or tracked()
    cache: dict[Path, tuple[set[str], set[str]]] = {}
    errors = [e for md in files for e in check(md, cache)]
    for e in errors:
        print(e)
    print(f"check-links: {len(files)} files, {len(errors)} broken links", file=sys.stderr)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
