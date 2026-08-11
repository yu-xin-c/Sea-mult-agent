import json
import subprocess
import sys
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parent


class IntentRouterEvaluatorTest(unittest.TestCase):
    def test_frozen_baseline_metrics_and_hashes(self):
        completed = subprocess.run(
            [sys.executable, str(ROOT / "evaluator.py")],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
        payload = json.loads(completed.stdout.strip().splitlines()[-1])
        self.assertEqual(payload["status"], "ok")
        self.assertEqual(payload["sample_count"], 26)
        self.assertAlmostEqual(payload["metrics"]["accuracy"], 0.6538461538461539)
        self.assertAlmostEqual(payload["metrics"]["macro_f1"], 0.672875816993464)
        self.assertEqual(
            payload["dataset_sha256"],
            "331ecf7f1a1d7356c3d9217ed0f657668b44f77f413191ccf030d2050694068f",
        )
        self.assertEqual(
            payload["candidate_sha256"],
            "7d39da592567260604ed6f411fcdbaadde0becab5ca3a8c55485ba301ee3deba",
        )


if __name__ == "__main__":
    unittest.main()
