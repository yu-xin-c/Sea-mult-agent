#!/usr/bin/env python3
"""Run and validate the Attention paper-reproduction example through ScholarAgent."""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


PROMPT = """请使用 https://github.com/harvardnlp/annotated-transformer 复现
Attention Is All You Need，使用 smoke 模式运行轻量注意力消融，
不要执行 WMT14 完整训练。"""

EXPECTED_REPOSITORY = "https://github.com/harvardnlp/annotated-transformer"
EXPECTED_ARTIFACTS = {"repo_url", "run_metrics", "comparison_report"}
EXPECTED_EVENTS = {"plan_started", "artifact_created", "plan_completed"}
TERMINAL_STATUSES = {"completed", "failed", "blocked", "cancelled", "canceled"}


def request_json(
    base_url: str,
    method: str,
    path: str,
    payload: dict[str, Any] | None = None,
    timeout: float = 30.0,
) -> dict[str, Any]:
    data = None if payload is None else json.dumps(payload).encode("utf-8")
    request = Request(
        base_url.rstrip("/") + path,
        data=data,
        method=method,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urlopen(request, timeout=timeout) as response:
            return json.load(response)
    except HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} returned HTTP {exc.code}: {body}") from exc
    except URLError as exc:
        raise RuntimeError(f"cannot reach {base_url}: {exc.reason}") from exc
    except TimeoutError as exc:
        raise RuntimeError(f"{method} {path} timed out after {timeout:.0f}s") from exc


def artifact_value(artifacts: dict[str, Any], key: str) -> str:
    artifact = artifacts.get(key, {})
    if isinstance(artifact, dict):
        return str(artifact.get("value", "")).strip()
    return ""


def run_example(base_url: str, timeout: float, poll_interval: float) -> dict[str, Any]:
    health = request_json(base_url, "GET", "/api/health")
    if health.get("ok") is not True:
        raise RuntimeError(f"service health check failed: {json.dumps(health, ensure_ascii=False)}")

    created = request_json(base_url, "POST", "/api/plan", {"intent": PROMPT})
    plan = created.get("plan_graph")
    if not isinstance(plan, dict) or not plan.get("id"):
        raise RuntimeError("POST /api/plan did not return plan_graph.id")
    if plan.get("intent_type") != "Paper_Reproduction":
        raise RuntimeError(f"unexpected intent type: {plan.get('intent_type')!r}")

    plan_id = str(plan["id"])
    print(f"plan_id={plan_id} nodes={len(plan.get('nodes', []))}", flush=True)
    request_json(base_url, "POST", f"/api/plans/{plan_id}/execute", {})

    deadline = time.monotonic() + timeout
    previous_progress: tuple[Any, ...] | None = None
    while time.monotonic() < deadline:
        current = request_json(base_url, "GET", f"/api/plans/{plan_id}").get("plan_graph", {})
        meta = current.get("meta", {})
        progress = (
            current.get("status"),
            meta.get("completed_nodes"),
            meta.get("failed_nodes"),
            meta.get("in_progress_nodes"),
        )
        if progress != previous_progress:
            print(
                "status={} completed={} failed={} running={}".format(*progress),
                flush=True,
            )
            previous_progress = progress
        if current.get("status") in TERMINAL_STATUSES:
            plan = current
            break
        time.sleep(poll_interval)
    else:
        raise RuntimeError(f"plan {plan_id} did not finish within {timeout:.0f}s")

    if plan.get("status") != "completed":
        failures = [
            {"name": node.get("name"), "status": node.get("status"), "error": node.get("error")}
            for node in plan.get("nodes", [])
            if node.get("status") != "completed"
        ]
        raise RuntimeError(f"plan ended with status {plan.get('status')}: {failures}")

    meta = plan.get("meta", {})
    nodes = plan.get("nodes", [])
    if meta.get("completed_nodes") != len(nodes):
        raise RuntimeError(f"only {meta.get('completed_nodes')} of {len(nodes)} nodes completed")

    artifacts = plan.get("artifacts", {})
    missing_artifacts = sorted(EXPECTED_ARTIFACTS - set(artifacts))
    if missing_artifacts:
        raise RuntimeError(f"missing expected artifacts: {missing_artifacts}")

    selected_repository = artifact_value(artifacts, "repo_url").removesuffix(".git")
    if selected_repository != EXPECTED_REPOSITORY:
        raise RuntimeError(f"unexpected repository: {selected_repository!r}")
    if not artifact_value(artifacts, "run_metrics"):
        raise RuntimeError("run_metrics artifact is empty")
    if not artifact_value(artifacts, "comparison_report"):
        raise RuntimeError("comparison_report artifact is empty")

    event_response = request_json(base_url, "GET", f"/api/plans/{plan_id}/events")
    events = event_response.get("events", [])
    event_types = {event.get("event_type") for event in events}
    missing_events = sorted(EXPECTED_EVENTS - event_types)
    if missing_events:
        raise RuntimeError(f"missing expected events: {missing_events}")

    return {
        "example": "attention-paper-reproduction",
        "status": "passed",
        "plan_id": plan_id,
        "intent_type": plan.get("intent_type"),
        "repository": selected_repository,
        "node_count": len(nodes),
        "completed_nodes": meta.get("completed_nodes"),
        "artifact_count": len(artifacts),
        "artifact_keys": sorted(artifacts),
        "event_count": len(events),
        "event_types": sorted(event_type for event_type in event_types if event_type),
        "scope": "smoke attention ablation; no WMT14 training or BLEU reproduction",
    }


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--base-url",
        default=os.environ.get("SCHOLAR_API_URL", "http://localhost:8080"),
        help="ScholarAgent backend URL (default: SCHOLAR_API_URL or http://localhost:8080)",
    )
    parser.add_argument("--timeout", type=float, default=2100, help="overall execution timeout in seconds")
    parser.add_argument("--poll-interval", type=float, default=2, help="plan polling interval in seconds")
    parser.add_argument("--output", type=Path, help="optional path for the JSON summary")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        summary = run_example(args.base_url, args.timeout, args.poll_interval)
    except (RuntimeError, ValueError, json.JSONDecodeError) as exc:
        print(f"FAILED: {exc}", file=sys.stderr)
        return 1

    rendered = json.dumps(summary, ensure_ascii=False, indent=2)
    print(rendered)
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(rendered + "\n", encoding="utf-8")
        print(f"summary written to {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
