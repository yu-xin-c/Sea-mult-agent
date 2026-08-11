"""Public V2 evaluator for GraphRAG hasher graph canonicalization."""

from __future__ import annotations

import hashlib
import importlib.util
import json
from pathlib import Path


CANDIDATE_PATH = (
    Path.cwd()
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
    spec = importlib.util.spec_from_file_location("graphrag_hasher_v2", CANDIDATE_PATH)
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


def equivalent_cycles(candidate) -> bool:
    left = {"payload": {2: "b", "a": 1}}
    right = {"payload": {"a": 1, 2: "b"}}
    left["self"] = left
    right["self"] = right
    return candidate.hash_data(left) == candidate.hash_data(right)


def object_state_contract(candidate) -> bool:
    return (
        candidate.make_yaml_serializable(Probe("same"))
        == candidate.make_yaml_serializable(Probe("same"))
        != candidate.make_yaml_serializable(Probe("different"))
    )


def main() -> None:
    candidate = load_candidate()
    ordinary_a = {"b": 2, "a": 1}
    ordinary_b = {"a": 1, "b": 2}
    mixed_a = {"a": 1, 2: "b"}
    mixed_b = {2: "b", "a": 1}
    nested_a = {"outer": mixed_a, "items": [3, 2, 1]}
    nested_b = {"items": [3, 2, 1], "outer": mixed_b}
    cycle = {"name": "root"}
    cycle["self"] = cycle
    shared_a = {2: Probe("same"), "a": 1}
    shared_b = {"a": 1, 2: Probe("same")}
    aliases_a = {"first": shared_a, "second": shared_a}
    aliases_b = {"second": shared_b, "first": shared_b}

    cases = [
        run_case("sha256_contract", lambda: candidate.sha256_hasher("abc") == hashlib.sha256(b"abc").hexdigest()),
        run_case(
            "ordinary_mapping_contract",
            lambda: candidate.make_yaml_serializable(ordinary_a)
            == candidate.make_yaml_serializable(ordinary_b)
            == (("a", "1"), ("b", "2")),
        ),
        run_case(
            "ordinary_hash_input_contract",
            lambda: candidate.hash_data(ordinary_a, hasher=lambda value: value) == "a: 1\nb: 2\n",
        ),
        run_case(
            "sequence_contract",
            lambda: candidate.make_yaml_serializable([1, "x", None]) == ("1", "x", "None"),
        ),
        run_case(
            "heterogeneous_set_contract",
            lambda: candidate.make_yaml_serializable({2, "a"}) == ("2", "a"),
        ),
        run_case(
            "mixed_mapping_keys",
            lambda: candidate.make_yaml_serializable(mixed_a) == candidate.make_yaml_serializable(mixed_b),
        ),
        run_case(
            "nested_mixed_mapping_keys",
            lambda: candidate.make_yaml_serializable(nested_a) == candidate.make_yaml_serializable(nested_b),
        ),
        run_case(
            "nested_hash_order_invariance",
            lambda: candidate.hash_data(nested_a) == candidate.hash_data(nested_b),
        ),
        run_case("cyclic_container_contract", lambda: isinstance(candidate.hash_data(cycle), str)),
        run_case("object_state_canonicalization", lambda: object_state_contract(candidate)),
        run_case(
            "shared_alias_order_invariance",
            lambda: candidate.hash_data(aliases_a) == candidate.hash_data(aliases_b),
        ),
        run_case(
            "spent_mixed_recursive_mapping_contract",
            lambda: equivalent_cycles(candidate),
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
