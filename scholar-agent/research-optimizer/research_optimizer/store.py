from __future__ import annotations

import json
import math
import sqlite3
import threading
from pathlib import Path
from typing import Any


class ExperienceConflict(ValueError):
    pass


class ExperienceStore:
    def __init__(self, path: str) -> None:
        self.path = str(Path(path).expanduser())
        Path(self.path).parent.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()
        self._initialize()

    def _connect(self) -> sqlite3.Connection:
        connection = sqlite3.connect(self.path, timeout=10)
        connection.row_factory = sqlite3.Row
        connection.execute("PRAGMA foreign_keys = ON")
        connection.execute("PRAGMA busy_timeout = 10000")
        return connection

    def _initialize(self) -> None:
        with self._lock, self._connect() as connection:
            connection.execute("PRAGMA journal_mode = WAL")
            connection.executescript(
                """
                CREATE TABLE IF NOT EXISTS decisions (
                    campaign_id TEXT NOT NULL,
                    trial_number INTEGER NOT NULL,
                    context_id TEXT NOT NULL,
                    domain TEXT NOT NULL,
                    adapter TEXT NOT NULL,
                    context_json TEXT NOT NULL,
                    available_actions_json TEXT NOT NULL,
                    candidate_id TEXT NOT NULL,
                    strategy TEXT NOT NULL,
                    policy_version TEXT NOT NULL,
                    propensity REAL NOT NULL,
                    predicted_reward REAL,
                    request_json TEXT NOT NULL,
                    response_json TEXT NOT NULL,
                    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
                    PRIMARY KEY (campaign_id, trial_number)
                );
                CREATE TABLE IF NOT EXISTS outcomes (
                    campaign_id TEXT NOT NULL,
                    trial_number INTEGER NOT NULL,
                    status TEXT NOT NULL,
                    score REAL,
                    baseline_score REAL NOT NULL,
                    delta_from_baseline REAL,
                    reward REAL NOT NULL,
                    duration_ms INTEGER NOT NULL,
                    payload_json TEXT NOT NULL,
                    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
                    PRIMARY KEY (campaign_id, trial_number),
                    FOREIGN KEY (campaign_id, trial_number)
                        REFERENCES decisions(campaign_id, trial_number)
                );
                CREATE TABLE IF NOT EXISTS validations (
                    campaign_id TEXT PRIMARY KEY,
                    status TEXT NOT NULL,
                    requested_runs INTEGER NOT NULL,
                    passed_runs INTEGER NOT NULL,
                    search_baseline REAL NOT NULL,
                    search_best REAL NOT NULL,
                    payload_json TEXT NOT NULL,
                    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
                );
                CREATE INDEX IF NOT EXISTS idx_decisions_action
                    ON decisions(domain, adapter, candidate_id, strategy);
                """
            )

    @staticmethod
    def _json(value: Any) -> str:
        return json.dumps(value, ensure_ascii=True, sort_keys=True, separators=(",", ":"))

    @classmethod
    def _retry_matches(cls, existing: str, payload: dict[str, Any]) -> bool:
        try:
            previous = json.loads(existing)
        except json.JSONDecodeError:
            return False
        if not isinstance(previous, dict):
            return False
        previous = dict(previous)
        current = dict(payload)
        previous.pop("recorded_at", None)
        current.pop("recorded_at", None)
        return cls._json(previous) == cls._json(current)

    def record_decision(self, request: dict[str, Any], response: dict[str, Any]) -> None:
        campaign_id = str(request["campaign_id"])
        trial_number = int(request["trial_number"])
        candidate_id = str(response["candidate_id"])
        candidates = request.get("candidates") or []
        selected = next((item for item in candidates if item.get("id") == candidate_id), None)
        if not selected:
            raise ExperienceConflict("selected candidate is absent from request")
        context = request.get("context") or {}
        values = (
            campaign_id,
            trial_number,
            str(context.get("id") or "unknown"),
            str(context.get("domain") or "unknown"),
            str(context.get("adapter") or "unknown"),
            self._json(context),
            self._json([item.get("id") for item in candidates]),
            candidate_id,
            str(selected.get("strategy") or ""),
            str(response["policy_version"]),
            float(response["propensity"]),
            response.get("predicted_reward"),
            self._json(request),
            self._json(response),
        )
        with self._lock, self._connect() as connection:
            existing = connection.execute(
                "SELECT candidate_id, response_json FROM decisions WHERE campaign_id=? AND trial_number=?",
                (campaign_id, trial_number),
            ).fetchone()
            if existing:
                if existing["candidate_id"] != candidate_id or existing["response_json"] != self._json(response):
                    raise ExperienceConflict("decision retry conflicts with the recorded decision")
                return
            connection.execute(
                """
                INSERT INTO decisions (
                    campaign_id, trial_number, context_id, domain, adapter, context_json,
                    available_actions_json, candidate_id, strategy, policy_version,
                    propensity, predicted_reward, request_json, response_json
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                values,
            )

    def record_outcome(self, payload: dict[str, Any]) -> None:
        campaign_id = str(payload["campaign_id"])
        trial_number = int(payload["trial_number"])
        candidate = payload.get("candidate") or {}
        candidate_id = str(candidate.get("id") or "") if isinstance(candidate, dict) else ""
        status = str(payload["status"])
        baseline_score = float(payload["baseline_score"])
        reward = float(payload["reward"])
        duration_ms = int(payload["duration_ms"])
        candidate_score = payload.get("candidate_score")
        delta = payload.get("delta_from_baseline")
        numeric_values = [baseline_score, reward]
        numeric_values.extend(float(value) for value in (candidate_score, delta) if value is not None)
        if not campaign_id or trial_number < 1 or not candidate_id:
            raise ValueError("outcome identity is incomplete")
        if status not in {"kept", "rejected"} or duration_ms < 0 or any(not math.isfinite(value) for value in numeric_values):
            raise ValueError("outcome values are invalid")
        encoded = self._json(payload)
        values = (
            campaign_id,
            trial_number,
            status,
            candidate_score,
            baseline_score,
            delta,
            reward,
            duration_ms,
            encoded,
        )
        with self._lock, self._connect() as connection:
            decision = connection.execute(
                "SELECT candidate_id, context_id FROM decisions WHERE campaign_id=? AND trial_number=?",
                (campaign_id, trial_number),
            ).fetchone()
            if not decision:
                raise ExperienceConflict("outcome has no recorded decision")
            if decision["candidate_id"] != candidate_id:
                raise ExperienceConflict("outcome candidate conflicts with the recorded decision")
            context_id = str(payload.get("context_id") or "")
            if context_id and decision["context_id"] != context_id:
                raise ExperienceConflict("outcome context conflicts with the recorded decision")
            existing = connection.execute(
                "SELECT payload_json FROM outcomes WHERE campaign_id=? AND trial_number=?", (campaign_id, trial_number)
            ).fetchone()
            if existing:
                if not self._retry_matches(existing["payload_json"], payload):
                    raise ExperienceConflict("outcome retry conflicts with the recorded outcome")
                return
            connection.execute(
                """
                INSERT INTO outcomes (
                    campaign_id, trial_number, status, score, baseline_score,
                    delta_from_baseline, reward, duration_ms, payload_json
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                values,
            )

    def record_validation(self, payload: dict[str, Any]) -> None:
        campaign_id = str(payload["campaign_id"])
        status = str(payload["status"])
        requested_runs = int(payload["requested_runs"])
        passed_runs = int(payload["passed_runs"])
        search_baseline = float(payload["search_baseline"])
        search_best = float(payload["search_best"])
        if not campaign_id or status not in {"validated", "not_validated", "failed"}:
            raise ValueError("validation identity or status is invalid")
        if requested_runs < 1 or passed_runs < 0 or passed_runs > requested_runs:
            raise ValueError("validation run counts are invalid")
        if status == "validated" and passed_runs != requested_runs:
            raise ValueError("validated campaign must pass every requested run")
        if not math.isfinite(search_baseline) or not math.isfinite(search_best):
            raise ValueError("validation scores must be finite")
        encoded = self._json(payload)
        values = (
            campaign_id,
            status,
            requested_runs,
            passed_runs,
            search_baseline,
            search_best,
            encoded,
        )
        with self._lock, self._connect() as connection:
            existing = connection.execute(
                "SELECT payload_json FROM validations WHERE campaign_id=?", (campaign_id,)
            ).fetchone()
            if existing:
                if not self._retry_matches(existing["payload_json"], payload):
                    raise ExperienceConflict("validation retry conflicts with the recorded validation")
                return
            connection.execute(
                """
                INSERT INTO validations (
                    campaign_id, status, requested_runs, passed_runs,
                    search_baseline, search_best, payload_json
                ) VALUES (?, ?, ?, ?, ?, ?, ?)
                """,
                values,
            )

    def validated_experiences(self, domain: str, adapter: str) -> list[dict[str, Any]]:
        with self._connect() as connection:
            rows = connection.execute(
                """
                SELECT d.context_json, d.candidate_id, d.strategy, o.reward
                FROM decisions d
                JOIN outcomes o USING (campaign_id, trial_number)
                JOIN validations v USING (campaign_id)
                WHERE d.domain=? AND d.adapter=? AND v.status='validated'
                    AND v.passed_runs = v.requested_runs
                """,
                (domain, adapter),
            ).fetchall()
        return [
            {
                "context": json.loads(row["context_json"]),
                "candidate_id": row["candidate_id"],
                "strategy": row["strategy"],
                "reward": float(row["reward"]),
            }
            for row in rows
        ]

    def stats(self) -> dict[str, int]:
        with self._connect() as connection:
            return {
                "decisions": int(connection.execute("SELECT COUNT(*) FROM decisions").fetchone()[0]),
                "outcomes": int(connection.execute("SELECT COUNT(*) FROM outcomes").fetchone()[0]),
                "validated_campaigns": int(
                    connection.execute("SELECT COUNT(*) FROM validations WHERE status='validated'").fetchone()[0]
                ),
            }
