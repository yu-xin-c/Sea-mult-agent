"""Model-hidden holdout for GraphRAG hasher generalization."""

from __future__ import annotations

import importlib.util
import json
from pathlib import Path


WORKSPACE = Path.cwd()
CANDIDATE_PATH = (
    WORKSPACE
    / "packages"
    / "graphrag-common"
    / "graphrag_common"
    / "hasher"
    / "hasher.py"
)


class Probe:
    def __init__(self, value: str) -> None:
        self.value = value


def load_candidate():
    spec = importlib.util.spec_from_file_location("graphrag_hasher_holdout", CANDIDATE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("GraphRAG hasher candidate cannot be imported")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_case(name, check):
    try:
        passed = bool(check())
        return {"name": name, "passed": passed, "error": "" if passed else "assertion failed"}
    except Exception as exc:
        return {"name": name, "passed": False, "error": f"{type(exc).__name__}: {exc}"}


def equivalent_object_hashes(candidate) -> bool:
    left = {"payload": {2: Probe("same"), "a": 1}, "tag": "x"}
    right = {"tag": "x", "payload": {"a": 1, 2: Probe("same")}}
    return candidate.hash_data(left) == candidate.hash_data(right)


def distinct_object_hashes(candidate) -> bool:
    left = {"payload": {2: Probe("left"), "a": 1}}
    right = {"payload": {"a": 1, 2: Probe("right")}}
    return candidate.hash_data(left) != candidate.hash_data(right)


def equivalent_cycles(candidate) -> bool:
    left = {"payload": {2: "b", "a": 1}}
    right = {"payload": {"a": 1, 2: "b"}}
    left["self"] = left
    right["self"] = right
    return candidate.hash_data(left) == candidate.hash_data(right)


def alias_contract(candidate) -> bool:
    shared_left = {2: Probe("same"), "a": 1}
    shared_right = {"a": 1, 2: Probe("same")}
    left = {"first": shared_left, "second": shared_left}
    right = {"second": shared_right, "first": shared_right}
    return candidate.hash_data(left) == candidate.hash_data(right)


def main() -> None:
    candidate = load_candidate()
    cases = [
        run_case("mixed_mapping_equivalent_objects", lambda: equivalent_object_hashes(candidate)),
        run_case("mixed_mapping_distinct_object_state", lambda: distinct_object_hashes(candidate)),
        run_case("equivalent_recursive_mappings", lambda: equivalent_cycles(candidate)),
        run_case("shared_alias_with_mixed_mapping", lambda: alias_contract(candidate)),
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
