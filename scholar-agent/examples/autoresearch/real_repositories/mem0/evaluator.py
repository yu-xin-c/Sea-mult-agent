"""Frozen deterministic evaluator for Mem0 hybrid retrieval scoring."""

from __future__ import annotations

import importlib.util
import json
import math
from pathlib import Path


WORKSPACE = Path.cwd()
CANDIDATE_PATH = WORKSPACE / "mem0" / "utils" / "scoring.py"


def load_candidate():
    spec = importlib.util.spec_from_file_location("mem0_scoring_candidate", CANDIDATE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("Mem0 scoring candidate cannot be imported")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_case(name, check):
    try:
        passed = bool(check())
        return {"name": name, "passed": passed, "error": "" if passed else "assertion failed"}
    except Exception as exc:  # The failure type is evidence for the coding agent.
        return {"name": name, "passed": False, "error": f"{type(exc).__name__}: {exc}"}


def main() -> None:
    candidate = load_candidate()
    normal_results = [
        {"id": "semantic", "score": 0.9, "payload": {"text": "semantic"}},
        {"id": "hybrid", "score": 0.7, "payload": {"text": "hybrid"}},
    ]

    def normal_ranking_is_preserved():
        ranked = candidate.score_and_rank(
            normal_results,
            {"semantic": 0.1, "hybrid": 0.8},
            {},
            0.5,
            2,
        )
        return [item["id"] for item in ranked] == ["hybrid", "semantic"]

    def explanation_is_consistent():
        result = candidate.score_and_rank(
            [{"id": 7, "score": 0.8, "payload": None}],
            {"7": 0.4},
            {"7": 0.2},
            0.5,
            1,
            explain=True,
        )[0]
        details = result["score_details"]
        return result["id"] == "7" and math.isclose(details["final_score"], result["score"])

    cases = [
        run_case(
            "ordinary_bm25_contract",
            lambda: math.isclose(candidate.normalize_bm25(5.0, 5.0, 0.7), 0.5),
        ),
        run_case(
            "positive_extreme_bm25",
            lambda: candidate.normalize_bm25(1e308, 5.0, 0.7) == 1.0,
        ),
        run_case(
            "negative_extreme_bm25",
            lambda: candidate.normalize_bm25(-1e308, 5.0, 0.7) == 0.0,
        ),
        run_case("ordinary_ranking_contract", normal_ranking_is_preserved),
        run_case(
            "negative_top_k_is_empty",
            lambda: candidate.score_and_rank(normal_results, {}, {}, 0.0, -1) == [],
        ),
        run_case(
            "nan_semantic_score_is_excluded",
            lambda: candidate.score_and_rank(
                [{"id": "nan", "score": float("nan")}], {}, {}, 0.1, 5
            )
            == [],
        ),
        run_case(
            "infinite_semantic_score_is_excluded",
            lambda: candidate.score_and_rank(
                [{"id": "inf", "score": float("inf")}], {}, {}, 0.1, 5
            )
            == [],
        ),
        run_case(
            "missing_id_is_excluded",
            lambda: candidate.score_and_rank([{"score": 0.9}], {}, {}, 0.1, 5) == [],
        ),
        run_case("explanation_contract", explanation_is_consistent),
    ]
    passed = sum(case["passed"] for case in cases)
    payload = {
        "status": "ok",
        "metrics": {"robustness_score": passed / len(cases)},
        "passed_cases": passed,
        "total_cases": len(cases),
        "cases": cases,
    }
    print(json.dumps(payload, ensure_ascii=True, sort_keys=True))


if __name__ == "__main__":
    main()
