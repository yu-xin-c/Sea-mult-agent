import assert from 'node:assert/strict';
import test from 'node:test';
import {
  autoResearchGain,
  formatResearchDuration,
  parseAutoResearchLedger,
  parseAutoResearchValidationReport,
} from '../src/features/autoresearch/trialLedger.ts';

const fixture = {
  version: 'autoresearch.ledger/v1',
  status: 'completed',
  metric_key: 'metrics.macro_f1',
  direction: 'maximize',
  target_score: 0.8,
  search_runs: 3,
  search_aggregation: 'worst',
  baseline_score: 0.5,
  best_score: 0.8,
  max_trials: 2,
  completed_trials: 2,
  accepted_trials: 1,
  stop_reason: 'trial_budget_exhausted',
  resource_usage: {
    command_runs: 6,
    guard_runs: 3,
    evaluator_runs: 3,
    successful_commands: 6,
    failed_commands: 0,
    command_duration_ms: 1500,
    wall_duration_ms: 3600,
  },
  trials: [
    {
      number: 0,
      status: 'baseline',
      decision: 'keep',
      reason: 'frozen baseline',
      started_at: '2026-08-07T00:00:00Z',
      finished_at: '2026-08-07T00:00:01Z',
    },
    {
      number: 1,
      status: 'kept',
      decision: 'keep',
      diagnosis: 'the evaluator misses ambiguous routing cases',
      hypothesis: 'focused routing rules improve F1',
      reason: 'improved by 0.3',
      metric: 0.8,
      metric_samples: [0.8, 0.82, 0.81],
      metric_stddev: 0.00816,
      metric_aggregation: 'worst',
      delta_from_best: 0.3,
      started_at: '2026-08-07T00:00:01Z',
      finished_at: '2026-08-07T00:00:03.5Z',
      patches: [{ path: 'candidate.py', reason: 'add bounded rules', before_sha256: 'aaaa', after_sha256: 'bbbb' }],
    },
    {
      number: 2,
      status: 'rejected',
      decision: 'reject',
      reason: 'regressed',
      metric: 0.4,
      delta_from_best: -0.4,
      guard_results: [{ duration_ms: 25 }],
      eval_result: { duration_ms: 75 },
    },
  ],
};

test('parses a completed ledger and normalizes baseline plus durations', () => {
  const parsed = parseAutoResearchLedger(JSON.stringify(fixture));
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.equal(parsed.ledger.trials[0].metric, 0.5);
  assert.equal(parsed.ledger.trials[1].durationMs, 2500);
  assert.equal(parsed.ledger.trials[1].diagnosis, 'the evaluator misses ambiguous routing cases');
  assert.equal(parsed.ledger.searchRuns, 3);
  assert.equal(parsed.ledger.searchAggregation, 'worst');
  assert.equal(parsed.ledger.targetScore, 0.8);
  assert.deepEqual(parsed.ledger.trials[1].metricSamples, [0.8, 0.82, 0.81]);
  assert.equal(parsed.ledger.trials[1].metricAggregation, 'worst');
  assert.equal(parsed.ledger.trials[2].durationMs, 100);
  assert.equal(parsed.ledger.trials[1].patches[0].path, 'candidate.py');
  assert.equal(parsed.ledger.resourceUsage?.commandRuns, 6);
  assert.equal(parsed.ledger.resourceUsage?.wallDurationMs, 3600);
  assert.ok(Math.abs(autoResearchGain(parsed.ledger) - 0.3) < 1e-12);
});

test('supports a JSON-encoded ledger string and minimize direction', () => {
  const minimize = { ...fixture, direction: 'minimize', baseline_score: 1.2, best_score: 0.7 };
  const parsed = parseAutoResearchLedger(JSON.stringify(JSON.stringify(minimize)));
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.ok(Math.abs(autoResearchGain(parsed.ledger) - 0.5) < 1e-12);
});

