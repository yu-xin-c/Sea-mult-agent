"""Model-hidden composition holdout for Tenacity wait strategy calls."""

from __future__ import annotations

import json
from types import SimpleNamespace

from tenacity import wait as candidate


class RenamedStateWait(candidate.wait_base):
    def __init__(self, scale: float) -> None:
        self.scale = scale

    def __call__(self, call_state, /) -> float:
        return self.scale * max(call_state.attempt_number, 1)


def run_case(name, check):
    try:
        passed = bool(check())
        return {"name": name, "passed": passed, "error": "" if passed else "assertion failed"}
    except Exception as exc:
        return {"name": name, "passed": False, "error": f"{type(exc).__name__}: {exc}"}


def main() -> None:
    before_first = SimpleNamespace(attempt_number=0)
    middle = SimpleNamespace(attempt_number=2)
    after_end = SimpleNamespace(attempt_number=20)
    cases = [
        run_case(
            "custom_chain_clamps_before_first_attempt",
            lambda: candidate.wait_chain(RenamedStateWait(2), RenamedStateWait(5))(before_first) == 2,
        ),
        run_case(
            "custom_chain_selects_middle_strategy",
            lambda: candidate.wait_chain(RenamedStateWait(2), RenamedStateWait(5))(middle) == 10,
        ),
        run_case(
            "custom_chain_reuses_final_strategy",
            lambda: candidate.wait_chain(RenamedStateWait(2), RenamedStateWait(5))(after_end) == 100,
        ),
        run_case(
            "custom_chain_composes_with_plain_callable",
            lambda: candidate.wait_combine(
                candidate.wait_chain(RenamedStateWait(3)),
                lambda state, /: state.attempt_number,
            )(middle)
            == 8,
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
