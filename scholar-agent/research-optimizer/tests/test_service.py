from __future__ import annotations

import hashlib
import json
import os
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer
from pathlib import Path
from unittest.mock import patch

from research_optimizer.service import OptimizerApplication, OptimizerHandler
from research_optimizer.store import ExperienceStore


class ServiceTests(unittest.TestCase):
    def setUp(self) -> None:
        self.directory = tempfile.TemporaryDirectory()
        self.environment = patch.dict(os.environ, {"RESEARCH_OPTIMIZER_API_TOKEN": "test-token"})
        self.environment.start()
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), OptimizerHandler)
        self.server.application = OptimizerApplication(  # type: ignore[attr-defined]
            ExperienceStore(str(Path(self.directory.name) / "experience.sqlite3"))
        )
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        self.environment.stop()
        self.directory.cleanup()

    def request(self, path: str, payload: dict | None = None, authorized: bool = False) -> tuple[int, dict]:
        body = None if payload is None else json.dumps(payload).encode("utf-8")
        request = urllib.request.Request(self.base_url + path, data=body)
        if authorized:
            request.add_header("Authorization", "Bearer test-token")
        if body is not None:
            request.add_header("Content-Type", "application/json")
        with urllib.request.urlopen(request, timeout=2) as response:
            return response.status, json.loads(response.read())

    def test_health_authentication_and_profile_contract(self) -> None:
        status, health = self.request("/health")
        self.assertEqual(status, 200)
        self.assertTrue(health["ok"])
        with self.assertRaises(urllib.error.HTTPError) as failure:
            self.request("/v1/stats")
        self.assertEqual(failure.exception.code, 401)

        status, profile = self.request(
            "/v1/profile",
            {
                "version": "research-optimizer.profile-request/v1",
                "manifest": {
                    "domain": "generic",
                    "adapter": "portable.v1",
                    "counts": {"samples": 12},
                    "capabilities": {},
                    "source_files": [],
                },
            },
            authorized=True,
        )
        self.assertEqual(status, 200)
        self.assertEqual(profile["version"], "experiment.features/v1")
        self.assertEqual(profile["dataset_fingerprint"], hashlib.sha256(b"").hexdigest())


if __name__ == "__main__":
    unittest.main()
