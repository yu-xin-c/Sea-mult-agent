"""Frozen deterministic evaluator for GraphRAG's hasher module."""

from __future__ import annotations

import hashlib
import importlib.util
import json
from pathlib import Path

import yaml


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
    spec = importlib.util.spec_from_file_location("graphrag_hasher_candidate", CANDIDATE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError("GraphRAG hasher candidate cannot be imported")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_case(name, check):
    try:
        passed = bool(check())
        return {"name": name, "passed": passed, "error": "" if passed else "assertion failed"}
    except Exception as exc:  # The failure type is evidence for the coding agent.
        return {"name": name, "passed": False, "error": f"{type(exc).__name__}: {exc}"}


def run_nested_hash_case(candidate, left, right):
    """Expose public intermediate evidence instead of a bare assertion."""
    try:
        left_yaml = yaml.dump(left, sort_keys=True)
        right_yaml = yaml.dump(right, sort_keys=True)
        left_canonical = candidate.make_yaml_serializable(left)
        right_canonical = candidate.make_yaml_serializable(right)
        left_hash = candidate.hash_data(left)
        right_hash = candidate.hash_data(right)
        passed = left_hash == right_hash
        return {
            "name": "nested_hash_order_invariance",
            "passed": passed,
            "error": "" if passed else "hash mismatch without a yaml.dump exception",
            "evidence": {
                "primary_yaml_equal": left_yaml == right_yaml,
                "canonical_values_equal": left_canonical == right_canonical,
                "hash_equal": passed,
                "left_primary_yaml": left_yaml,
                "right_primary_yaml": right_yaml,
            },
        }
    except Exception as exc:
        return {
            "name": "nested_hash_order_invariance",
            "passed": False,
            "error": f"{type(exc).__name__}: {exc}",
        }


def run_object_state_case(candidate):
    """Check fallback canonicalization without copying holdout combinations."""
    try:
        left_probe = Probe("same")
        right_probe = Probe("same")
        different_probe = Probe("different")
        left = candidate.make_yaml_serializable(left_probe)
        right = candidate.make_yaml_serializable(right_probe)
        different = candidate.make_yaml_serializable(different_probe)
        passed = left == right and left != different
        return {
            "name": "object_state_canonicalization",
            "passed": passed,
            "error": "" if passed else "equivalent object state is not canonical",
            "evidence": {
                "equivalent_states_equal": left == right,
                "distinct_states_differ": left != different,
            },
        }
    except Exception as exc:
        return {
            "name": "object_state_canonicalization",
            "passed": False,
            "error": f"{type(exc).__name__}: {exc}",
        }


def main() -> None:
    candidate = load_candidate()
    expected_sha = hashlib.sha256(b"abc").hexdigest()
    expected_ordinary_yaml = "a: 1\nb: 2\n"
    ordinary = {"b": 2, "a": 1}
    reverse_ordinary = {"a": 1, "b": 2}
    mixed_a = {"a": 1, 2: "b"}
    mixed_b = {2: "b", "a": 1}
    nested_a = {"outer": {"a": 1, 2: "b"}, "items": [3, 2, 1]}
    nested_b = {"items": [3, 2, 1], "outer": {2: "b", "a": 1}}
    cyclic = {"name": "root"}
    cyclic["self"] = cyclic
    alias_left_value = {"value": 1}
    alias_right_value = {"value": 1}
    alias_left = {"first": alias_left_value, "second": alias_left_value}
    alias_right = {"second": alias_right_value, "first": alias_right_value}

    cases = [
        run_case("sha256_contract", lambda: candidate.sha256_hasher("abc") == expected_sha),
        run_case(
            "ordinary_mapping_contract",
            lambda: candidate.make_yaml_serializable(ordinary)
            == candidate.make_yaml_serializable(reverse_ordinary)
            == (("a", "1"), ("b", "2")),
        ),
        run_case(
            "ordinary_hash_input_contract",
            lambda: candidate.hash_data(ordinary, hasher=lambda value: value)
            == expected_ordinary_yaml,
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
            lambda: candidate.make_yaml_serializable(mixed_a)
            == candidate.make_yaml_serializable(mixed_b),
        ),
        run_case(
            "nested_mixed_mapping_keys",
            lambda: candidate.make_yaml_serializable(nested_a)
            == candidate.make_yaml_serializable(nested_b),
        ),
        run_nested_hash_case(candidate, nested_a, nested_b),
        run_case(
            "cyclic_container_contract",
            lambda: isinstance(candidate.hash_data(cyclic), str),
        ),
        run_object_state_case(candidate),
        run_case(
            "ordinary_shared_alias_order_invariance",
            lambda: candidate.hash_data(alias_left) == candidate.hash_data(alias_right),
        ),
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
