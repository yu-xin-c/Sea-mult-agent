#!/usr/bin/env python3
"""Run one AutoResearch task package through the ScholarAgent product API."""

from __future__ import annotations

import argparse
import json
import mimetypes
import os
import sys
import time
import uuid
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


TERMINAL_STATUSES = {"completed", "failed", "blocked", "cancelled", "canceled"}
PACKAGE_FILES = ("autoresearch.json", "evaluator.py", "holdout_evaluator.py")


def request_json(
    base_url: str,
    method: str,
    path: str,
    user_id: str,
    session_id: str,
    payload: dict[str, Any] | None = None,
    body: bytes | None = None,
    content_type: str = "application/json",
    timeout: float = 60,
) -> dict[str, Any]:
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
    request = Request(
        base_url.rstrip("/") + path,
        data=body,
        method=method,
        headers={
            "Content-Type": content_type,
            "X-User-Id": user_id,
            "X-Session-Id": session_id,
        },
    )
    try:
        with urlopen(request, timeout=timeout) as response:
            return json.load(response)
    except HTTPError as exc:
        response_body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} returned HTTP {exc.code}: {response_body}") from exc
    except URLError as exc:
        raise RuntimeError(f"cannot reach {base_url}: {exc.reason}") from exc


def multipart_file(path: Path) -> tuple[bytes, str]:
    boundary = "scholar-" + uuid.uuid4().hex
    content_type = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
    body = b"".join(
        [
            f"--{boundary}\r\n".encode(),
            f'Content-Disposition: form-data; name="file"; filename="{path.name}"\r\n'.encode(),
            f"Content-Type: {content_type}\r\n\r\n".encode(),
            path.read_bytes(),
            f"\r\n--{boundary}--\r\n".encode(),
        ]
    )
    return body, f"multipart/form-data; boundary={boundary}"


def upload_package(base_url: str, package: Path, user_id: str, session_id: str) -> list[str]:
    upload_ids = []
    for name in PACKAGE_FILES:
        path = package / name
        if not path.is_file():
            raise RuntimeError(f"task package is missing {path}")
        body, content_type = multipart_file(path)
        response = request_json(
            base_url,
            "POST",
            "/api/uploads",
            user_id,
            session_id,
            body=body,
            content_type=content_type,
        )
        upload_id = str(response.get("id", "")).strip()
        if not upload_id:
            raise RuntimeError(f"upload response for {name} has no id")
        upload_ids.append(upload_id)
        print(f"uploaded={name} id={upload_id}", flush=True)
    return upload_ids


def artifact_json(plan: dict[str, Any], key: str) -> dict[str, Any]:
    artifact = plan.get("artifacts", {}).get(key, {})
    value = artifact.get("value", "") if isinstance(artifact, dict) else ""
    if not isinstance(value, str) or not value.strip():
        return {}
    try:
        parsed = json.loads(value)
    except json.JSONDecodeError:
        return {}
    return parsed if isinstance(parsed, dict) else {}


def run_campaign(args: argparse.Namespace) -> dict[str, Any]:
    health = request_json(args.base_url, "GET", "/api/health", args.user_id, args.session_id)
    if health.get("ok") is not True:
        raise RuntimeError(f"service health check failed: {health}")

    upload_ids = upload_package(args.base_url, args.package, args.user_id, args.session_id)
    intent = (
        f"用 {args.repository} 做 AutoResearch，使用上传的 autoresearch.json，"
        f"最多 {args.max_trials} 次实验，总时长 {args.wall_minutes} 分钟，"
        f"模型不可见 holdout 验收 {args.validation_runs} 次。"
    )
    created = request_json(
        args.base_url,
        "POST",
        "/api/plan",
        args.user_id,
        args.session_id,
        payload={"intent": intent, "attachments": upload_ids},
    )
    plan = created.get("plan_graph")
    if not isinstance(plan, dict) or not plan.get("id"):
        raise RuntimeError("POST /api/plan did not return plan_graph.id")
    if plan.get("intent_type") != "AutoResearch":
        raise RuntimeError(f"unexpected intent type: {plan.get('intent_type')!r}")
    plan_id = str(plan["id"])
    print(f"plan_id={plan_id} nodes={len(plan.get('nodes', []))}", flush=True)
    request_json(
        args.base_url,
        "POST",
        f"/api/plans/{plan_id}/execute",
        args.user_id,
        args.session_id,
        payload={},
    )

    deadline = time.monotonic() + args.timeout
    previous_progress = None
    while time.monotonic() < deadline:
        response = request_json(
            args.base_url,
            "GET",
            f"/api/plans/{plan_id}",
            args.user_id,
            args.session_id,
        )
        current = response.get("plan_graph", {})
        meta = current.get("meta", {})
        progress = (
            current.get("status"),
            meta.get("completed_nodes"),
            meta.get("failed_nodes"),
            meta.get("in_progress_nodes"),
        )
        if progress != previous_progress:
            print("status={} completed={} failed={} running={}".format(*progress), flush=True)
            previous_progress = progress
        if current.get("status") in TERMINAL_STATUSES:
            plan = current
            break
        time.sleep(args.poll_interval)
    else:
        raise RuntimeError(f"plan {plan_id} did not finish within {args.timeout:.0f}s")

    output = {"plan_graph": plan}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(output, ensure_ascii=False, separators=(",", ":")) + "\n", encoding="utf-8")

    manifest = artifact_json(plan, "repo_manifest")
    ledger = artifact_json(plan, "research_trial_ledger")
    validation = artifact_json(plan, "research_validation_report")
    summary = {
        "plan_id": plan_id,
        "plan_status": plan.get("status"),
        "requested_revision": manifest.get("requested_revision"),
        "repository_commit": manifest.get("repository_commit"),
        "acquisition_method": manifest.get("acquisition_method"),
        "search": {
            "baseline": ledger.get("baseline_score"),
            "best": ledger.get("best_score"),
            "target": ledger.get("target_score"),
            "stop_reason": ledger.get("stop_reason"),
            "completed_trials": ledger.get("completed_trials"),
            "accepted_trials": ledger.get("accepted_trials"),
            "search_runs": ledger.get("search_runs"),
            "search_aggregation": ledger.get("search_aggregation"),
        },
        "validation": {
            "status": validation.get("status"),
            "mode": validation.get("validation_mode"),
            "baseline": validation.get("holdout_baseline_score"),
            "mean": validation.get("mean_score"),
            "passed_runs": validation.get("passed_runs"),
            "requested_runs": validation.get("requested_runs"),
        },
        "output": str(args.output),
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return output


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True, help="public GitHub repository URL")
    parser.add_argument("--package", type=Path, required=True, help="directory containing the three task-package files")
    parser.add_argument("--output", type=Path, required=True, help="path for the complete plan_graph JSON")
    parser.add_argument("--base-url", default=os.environ.get("SCHOLAR_API_URL", "http://localhost:8080"))
    parser.add_argument("--user-id", default="autoresearch-example-runner")
    parser.add_argument("--session-id", default="autoresearch-" + uuid.uuid4().hex[:12])
    parser.add_argument("--max-trials", type=int, default=8)
    parser.add_argument("--wall-minutes", type=int, default=6)
    parser.add_argument("--validation-runs", type=int, default=3)
    parser.add_argument("--timeout", type=float, default=1800)
    parser.add_argument("--poll-interval", type=float, default=2)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        run_campaign(args)
    except (RuntimeError, ValueError, OSError, json.JSONDecodeError) as exc:
        print(f"FAILED: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
