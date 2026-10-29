"""Tests for scripts/check-links.py, the Markdown link checker (#159)."""

import importlib.util
import subprocess
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "scripts/check-links.py"
_spec = importlib.util.spec_from_file_location("check_links", SCRIPT)
links = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(links)


@pytest.fixture
def repo(tmp_path, monkeypatch):
    """A repository root of Markdown files; returns a function that writes
    one and returns its path."""
    monkeypatch.setattr(links, "ROOT", tmp_path)

    def write(name, text):
        p = tmp_path / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)
        return p

    return write


def check(md):
    return links.check(md, {})


def test_good_links_pass(repo):
    repo("docs/b.md", "# B\n\n## Two words, `code` and *more*\n\n## Two words, `code` and *more*\n")
    repo("docs/c.md", 'Setext\n======\n\n<a name="here"></a>\n<div id="there"></div>\n')
    md = repo(
        "a.md",
        "# Top\n\n"
        "[b](docs/b.md) [b2](docs/b.md#two-words-code-and-more)"
        " [b3](docs/b.md#two-words-code-and-more-1) [c](docs/c.md#setext)\n"
        "[c2](docs/c.md#here) [c3](docs/c.md#there) [dir](docs/) [self](#top)"
        " [root](/docs/b.md) [web](https://example.com/x.md#y) [mail](mailto:a@b)\n"
        '![img](docs/b.md "title") <a href="docs/c.md#here">x</a>\n\n'
        "[ref]: docs/b.md#b\n",
    )
    assert check(md) == []


def test_broken_links_fail(repo):
    repo("b.md", "# B\n")
    md = repo(
        "a.md",
        "[x](missing.md) [y](b.md#nope) [z](#nowhere)\n[ref]: gone/c.md\n",
    )
    assert check(md) == [
        "a.md:1: missing.md: no such file",
        "a.md:1: b.md#nope: no such heading or anchor",
        "a.md:1: #nowhere: no such heading or anchor",
        "a.md:2: gone/c.md: no such file",
    ]


def test_code_is_not_checked(repo):
    md = repo(
        "a.md",
        "`[a](nope.md)`\n\n```\n[b](nope.md)\n```\n\n~~~\n[c](nope.md)\n~~~\n",
    )
    assert check(md) == []


def test_anchors_on_other_files_are_not_checked(repo):
    repo("go.mod", "module x\n")
    md = repo("a.md", "[line](go.mod#L1) [enc](a%20b.md)\n")
    assert check(md) == ["a.md:1: a%20b.md: no such file"]


def test_a_fence_ends_only_at_a_long_enough_run_of_its_character(repo):
    md = repo("a.md", "````\n```\n[a](nope.md)\n~~~~\n````\n[b](gone.md)\n")
    assert check(md) == ["a.md:6: gone.md: no such file"]


def test_a_query_is_not_part_of_the_path(repo):
    repo("b.md", "# Part\n")
    md = repo("a.md", "[x](b.md?raw=1#part) [y](b.md?plain=1)\n")
    assert check(md) == []


def test_explicit_ids_are_case_sensitive(repo):
    repo("b.md", '<a id="MySection"></a>\n<p data-id="x">id="y"</p>\n`<a id="fake">`\n')
    bad = "mysection x y fake".split()
    md = repo("a.md", "[a](b.md#MySection) " + " ".join(f"[{a}](b.md#{a})" for a in bad))
    assert check(md) == [f"a.md:1: b.md#{a}: no such heading or anchor" for a in bad]


def test_unquoted_html_attributes_are_checked(repo):
    md = repo("a.md", "<a href=missing.md>x</a> <img src='gone.png'>\n")
    assert check(md) == ["a.md:1: missing.md: no such file", "a.md:1: gone.png: no such file"]


def test_links_out_of_the_repository_fail(repo):
    (repo("a.md", "").parent.parent / "outside.md").touch()
    md = repo("a.md", "[x](../outside.md)\n")
    assert check(md) == ["a.md:1: ../outside.md: outside the repository"]


def test_tracked_files_may_have_spaces(repo):
    repo("a b.md", "# A\n")
    subprocess.run(["git", "init", "-q"], cwd=links.ROOT, check=True)
    subprocess.run(["git", "add", "a b.md"], cwd=links.ROOT, check=True)
    assert links.tracked() == [links.ROOT / "a b.md"]


@pytest.mark.parametrize(
    "heading, want",
    [
        ("Hello, World!", "hello-world"),
        ("`make check` and *CI*", "make-check-and-ci"),
        ("[Link](x.md) text", "link-text"),
        ("snake_case and _emphasis_", "snake_case-and-emphasis"),
        ("ADR 0007: the log", "adr-0007-the-log"),
    ],
)
def test_slug(heading, want):
    assert links.slug(heading) == want


def test_main(repo, capsys):
    good = repo("good.md", "# G\n")
    bad = repo("bad.md", "[x](nope.md)\n")
    assert links.main([str(good)]) == 0
    assert links.main([str(good), str(bad)]) == 1
    out = capsys.readouterr()
    assert out.out == "bad.md:1: nope.md: no such file\n"
    assert "2 files, 1 broken links" in out.err


def test_the_repository_has_no_broken_links():
    """Every tracked Markdown file, as make lint checks them."""
    assert links.main([]) == 0
