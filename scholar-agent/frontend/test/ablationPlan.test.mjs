import assert from 'node:assert/strict';
import test from 'node:test';
import { parseAblationPlan } from '../src/features/ablation/ablationPlan.ts';

const candidate = (overrides = {}) => ({
  id: 'module',
  parent_id: 'root',
  depth: 1,
  category: 'module',
  title: 'Remove reranker',
  hypothesis: 'The reranker causes the gain.',
  change: 'Disable only the reranker.',
  metrics: ['ndcg@10', 'latency_ms'],
  estimated_minutes: 10,
  estimated_gpu_minutes: 0,
  information_gain: 0.9,
  relevance: 0.8,
  reproducibility: 0.95,
  risk: 0.1,
  score: 0.82,
  decision_reason: 'selected within budget',
  ...overrides,
});

const fixture = {
  strategy: 'bounded_tree_of_thoughts',
  max_depth: 2,
  actual_depth: 2,
  branch_limit: 8,
  budget: { max_experiments: 2, max_gpu_minutes: 10, max_wall_minutes: 30 },
  candidates: [
    candidate(),
    candidate({
      id: 'module_isolated',
      parent_id: 'module',
      depth: 2,
      title: 'Remove only cross encoder',
      expansion_reason: 'Separates reranking from retrieval.',
      score: 0.91,
    }),
    candidate({ id: 'runtime', category: 'runtime_cost', title: 'Measure runtime', score: 0.7 }),
  ],
  selected: [candidate({ id: 'module_isolated', parent_id: 'module', depth: 2, title: 'Remove only cross encoder', score: 0.91 })],
  expanded_parent_ids: ['module'],
  pruned_ids: ['module', 'runtime'],
  selection_reason: 'selected one causal branch within budget',
};

test('parses a progressive ablation tree and preserves lineage', () => {
  const parsed = parseAblationPlan(JSON.stringify(fixture));
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.equal(parsed.plan.actualDepth, 2);
  assert.equal(parsed.plan.candidates[1].parentId, 'module');
  assert.equal(parsed.plan.selectedIds.has('module_isolated'), true);
  assert.equal(parsed.plan.expandedParentIds.has('module'), true);
  assert.equal(parsed.plan.candidates[0].informationGain, 0.9);
  assert.equal(parsed.plan.candidates[0].decisionReason, 'selected within budget');
});

test('keeps legacy one-level plans readable', () => {
  const legacy = {
    ...fixture,
    actual_depth: undefined,
    candidates: [candidate({ depth: undefined })],
    selected: [candidate({ depth: undefined })],
    expanded_parent_ids: undefined,
    pruned_ids: undefined,
  };
  const parsed = parseAblationPlan(legacy);
  assert.equal(parsed.ok, true);
  if (!parsed.ok) return;
  assert.equal(parsed.plan.actualDepth, 1);
  assert.equal(parsed.plan.candidates[0].depth, 1);
});

test('rejects child branches with an unknown or cross-category parent', () => {
  const unknownParent = { ...fixture, candidates: [candidate(), candidate({ id: 'child', parent_id: 'missing', depth: 2 })] };
  assert.equal(parseAblationPlan(unknownParent).ok, false);

  const wrongCategory = {
    ...fixture,
    candidates: [candidate(), candidate({ id: 'child', parent_id: 'module', depth: 2, category: 'parameter' })],
  };
  assert.equal(parseAblationPlan(wrongCategory).ok, false);
});

test('rejects inconsistent depth, branch limits and decision states', () => {
  const wrongDepth = { ...fixture, actual_depth: 1 };
  assert.equal(parseAblationPlan(wrongDepth).ok, false);

  const overLimit = { ...fixture, branch_limit: 2 };
  assert.equal(parseAblationPlan(overLimit).ok, false);

  const selectedAndPruned = { ...fixture, pruned_ids: ['module_isolated'] };
  assert.equal(parseAblationPlan(selectedAndPruned).ok, false);
});