test('rejects malformed, unsupported and incomplete ledgers', () => {
  assert.equal(parseAutoResearchLedger('{broken').ok, false);
  assert.equal(parseAutoResearchLedger({ ...fixture, version: 'autoresearch.ledger/v2' }).ok, false);
  assert.equal(parseAutoResearchLedger({ ...fixture, trials: [] }).ok, false);
  assert.equal(parseAutoResearchLedger({ ...fixture, baseline_score: '0.5' }).ok, false);
  assert.equal(parseAutoResearchLedger({ ...fixture, target_score: '0.8' }).ok, false);
  assert.equal(parseAutoResearchLedger({ ...fixture, resource_usage: { ...fixture.resource_usage, command_runs: 7 } }).ok, false);
});

test('keeps legacy v1 ledgers readable when resource usage is absent', () => {
  const legacyFixture = { ...fixture };
  delete legacyFixture.resource_usage;
  const parsed = parseAutoResearchLedger(legacyFixture);
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.equal(parsed.ledger.resourceUsage, null);
  assert.equal(parsed.ledger.searchRuns, 3);
});

test('defaults legacy search measurement fields to one mean sample', () => {
  const legacyFixture = { ...fixture, trials: fixture.trials.map((trial) => ({ ...trial })) };
  delete legacyFixture.search_runs;
  delete legacyFixture.search_aggregation;
  delete legacyFixture.trials[1].metric_samples;
  delete legacyFixture.trials[1].metric_aggregation;
  const parsed = parseAutoResearchLedger(legacyFixture);
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.equal(parsed.ledger.searchRuns, 1);
  assert.equal(parsed.ledger.searchAggregation, 'mean');
  assert.equal(parsed.ledger.targetScore, 0.8);
  assert.deepEqual(parsed.ledger.trials[1].metricSamples, [0.8]);
});

test('formats bounded trial durations for the timeline', () => {
  assert.equal(formatResearchDuration(450), '450 ms');
  assert.equal(formatResearchDuration(2500), '2.5 s');
  assert.equal(formatResearchDuration(65_000), '1m 5s');
});

test('parses hidden holdout acceptance separately from the search score', () => {
  const report = {
    version: 'autoresearch.validation/v1',
    status: 'validated',
    validation_mode: 'hidden_holdout',
    metric_key: 'metrics.robustness_score',
    search_best_score: 1,
    holdout_baseline_score: 0.25,
    expected_score: 1,
    mean_score: 1,
    stddev: 0,
    requested_runs: 3,
    completed_runs: 3,
    passed_runs: 3,
    failed_runs: 0,
    failure_rate: 0,
    protected_intact: true,
    workspace_intact: true,
    candidate_intact: true,
    summary: '3 hidden holdout runs accepted',
    resource_usage: {
      command_runs: 6,
      guard_runs: 3,
      evaluator_runs: 3,
      successful_commands: 6,
      failed_commands: 0,
      command_duration_ms: 900,
      wall_duration_ms: 1200,
    },
    runs: [1, 2, 3].map((number) => ({
      number,
      status: 'validated',
      observed_score: 1,
      delta_from_baseline: 0.75,
      score_matches: true,
      started_at: '2026-08-10T00:00:00Z',
      finished_at: '2026-08-10T00:00:01Z',
    })),
  };
  const parsed = parseAutoResearchValidationReport(report);
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.equal(parsed.validation.validationMode, 'hidden_holdout');
  assert.equal(parsed.validation.holdoutBaselineScore, 0.25);
  assert.equal(parsed.validation.searchBestScore, 1);
  assert.equal(parsed.validation.runs[0].deltaFromBaseline, 0.75);
  assert.equal(parsed.validation.resourceUsage?.evaluatorRuns, 3);
});

test('rejects a validation report that hides its validation mode', () => {
  const parsed = parseAutoResearchValidationReport({
    version: 'autoresearch.validation/v1',
    status: 'validated',
    validation_mode: 'independent',
  });
  assert.equal(parsed.ok, false);
});
