import assert from 'node:assert/strict';
import test from 'node:test';
import { parseExperimentLedger, parseExperimentValidation } from '../src/features/experiment/experimentLedger.ts';

const candidate = (id, strategy, parent = '', depth = 0) => ({ id, parent_id: parent, strategy, parameters: { alpha: 0.5 }, depth, reason: 'fixture' });

test('parses a strategy and parameter search tree', () => {
  const ledger = {
    version: 'experiment.ledger/v1', status: 'completed', domain: 'retrieval', adapter: 'retrieval.v1',
    metric_key: 'ndcg_at_k', direction: 'maximize', target_score: 0.8,
    baseline_score: 0.4, best_score: 0.82, max_trials: 8, completed_trials: 2, accepted_trials: 1,
    strategy_space: [
      { name: 'bm25', description: 'baseline', parameters: [{ name: 'k1', description: 'saturation', values: [0.8, 1.5], default: 1.5 }] },
      { name: 'hybrid_rrf', description: 'fusion', parameters: [{ name: 'alpha', description: 'weight', values: [0.2, 0.5, 0.8], default: 0.5 }] },
    ],
    trials: [
      { number: 0, candidate: candidate('base', 'bm25'), status: 'baseline', decision: 'keep', reason: 'baseline', metrics: { ndcg_at_k: 0.4 }, score: 0.4, duration_ms: 5 },
      { number: 1, candidate: candidate('hybrid', 'hybrid_rrf'), status: 'kept', decision: 'keep', reason: 'gain', metrics: { ndcg_at_k: 0.82 }, score: 0.82, delta_from_best: 0.42, duration_ms: 7 },
      { number: 2, candidate: candidate('alpha', 'hybrid_rrf', 'hybrid', 1), status: 'rejected', decision: 'reject', reason: 'no gain', metrics: { ndcg_at_k: 0.8 }, score: 0.8, delta_from_best: -0.02, duration_ms: 6 },
    ],
    best_candidate: candidate('hybrid', 'hybrid_rrf'), stop_reason: 'target_score_reached',
    resource_usage: { evaluator_runs: 3, evaluator_time_ms: 18, wall_duration_ms: 22 },
  };
  const parsed = parseExperimentLedger(ledger);
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.equal(parsed.ledger.trials[2].candidate.parentId, 'hybrid');
  assert.equal(parsed.ledger.bestCandidate.strategy, 'hybrid_rrf');
  assert.equal(parsed.ledger.strategySpace[1].parameters[0].values.length, 3);
});

test('parses repeated holdout evidence and rejects broken lineage', () => {
  const validation = {
    version: 'experiment.validation/v1', status: 'validated', domain: 'retrieval', adapter: 'retrieval.v1', metric_key: 'ndcg_at_k',
    search_baseline: 0.4, search_best: 0.82, holdout_target: 0.8, requested_runs: 1, passed_runs: 1, protected_intact: true,
    runs: [{ number: 1, status: 'passed', baseline_score: 0.45, candidate_score: 0.81, delta: 0.36, target_reached: true, duration_ms: 10, evidence: [{ case_id: 'q1', expected: ['d1'], observed: ['d1'], metrics: { ndcg: 1 }, details: { hit_rank: 1 } }] }],
    summary: 'validated',
  };
  assert.equal(parseExperimentValidation(validation).ok, true);

  const broken = {
    version: 'experiment.ledger/v1', status: 'completed', domain: 'x', adapter: 'x', metric_key: 'score', direction: 'maximize',
    baseline_score: 0, best_score: 1, max_trials: 1, completed_trials: 1, accepted_trials: 1,
    trials: [{ number: 0, candidate: candidate('child', 'x', 'missing', 1), status: 'baseline', decision: 'keep', reason: '', metrics: {}, score: 0, duration_ms: 1 }],
    best_candidate: candidate('child', 'x', 'missing', 1), stop_reason: 'done', resource_usage: { evaluator_runs: 1, evaluator_time_ms: 1, wall_duration_ms: 1 },
  };
  assert.equal(parseExperimentLedger(broken).ok, false);
});
