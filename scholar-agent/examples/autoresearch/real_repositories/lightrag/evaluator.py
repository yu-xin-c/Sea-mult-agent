"""Public development evaluator for LightRAG rerank aggregation."""

from __future__ import annotations

import importlib.util
import json
import math
import sys
import types
from pathlib import Path


CANDIDATE_PATH = Path.cwd() / "lightrag" / "rerank.py"


class NullLogger:
    def __getattr__(self, _name):
        return lambda *_args, **_kwargs: None


class RetryPredicate:
    def __or__(self, _other):
        return self


def install_import_stubs() -> None:
    package = types.ModuleType("lightrag")
    package.__path__ = []
    sys.modules["lightrag"] = package

    utils = types.ModuleType("lightrag.utils")
    utils.logger = NullLogger()

    def normalize_rerank_result(result, size):
        if not isinstance(result, dict):
            return None, "not a mapping"
        index = result.get("index")
        score = result.get("relevance_score")
        if isinstance(index, bool) or not isinstance(index, int) or not 0 <= index < size:
            return None, "invalid index"
        if isinstance(score, bool) or not isinstance(score, (int, float)):
            return None, "invalid score"
        return {"index": index, "relevance_score": float(score)}, None

    async def run_in_tokenizer_executor(function, *args):
        return function(*args)

    utils.normalize_rerank_result = normalize_rerank_result
    utils.run_in_tokenizer_executor = run_in_tokenizer_executor
    sys.modules["lightrag.utils"] = utils

    aiohttp = types.ModuleType("aiohttp")
    aiohttp.ClientError = type("ClientError", (Exception,), {})
    aiohttp.ClientResponseError = type("ClientResponseError", (aiohttp.ClientError,), {})
    sys.modules["aiohttp"] = aiohttp

    dotenv = types.ModuleType("dotenv")
    dotenv.load_dotenv = lambda *_args, **_kwargs: None
    sys.modules["dotenv"] = dotenv

    tenacity = types.ModuleType("tenacity")
    tenacity.retry = lambda *_args, **_kwargs: lambda function: function
    tenacity.retry_if_exception_type = lambda *_args, **_kwargs: RetryPredicate()
    tenacity.stop_after_attempt = lambda *_args, **_kwargs: None
    tenacity.wait_exponential = lambda *_args, **_kwargs: None
    sys.modules["tenacity"] = tenacity


def load_candidate():
    install_import_stubs()
    spec = importlib.util.spec_from_file_location("lightrag.rerank", CANDIDATE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("LightRAG rerank candidate cannot be imported")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def run_case(name, check):
    try:
        passed = bool(check())
        return {"name": name, "passed": passed, "error": "" if passed else "assertion failed"}
    except Exception as exc:
        return {"name": name, "passed": False, "error": f"{type(exc).__name__}: {exc}"}


def unknown_aggregation_rejected(candidate) -> bool:
    try:
        candidate.aggregate_chunk_scores(
            [{"index": 0, "relevance_score": 0.5}], [0], 1, "mystery"
        )
    except ValueError:
        return True
    return False


def score_of(results) -> float:
    return results[0]["relevance_score"] if results else math.nan


def main() -> None:
    candidate = load_candidate()
    ordinary = [
        {"index": 0, "relevance_score": 0.2},
        {"index": 1, "relevance_score": 0.8},
        {"index": 2, "relevance_score": 0.5},
    ]
    cases = [
        run_case(
            "ordinary_max_contract",
            lambda: candidate.aggregate_chunk_scores(ordinary, [0, 0, 1], 2, "max")
            == [
                {"index": 0, "relevance_score": 0.8},
                {"index": 1, "relevance_score": 0.5},
            ],
        ),
        run_case(
            "ordinary_mean_contract",
            lambda: math.isclose(
                score_of(candidate.aggregate_chunk_scores(ordinary[:2], [0, 0], 1, "mean")),
                0.5,
            ),
        ),
        run_case(
            "ordinary_first_contract",
            lambda: score_of(candidate.aggregate_chunk_scores(ordinary[:2], [0, 0], 1, "first")) == 0.2,
        ),
        run_case(
            "malformed_result_is_ignored",
            lambda: candidate.aggregate_chunk_scores([{"index": "0", "relevance_score": 1}], [0], 1) == [],
        ),
        run_case(
            "duplicate_chunk_indices_are_deduplicated",
            lambda: math.isclose(
                score_of(
                    candidate.aggregate_chunk_scores(
                        [
                            {"index": 0, "relevance_score": 0.2},
                            {"index": 0, "relevance_score": 0.8},
                            {"index": 1, "relevance_score": 0.4},
                        ],
                        [0, 0],
                        1,
                        "mean",
                    )
                ),
                0.6,
            ),
        ),
        run_case(
            "non_finite_scores_are_ignored",
            lambda: score_of(
                candidate.aggregate_chunk_scores(
                    [
                        {"index": 0, "relevance_score": float("nan")},
                        {"index": 1, "relevance_score": 0.5},
                    ],
                    [0, 0],
                    1,
                    "max",
                )
            )
            == 0.5,
        ),
        run_case(
            "boolean_document_indices_are_ignored",
            lambda: candidate.aggregate_chunk_scores(
                [{"index": 0, "relevance_score": 0.9}], [True], 2
            )
            == [],
        ),
        run_case(
            "unknown_aggregation_is_rejected",
            lambda: unknown_aggregation_rejected(candidate),
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
