"""Fresh model-hidden V2 holdout for GraphRAG recursive graph composition."""

from __future__ import annotations

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
    spec = importlib.util.spec_from_file_location("graphrag_hasher_v2_holdout", CANDIDATE_PATH)
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


def mutually_recursive_graph(value: str, reverse: bool):
    root = {}
    child = {}
    payload = {2: Probe(value), "a": 1}
    if reverse:
        root.update({"child": child, "payload": {"a": 1, 2: Probe(value)}})
        child.update({"tag": "child", "parent": root})
    else:
        root.update({"payload": payload, "child": child})
        child.update({"parent": root, "tag": "child"})
    return root


def list_mapping_cycle(reverse: bool):
    root = {}
    items = [root, {2: "b", "a": 1}]
    if reverse:
        root.update({"tag": "x", "items": [root, {"a": 1, 2: "b"}]})
    else:
        root.update({"items": items, "tag": "x"})
    return root


def shared_cycle(reverse: bool):
    root = {}
    shared = {2: Probe("same"), "a": 1}
    if reverse:
        shared = {"a": 1, 2: Probe("same")}
        root.update({"second": shared, "first": shared})
    else:
        root.update({"first": shared, "second": shared})
    shared["root"] = root
    return root


def main() -> None:
    candidate = load_candidate()
    cases = [
        run_case(
            "equivalent_mutually_recursive_graphs",
            lambda: candidate.hash_data(mutually_recursive_graph("same", False))
            == candidate.hash_data(mutually_recursive_graph("same", True)),
        ),
        run_case(
            "distinct_recursive_object_state",
            lambda: candidate.hash_data(mutually_recursive_graph("left", False))
            != candidate.hash_data(mutually_recursive_graph("right", True)),
        ),
        run_case(
            "list_mapping_cross_cycle",
            lambda: candidate.hash_data(list_mapping_cycle(False))
            == candidate.hash_data(list_mapping_cycle(True)),
        ),
        run_case(
            "shared_alias_inside_recursive_graph",
            lambda: candidate.hash_data(shared_cycle(False))
            == candidate.hash_data(shared_cycle(True)),
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
