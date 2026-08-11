"""Public development evaluator for Tenacity wait strategy call semantics."""

from __future__ import annotations

import json
from types import SimpleNamespace

from tenacity import wait as candidate


class PositionalOnlyWait(candidate.wait_base):
    def __init__(self, value: float) -> None:
        self.value = value

    def __call__(self, state, /) -> float:
        return self.value + state.attempt_number


def run_case(name, check):
    try:
        passed = bool(check())
        return {"name": name, "passed": passed, "error": "" if passed else "assertion failed"}
    except Exception as exc:
        return {"name": name, "passed": False, "error": f"{type(exc).__name__}: {exc}"}


def empty_chain_rejected() -> bool:
    try:
        candidate.wait_chain()
    except ValueError:
        return True
    return False


def main() -> None:
    state1 = SimpleNamespace(attempt_number=1)
    state3 = SimpleNamespace(attempt_number=3)
    chain = candidate.wait_chain(candidate.wait_fixed(1), candidate.wait_fixed(4))
    cases = [
        run_case("fixed_wait_contract", lambda: candidate.wait_fixed(2)(state1) == 2),
        run_case("chain_first_strategy", lambda: chain(state1) == 1),
        run_case("chain_reuses_last_strategy", lambda: chain(state3) == 4),
        run_case(
            "sum_identity_for_wait_strategies",
            lambda: sum([candidate.wait_fixed(2), candidate.wait_fixed(3)])(state1) == 5,
        ),
        run_case(
            "combine_calls_plain_callable_positionally",
            lambda: candidate.wait_combine(lambda state, /: state.attempt_number, candidate.wait_fixed(2))(state3) == 5,
        ),
        run_case("empty_chain_is_rejected", empty_chain_rejected),
        run_case(
            "chain_calls_custom_strategy_positionally",
            lambda: candidate.wait_chain(PositionalOnlyWait(10))(state3) == 13,
        ),
    ]
    passed = sum(case["passed"] for case in cases)
    print(
        json.dumps(
            {
                "status": "ok",
                "metrics": {"protocol_score": passed / len(cases)},
                "passed_cases": passed,
                "total_cases": len(cases),
                "cases": cases,
            },
            sort_keys=True,
        )
    )


if __name__ == "__main__":
    main()
