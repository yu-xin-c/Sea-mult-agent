from __future__ import annotations

import hashlib
import tempfile
import unittest
from pathlib import Path

from research_optimizer.policy import select_candidate
from research_optimizer.profiling import build_profile
from research_optimizer.store import ExperienceConflict, ExperienceStore


def selection_request(campaign_id: str, context: dict, candidates: list[dict]) -> dict:
    return {
        "version": "research-optimizer.selection-request/v1",
        "campaign_id": campaign_id,
        "trial_number": 1,
        "phase": "parameter_search",
        "context": context,
        "candidates": candidates,
        "candidate_hints": [
            {"candidate_id": candidate["id"], "frontier_kind": "beam", "beam_rank": 1}
            for candidate in candidates
        ],
        "in_flight": [],
        "history": [],
        "baseline_score": 0.4,
        "best_score": 0.4,
        "remaining_trials": 4,
        "remaining_wall_seconds": 60,
    }


class ProfilingTests(unittest.TestCase):
    def test_profiles_canonical_retrieval_assets(self) -> None:
        manifest = {
            "domain": "retrieval",
            "adapter": "retrieval.v1",
            "counts": {"documents": 2, "search_cases": 1},
            "capabilities": {"graph_links": True},
            "assets": {
                "corpus": ".scholar/experiment/corpus.jsonl",
                "search_cases": ".scholar/experiment/search_queries.jsonl",
            },
            "source_files": [{"path": "corpus.jsonl", "sha256": "A" * 64}],
        }
        profile = build_profile(
            {
                "version": "research-optimizer.profile-request/v1",
                "manifest": manifest,
                "samples": {
                    "corpus": [
                        {"id": "d1", "text": "pump pressure alarm", "links": ["d2"]},
                        {"id": "d2", "text": "database credential rotation"},
                    ],
                    "search_cases": [
                        {"id": "q1", "query": "pump alarm", "relevant_doc_ids": ["d1"]}
                    ],
                },
            }
        )
        self.assertEqual(profile["version"], "experiment.features/v1")
        self.assertEqual(profile["extractor"], "python-dataset-profiler/v1")
        self.assertEqual(profile["numeric"]["document_count"], 2)
        self.assertEqual(profile["numeric"]["avg_query_tokens"], 2)
        self.assertGreater(profile["numeric"]["graph_density"], 0)
        self.assertTrue(profile["boolean"]["labeled_queries"])
        expected_fingerprint = hashlib.sha256(f"corpus.jsonl\0{'a' * 64}\n".encode("utf-8")).hexdigest()
        self.assertEqual(profile["dataset_fingerprint"], expected_fingerprint)


