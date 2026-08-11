"""Model-hidden composition holdout for Rank-BM25 boundary contracts."""

from __future__ import annotations

import importlib.util
import json
from pathlib import Path

import numpy as np


CANDIDATE_PATH = Path.cwd() / "rank_bm25.py"


def load_candidate():
    spec = importlib.util.spec_from_file_location("rank_bm25_holdout", CANDIDATE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("Rank-BM25 candidate cannot be imported")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_case(name, check):
    try:
        passed = bool(check())
        return {"name": name, "passed": passed, "error": "" if passed else "assertion failed"}
    except Exception as exc:
        return {"name": name, "passed": False, "error": f"{type(exc).__name__}: {exc}"}


def rejects_negative_index(instance) -> bool:
    try:
        instance.get_batch_scores(["x"], [-1])
    except (AssertionError, IndexError, TypeError, ValueError):
        return True
    return False


def variants(candidate):
    return (candidate.BM25Okapi, candidate.BM25L, candidate.BM25Plus)


def main() -> None:
    candidate = load_candidate()
    cases = [
        run_case(
            "all_variants_accept_empty_corpus",
            lambda: all(cls([]).get_scores(["x"]).size == 0 for cls in variants(candidate)),
        ),
        run_case(
            "all_variants_keep_all_empty_scores_finite",
            lambda: all(
                np.array_equal(cls([[], []]).get_scores(["x"]), np.zeros(2))
                for cls in variants(candidate)
            ),
        ),
        run_case(
            "all_variants_reject_negative_top_n",
            lambda: all(
                cls([["x"], ["y"]]).get_top_n(["x"], ["x", "y"], -3) == []
                for cls in variants(candidate)
            ),
        ),
        run_case(
            "all_variants_reject_negative_batch_indices",
            lambda: all(
                rejects_negative_index(cls([["x"], ["y"]]))
                for cls in variants(candidate)
            ),
        ),
    ]
    passed = sum(case["passed"] for case in cases)
    print(
        json.dumps(
            {
                "status": "ok",
                "metrics": {"robustness_score": passed / len(cases)},
                "passed_cases": passed,
                "total_cases": len(cases),
                "cases": cases,
            },
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
