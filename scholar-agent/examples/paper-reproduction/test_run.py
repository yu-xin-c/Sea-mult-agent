import importlib.util
import unittest
from pathlib import Path
from unittest.mock import patch


MODULE_PATH = Path(__file__).with_name("run.py")
SPEC = importlib.util.spec_from_file_location("paper_reproduction_example", MODULE_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load example module from {MODULE_PATH}")
EXAMPLE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(EXAMPLE)


class PaperReproductionExampleTest(unittest.TestCase):
    def test_run_example_validates_completed_plan(self):
        def fake_request(_base_url, method, path, payload=None, timeout=30):
            self.assertGreater(timeout, 0)
            if (method, path) == ("GET", "/api/health"):
                return {"ok": True}
            if (method, path) == ("POST", "/api/plan"):
                self.assertEqual(payload, {"intent": EXAMPLE.PROMPT})
                return {
                    "plan_graph": {
                        "id": "example-plan",
                        "intent_type": "Paper_Reproduction",
                        "nodes": [{"id": "node-1"}],
                    }
                }
            if (method, path) == ("POST", "/api/plans/example-plan/execute"):
                self.assertEqual(payload, {})
                return {"plan_id": "example-plan"}
            if (method, path) == ("GET", "/api/plans/example-plan"):
                return {
                    "plan_graph": {
                        "id": "example-plan",
                        "intent_type": "Paper_Reproduction",
                        "status": "completed",
                        "nodes": [{"id": "node-1", "name": "Run smoke", "status": "completed"}],
                        "meta": {"completed_nodes": 1, "failed_nodes": 0, "in_progress_nodes": 0},
                        "artifacts": {
                            "repo_url": {"value": EXAMPLE.EXPECTED_REPOSITORY},
                            "run_metrics": {"value": '{"status":"ok"}'},
                            "comparison_report": {"value": "Smoke result matches the expected scope."},
                        },
                    }
                }
            if (method, path) == ("GET", "/api/plans/example-plan/events"):
                return {
                    "events": [
                        {"event_type": "plan_started"},
                        {"event_type": "artifact_created"},
                        {"event_type": "plan_completed"},
                    ]
                }
            self.fail(f"unexpected request: {method} {path}")

        with patch.object(EXAMPLE, "request_json", side_effect=fake_request):
            summary = EXAMPLE.run_example("http://example.invalid", timeout=2, poll_interval=0.01)

        self.assertEqual(summary["status"], "passed")
        self.assertEqual(summary["repository"], EXAMPLE.EXPECTED_REPOSITORY)
        self.assertEqual(summary["completed_nodes"], 1)
        self.assertEqual(summary["event_count"], 3)


if __name__ == "__main__":
    unittest.main()
