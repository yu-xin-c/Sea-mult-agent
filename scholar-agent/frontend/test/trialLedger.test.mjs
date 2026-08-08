import assert from 'node:assert/strict';
import test from 'node:test';
import {
  autoResearchGain,
  formatResearchDuration,
  parseAutoResearchLedger,
} from '../src/features/autoresearch/trialLedger.ts';

const fixture = {
  version: 'autoresearch.ledger/v1',
  status: 'completed',
  metric_key: 'metrics.macro_f1',
  direction: 'maximize',
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
      hypothesis: 'focused routing rules improve F1',
      reason: 'improved by 0.3',
      metric: 0.8,
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
  assert.equal(parseAutoResearchLedger({ ...fixture, resource_usage: { ...fixture.resource_usage, command_runs: 7 } }).ok, false);
});

test('keeps legacy v1 ledgers readable when resource usage is absent', () => {
  const legacyFixture = { ...fixture };
  delete legacyFixture.resource_usage;
  const parsed = parseAutoResearchLedger(legacyFixture);
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.equal(parsed.ledger.resourceUsage, null);
});

test('formats bounded trial durations for the timeline', () => {
  assert.equal(formatResearchDuration(450), '450 ms');
  assert.equal(formatResearchDuration(2500), '2.5 s');
  assert.equal(formatResearchDuration(65_000), '1m 5s');
});
