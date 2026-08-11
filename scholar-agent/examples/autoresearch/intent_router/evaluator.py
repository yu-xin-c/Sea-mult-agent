"""Frozen, standard-library evaluator for the intent-router example."""

from __future__ import annotations

import hashlib
import importlib.util
import json
import statistics
import time
from pathlib import Path


ROOT = Path(__file__).resolve().parent
CANDIDATE_PATH = ROOT / "candidate.py"
BENCHMARK_PATH = ROOT / "benchmark.json"
LABELS = (
    "AutoResearch",
    "Paper_Reproduction",
    "Custom_Benchmark",
    "Framework_Evaluation",
    "Code_Execution",
    "General",
)


def load_candidate():
    spec = importlib.util.spec_from_file_location("autoresearch_candidate", CANDIDATE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("candidate.py cannot be imported")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    if not callable(getattr(module, "classify", None)):
        raise RuntimeError("candidate.py must expose classify(text)")
    return module.classify


def macro_f1(expected: list[str], predicted: list[str]) -> float:
    scores = []
    for label in LABELS:
        true_positive = sum(e == label and p == label for e, p in zip(expected, predicted))
        false_positive = sum(e != label and p == label for e, p in zip(expected, predicted))
        false_negative = sum(e == label and p != label for e, p in zip(expected, predicted))
        precision = true_positive / (true_positive + false_positive) if true_positive + false_positive else 0.0
        recall = true_positive / (true_positive + false_negative) if true_positive + false_negative else 0.0
        scores.append(2 * precision * recall / (precision + recall) if precision + recall else 0.0)
    return sum(scores) / len(scores)


def percentile_95(values: list[float]) -> float:
    if len(values) == 1:
        return values[0]
    return statistics.quantiles(values, n=100, method="inclusive")[94]


def main() -> None:
    records = json.loads(BENCHMARK_PATH.read_text(encoding="utf-8"))
    classify = load_candidate()
    expected: list[str] = []
    predicted: list[str] = []
    latencies_ms: list[float] = []

    for record in records:
        started = time.perf_counter_ns()
        prediction = classify(record["text"])
        latencies_ms.append((time.perf_counter_ns() - started) / 1_000_000)
        if prediction not in LABELS:
            raise RuntimeError(f"unsupported intent label: {prediction!r}")
        expected.append(record["label"])
        predicted.append(prediction)

    accuracy = sum(e == p for e, p in zip(expected, predicted)) / len(records)
    metrics = {
        "accuracy": accuracy,
        "macro_f1": macro_f1(expected, predicted),
        "p95_latency_ms": percentile_95(latencies_ms),
    }
    payload = {
        "status": "ok",
        "sample_count": len(records),
        "metrics": metrics,
        "dataset_sha256": hashlib.sha256(BENCHMARK_PATH.read_bytes()).hexdigest(),
        "candidate_sha256": hashlib.sha256(CANDIDATE_PATH.read_bytes()).hexdigest(),
    }
    print(json.dumps(payload, ensure_ascii=True, sort_keys=True))


if __name__ == "__main__":
    main()
