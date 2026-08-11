from __future__ import annotations

import hashlib
import json
import math
import os
import random
import time
from pathlib import Path
from typing import Any, Iterable


LABELS = [
    "Framework_Evaluation",
    "Paper_Reproduction",
    "Code_Execution",
    "General",
]
LABEL2ID = {label: index for index, label in enumerate(LABELS)}
ID2LABEL = {index: label for label, index in LABEL2ID.items()}


def seed_everything(seed: int) -> None:
    import torch

    random.seed(seed)
    os.environ["PYTHONHASHSEED"] = str(seed)
    torch.manual_seed(seed)
    if torch.cuda.is_available():
        torch.cuda.manual_seed_all(seed)


def read_jsonl(path: str | Path) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    with Path(path).open("r", encoding="utf-8") as handle:
        for line_number, line in enumerate(handle, start=1):
            if not line.strip():
                continue
            item = json.loads(line)
            if item.get("label") not in LABEL2ID:
                raise ValueError(f"{path}:{line_number}: unsupported label {item.get('label')!r}")
            records.append(item)
    return records


def write_json(path: str | Path, value: Any) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("w", encoding="utf-8") as handle:
        json.dump(value, handle, ensure_ascii=False, indent=2, sort_keys=True)
        handle.write("\n")


def write_jsonl(path: str | Path, records: Iterable[dict[str, Any]]) -> None:
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("w", encoding="utf-8") as handle:
        for record in records:
            handle.write(json.dumps(record, ensure_ascii=False, sort_keys=True) + "\n")


def file_sha256(path: str | Path) -> str:
    digest = hashlib.sha256()
    with Path(path).open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def percentile(values: list[float], q: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    position = (len(ordered) - 1) * q
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return ordered[lower]
    return ordered[lower] + (ordered[upper] - ordered[lower]) * (position - lower)


def classification_metrics(expected: list[str], predicted: list[str]) -> dict[str, Any]:
    if len(expected) != len(predicted):
        raise ValueError("expected and predicted lengths differ")
    confusion = {label: {candidate: 0 for candidate in LABELS} for label in LABELS}
    unknown_by_expected = {label: 0 for label in LABELS}
    unknown_predictions = 0
    for gold, guess in zip(expected, predicted):
        if guess in LABEL2ID:
            confusion[gold][guess] += 1
        else:
            unknown_predictions += 1
            unknown_by_expected[gold] += 1

    per_class: dict[str, dict[str, float | int]] = {}
    f1_values: list[float] = []
    correct = 0
    for label in LABELS:
        tp = confusion[label][label]
        correct += tp
        fp = sum(confusion[other][label] for other in LABELS if other != label)
        fn = sum(confusion[label][other] for other in LABELS if other != label) + unknown_by_expected[label]
        support = sum(confusion[label].values()) + unknown_by_expected[label]
        precision = tp / (tp + fp) if tp + fp else 0.0
        recall = tp / (tp + fn) if tp + fn else 0.0
        f1 = 2 * precision * recall / (precision + recall) if precision + recall else 0.0
        f1_values.append(f1)
        per_class[label] = {
            "precision": precision,
            "recall": recall,
            "f1": f1,
            "support": support,
        }

    total = len(expected)
    return {
        "accuracy": correct / total if total else 0.0,
        "macro_f1": sum(f1_values) / len(f1_values),
        "unknown_predictions": unknown_predictions,
        "unknown_by_expected": unknown_by_expected,
        "per_class": per_class,
        "confusion_matrix": confusion,
    }


class Timer:
    def __enter__(self) -> "Timer":
        self.started = time.perf_counter()
        self.elapsed = 0.0
        return self

    def __exit__(self, *_: object) -> None:
        self.elapsed = time.perf_counter() - self.started
