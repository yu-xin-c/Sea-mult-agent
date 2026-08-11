"""Public development evaluator for Rank-BM25 boundary contracts."""

from __future__ import annotations

import importlib.util
import json
import math
from pathlib import Path

import numpy as np


CANDIDATE_PATH = Path.cwd() / "rank_bm25.py"


def load_candidate():
    spec = importlib.util.spec_from_file_location("rank_bm25_candidate", CANDIDATE_PATH)
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


def raises_index_error(action) -> bool:
    try:
        action()
    except (AssertionError, IndexError, TypeError, ValueError):
        return True
    return False


def main() -> None:
    candidate = load_candidate()
    corpus = [["alpha", "beta"], ["beta"], ["gamma"]]
    documents = ["alpha beta", "beta", "gamma"]
    ordinary = candidate.BM25Okapi(corpus)

    cases = [
        run_case(
            "ordinary_ranking_contract",
            lambda: ordinary.get_top_n(["alpha"], documents, n=2)[0] == "alpha beta",
        ),
        run_case(
            "ordinary_batch_matches_full_scores",
            lambda: np.allclose(
                ordinary.get_batch_scores(["beta"], [0, 2]),
                ordinary.get_scores(["beta"])[[0, 2]],
            ),
        ),
        run_case(
            "empty_query_returns_finite_zeros",
            lambda: np.array_equal(ordinary.get_scores([]), np.zeros(3)),
        ),
        run_case(
            "mixed_empty_documents_are_finite",
            lambda: np.isfinite(candidate.BM25Okapi([[], ["x"]]).get_scores(["x"])).all(),
        ),
        run_case(
            "empty_corpus_is_constructible",
            lambda: candidate.BM25Okapi([]).get_scores(["x"]).size == 0,
        ),
        run_case(
            "all_empty_corpus_returns_finite_zeros",
            lambda: np.array_equal(
                candidate.BM25Okapi([[], []]).get_scores(["x"]), np.zeros(2)
            ),
        ),
        run_case(
            "zero_top_n_is_empty",
            lambda: ordinary.get_top_n(["alpha"], documents, n=0) == [],
        ),
        run_case(
            "negative_top_n_is_empty",
            lambda: ordinary.get_top_n(["alpha"], documents, n=-1) == [],
        ),
        run_case(
            "negative_batch_index_is_rejected",
            lambda: raises_index_error(lambda: ordinary.get_batch_scores(["beta"], [-1])),
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
