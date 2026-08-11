"""Model-hidden composition holdout for LightRAG rerank aggregation."""

from __future__ import annotations

import json
import math
import importlib.util
from pathlib import Path


def load_public_helpers():
    path = Path(__file__).with_name("02-evaluator.py")
    spec = importlib.util.spec_from_file_location("lightrag_public_helpers", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("public evaluator helpers cannot be imported")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main() -> None:
    helpers = load_public_helpers()
    candidate = helpers.load_candidate()
    duplicate_mix = [
        {"index": 0, "relevance_score": 0.3},
        {"index": 0, "relevance_score": 0.9},
        {"index": 1, "relevance_score": float("inf")},
        {"index": 2, "relevance_score": 0.5},
    ]
    reverse_mix = list(reversed(duplicate_mix))
    cases = [
        helpers.run_case(
            "duplicate_and_non_finite_composition",
            lambda: math.isclose(
                helpers.score_of(candidate.aggregate_chunk_scores(duplicate_mix, [0, 0, 0], 1, "mean")),
                0.7,
            ),
        ),
        helpers.run_case(
            "duplicate_order_invariance",
            lambda: candidate.aggregate_chunk_scores(duplicate_mix, [0, 0, 0], 1, "mean")
            == candidate.aggregate_chunk_scores(reverse_mix, [0, 0, 0], 1, "mean"),
        ),
        helpers.run_case(
            "boolean_mapping_does_not_alias_integer_document",
            lambda: candidate.aggregate_chunk_scores(
                [{"index": 0, "relevance_score": 0.7}], [False], 1, "max"
            )
            == [],
        ),
        helpers.run_case(
            "strict_aggregation_survives_composed_input",
            lambda: helpers.unknown_aggregation_rejected(candidate),
        ),
    ]
    passed = sum(case["passed"] for case in cases)
    print(
        json.dumps(
            {
                "status": "ok",
                "metrics": {"aggregation_score": passed / len(cases)},
                "passed_cases": passed,
                "total_cases": len(cases),
                "cases": cases,
            },
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
