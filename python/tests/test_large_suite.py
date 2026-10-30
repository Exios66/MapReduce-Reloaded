"""Every E0–E5 control remains present at the larger scale."""

import importlib.util
from collections import Counter
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "large_suite", Path(__file__).resolve().parents[2] / "bench/large-suite.py"
)
SUITE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SUITE)


def test_full_e0_to_e5_plan_and_separate_corpus_variants():
    rows = SUITE.plan()
    primary = [row for row in rows if row["group"] not in ("paper-sort", "crawl-eng")]
    assert Counter(row["experiment"] for row in primary) == {
        "E0": 27,
        "E1": 18,
        "E2": 12,
        "E3": 12,
        "E4": 6,
        "E5": 9,
    }
    assert set(Counter((r["experiment"], r["setting"], r["group"]) for r in primary).values()) == {
        3
    }
    assert {r["fraction"] for r in primary if r["experiment"] == "E3"} == {None, 0.25, 0.5, 0.75}
    for experiment in ("E2", "E3"):
        first = [
            next(
                r["setting"] for r in primary if r["experiment"] == experiment and r["run"] == trial
            )
            for trial in (1, 2, 3)
        ]
        assert len(set(first)) == 3
    assert len([r for r in rows if r["group"] == "paper-sort"]) == 9
    assert len([r for r in rows if r["group"] == "crawl-eng"]) == 24
    assert len(rows) == 117
