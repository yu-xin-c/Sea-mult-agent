from __future__ import annotations

import hmac
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from .policy import PolicyError, select_candidate
from .profiling import ProfileError, build_profile
from .store import ExperienceConflict, ExperienceStore

MAX_REQUEST_BYTES = 2 * 1024 * 1024
OUTCOME_VERSION = "experiment.experience-outcome/v1"
VALIDATION_VERSION = "research-optimizer.validation/v1"


def _reject_json_constant(value: str) -> None:
    raise ValueError(f"invalid JSON number: {value}")


class OptimizerApplication:
    def __init__(self, store: ExperienceStore) -> None:
        self.store = store

    def profile(self, payload: dict[str, Any]) -> dict[str, Any]:
        return build_profile(payload)

    def select(self, payload: dict[str, Any]) -> dict[str, Any]:
        context = payload.get("context") or {}
        experiences = self.store.validated_experiences(str(context.get("domain") or ""), str(context.get("adapter") or ""))
        response = select_candidate(payload, experiences)
        self.store.record_decision(payload, response)
        return response

    def outcome(self, payload: dict[str, Any]) -> dict[str, Any]:
        if payload.get("version") != OUTCOME_VERSION:
            raise ValueError("unsupported outcome version")
        self.store.record_outcome(payload)
        return {"accepted": True}

    def validation(self, payload: dict[str, Any]) -> dict[str, Any]:
        if payload.get("version") != VALIDATION_VERSION:
            raise ValueError("unsupported validation version")
        self.store.record_validation(payload)
        return {"accepted": True}


class OptimizerHandler(BaseHTTPRequestHandler):
    server_version = "ScholarResearchOptimizer/0.1"

    @property
    def application(self) -> OptimizerApplication:
        return self.server.application  # type: ignore[attr-defined]

    def _authorized(self) -> bool:
        expected = os.getenv("RESEARCH_OPTIMIZER_API_TOKEN", "").strip()
        if not expected:
            return True
        supplied = self.headers.get("Authorization", "")
        return hmac.compare_digest(supplied, f"Bearer {expected}")

    def _send(self, status: int, payload: dict[str, Any]) -> None:
        body = json.dumps(payload, ensure_ascii=True, allow_nan=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _payload(self) -> dict[str, Any]:
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError as exc:
            raise ValueError("invalid Content-Length") from exc
        if length <= 0 or length > MAX_REQUEST_BYTES:
            raise ValueError("request body must be between 1 byte and 2 MiB")
        value = json.loads(
            self.rfile.read(length),
            parse_constant=_reject_json_constant,
        )
        if not isinstance(value, dict):
            raise ValueError("request body must be a JSON object")
        return value

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/health":
            self._send(200, {"ok": True, "service": "research-optimizer"})
            return
        if self.path == "/v1/stats":
            if not self._authorized():
                self._send(401, {"error": "unauthorized"})
                return
            self._send(200, self.application.store.stats())
            return
        self._send(404, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802
        if not self._authorized():
            self._send(401, {"error": "unauthorized"})
            return
        routes = {
            "/v1/profile": self.application.profile,
            "/v1/select": self.application.select,
            "/v1/experience/outcome": self.application.outcome,
            "/v1/experience/validation": self.application.validation,
        }
        handler = routes.get(self.path)
        if handler is None:
            self._send(404, {"error": "not found"})
            return
        try:
            self._send(200, handler(self._payload()))
        except ExperienceConflict as exc:
            self._send(409, {"error": str(exc)})
        except (ValueError, KeyError, TypeError, ProfileError, PolicyError) as exc:
            self._send(400, {"error": str(exc)})
        except Exception:
            self._send(500, {"error": "internal optimizer error"})

    def log_message(self, format_string: str, *args: object) -> None:
        print(f"research-optimizer: {format_string % args}", flush=True)


def main() -> None:
    host = os.getenv("RESEARCH_OPTIMIZER_HOST", "0.0.0.0")
    port = int(os.getenv("RESEARCH_OPTIMIZER_PORT", "8090"))
    database = os.getenv("RESEARCH_OPTIMIZER_DB_PATH", "/data/experiences.sqlite3")
    server = ThreadingHTTPServer((host, port), OptimizerHandler)
    server.application = OptimizerApplication(ExperienceStore(database))  # type: ignore[attr-defined]
    print(f"research-optimizer listening on {host}:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