class ExperienceTests(unittest.TestCase):
    def test_records_only_validated_history_for_policy_learning(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            store = ExperienceStore(str(Path(directory) / "experience.sqlite3"))
            context = {
                "id": "context-a",
                "domain": "retrieval",
                "adapter": "retrieval.v1",
                "numeric": {"document_count": 100, "avg_query_tokens": 3},
                "boolean": {"graph_links": True},
            }
            candidates = [
                {"id": "candidate-bm25", "strategy": "bm25", "parameters": {}},
                {"id": "candidate-graph", "strategy": "graph_hybrid", "parameters": {}},
            ]
            request = selection_request("campaign-a", context, candidates)
            response = {
                "version": "research-optimizer.selection/v1",
                "policy_version": "contextual-ucb/v1",
                "candidate_id": "candidate-graph",
                "propensity": 0.5,
                "predicted_reward": 0.0,
                "reason_codes": ["fixture"],
            }
            store.record_decision(request, response)
            outcome = {
                "version": "experiment.experience-outcome/v1",
                "campaign_id": "campaign-a",
                "trial_number": 1,
                "status": "kept",
                "baseline_score": 0.4,
                "candidate_score": 0.6,
                "delta_from_baseline": 0.2,
                "reward": 0.49,
                "duration_ms": 100,
                "candidate": candidates[1],
                "recorded_at": "2026-08-13T00:00:00Z",
            }
            store.record_outcome(outcome)
            store.record_outcome(dict(outcome, recorded_at="2026-08-13T00:00:01Z"))
            self.assertEqual(store.validated_experiences("retrieval", "retrieval.v1"), [])
            validation = {
                "version": "research-optimizer.validation/v1",
                "campaign_id": "campaign-a",
                "status": "validated",
                "requested_runs": 2,
                "passed_runs": 2,
                "search_baseline": 0.4,
                "search_best": 0.6,
                "recorded_at": "2026-08-13T00:01:00Z",
            }
            store.record_validation(validation)
            store.record_validation(dict(validation, recorded_at="2026-08-13T00:01:01Z"))
            experiences = store.validated_experiences("retrieval", "retrieval.v1")
            self.assertEqual(len(experiences), 1)
            self.assertEqual(experiences[0]["candidate_id"], "candidate-graph")
            self.assertEqual(store.stats(), {"decisions": 1, "outcomes": 1, "validated_campaigns": 1})
            store.record_decision(request, response)
            with self.assertRaises(ExperienceConflict):
                conflicting = dict(response, candidate_id="candidate-bm25")
                store.record_decision(request, conflicting)
            with self.assertRaises(ExperienceConflict):
                store.record_outcome(dict(outcome, candidate=candidates[0]))
            with self.assertRaises(ValueError):
                store.record_validation(dict(validation, campaign_id="campaign-invalid", passed_runs=1))

    def test_contextual_policy_prefers_positive_similar_history(self) -> None:
        context = {
            "id": "context-new",
            "domain": "retrieval",
            "adapter": "retrieval.v1",
            "numeric": {"document_count": 110, "avg_query_tokens": 3.2},
            "boolean": {"graph_links": True},
        }
        candidates = [
            {"id": "candidate-bm25", "strategy": "bm25", "parameters": {}},
            {"id": "candidate-graph", "strategy": "graph_hybrid", "parameters": {}},
        ]
        experiences = [
            {
                "context": dict(context, id="old-a", numeric={"document_count": 100, "avg_query_tokens": 3}),
                "candidate_id": "candidate-bm25",
                "strategy": "bm25",
                "reward": -0.2,
            },
            {
                "context": dict(context, id="old-b", numeric={"document_count": 120, "avg_query_tokens": 3.5}),
                "candidate_id": "candidate-graph",
                "strategy": "graph_hybrid",
                "reward": 0.5,
            },
        ]
        response = select_candidate(selection_request("campaign-greedy", context, candidates), experiences)
        self.assertEqual(response["candidate_id"], "candidate-graph")
        self.assertIn("contextual", " ".join(response["reason_codes"]))
        self.assertGreater(response["predicted_reward"], 0)

    def test_contextual_prior_cannot_overwrite_current_validation(self) -> None:
        context = {
            "id": "context-current",
            "domain": "retrieval",
            "adapter": "retrieval.v1",
            "numeric": {"document_count": 100},
            "boolean": {},
        }
        candidates = [
            {"id": "a-next", "parent_id": "a-root", "strategy": "a", "parameters": {}, "depth": 1},
            {"id": "b-next", "parent_id": "b-root", "strategy": "b", "parameters": {}, "depth": 1},
        ]
        request = selection_request("campaign-prior-cap", context, candidates)
        request["history"] = [
            {"candidate": {"id": "a-root", "strategy": "a"}, "reward": 0.5, "backprop_path": ["a-root"]},
            {"candidate": {"id": "b-root", "strategy": "b"}, "reward": 0.0, "backprop_path": ["b-root"]},
        ]
        experiences = [
            {"context": dict(context, id=f"old-{index}"), "strategy": "b", "reward": 1.0}
            for index in range(40)
        ]
        response = select_candidate(request, experiences)
        self.assertEqual(response["candidate_id"], "a-next")
        self.assertIn("validated_contextual_prior", response["reason_codes"])

    def test_model_defaults_use_bounded_exhaustive_policy(self) -> None:
        candidates = [{"id": "model-a", "strategy": "a", "parameters": {}, "depth": 0}]
        request = selection_request("campaign-default", {}, candidates)
        request["phase"] = "model_defaults"
        response = select_candidate(request, [])
        self.assertEqual(response["policy_version"], "bounded-exhaustive/v1")
        self.assertEqual(response["candidate_id"], "model-a")

    def test_virtual_visit_spreads_parallel_agents_across_routes(self) -> None:
        candidates = [
            {"id": "a-next", "parent_id": "a-root", "strategy": "a", "parameters": {}, "depth": 1},
            {"id": "b-next", "parent_id": "b-root", "strategy": "b", "parameters": {}, "depth": 1},
        ]
        request = selection_request("campaign-parallel", {}, candidates)
        request["history"] = [
            {"candidate": {"id": "a-root", "strategy": "a"}, "reward": 0.0, "backprop_path": ["a-root"]},
            {"candidate": {"id": "b-root", "strategy": "b"}, "reward": 0.0, "backprop_path": ["b-root"]},
        ]
        request["in_flight"] = [candidates[0]]
        response = select_candidate(request, [])
        self.assertEqual(response["candidate_id"], "b-next")
        self.assertIn("outer_contextual_ucb_route", response["reason_codes"])

    def test_uct_prefers_stronger_parent_inside_selected_route(self) -> None:
        candidates = [
            {"id": "best-child", "parent_id": "parent-best", "strategy": "a", "parameters": {}, "depth": 2},
            {"id": "weak-child", "parent_id": "parent-weak", "strategy": "a", "parameters": {}, "depth": 2},
        ]
        request = selection_request("campaign-uct", {}, candidates)
        request["history"] = [
            {"candidate": {"id": "a-root", "strategy": "a"}, "reward": 0.0, "backprop_path": ["a-root"]},
            {"candidate": {"id": "parent-best", "strategy": "a"}, "reward": 0.5, "backprop_path": ["a-root", "parent-best"]},
            {"candidate": {"id": "parent-weak", "strategy": "a"}, "reward": -0.2, "backprop_path": ["a-root", "parent-weak"]},
        ]
        response = select_candidate(request, [])
        self.assertEqual(response["candidate_id"], "best-child")
        self.assertGreater(response["node_mean_reward"], 0)
        self.assertIn("inner_uct_parameter_path", response["reason_codes"])


if __name__ == "__main__":
    unittest.main()
