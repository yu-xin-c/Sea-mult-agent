export interface AblationBudgetView {
  maxExperiments: number;
  maxGpuMinutes: number;
  maxWallMinutes: number;
}

export interface AblationCandidateView {
  id: string;
  parentId: string;
  depth: number;
  category: string;
  title: string;
  hypothesis: string;
  change: string;
  metrics: string[];
  estimatedMinutes: number;
  estimatedGpuMinutes: number;
  informationGain: number;
  relevance: number;
  reproducibility: number;
  risk: number;
  score: number;
  expansionReason: string;
  evaluationReason: string;
  decisionReason: string;
}

export interface AblationPlanView {
  strategy: 'bounded_tree_of_thoughts';
  maxDepth: number;
  actualDepth: number;
  branchLimit: number;
  budget: AblationBudgetView;
  candidates: AblationCandidateView[];
  selectedIds: Set<string>;
  expandedParentIds: Set<string>;
  prunedIds: Set<string>;
  selectionReason: string;
}

export type AblationPlanParseResult =
  | { ok: true; plan: AblationPlanView }
  | { ok: false; error: string };

type JsonObject = Record<string, unknown>;

const asObject = (value: unknown): JsonObject | null =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? (value as JsonObject) : null;

const text = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

const finite = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;

const nonNegativeInteger = (value: unknown): number | null =>
  typeof value === 'number' && Number.isInteger(value) && value >= 0 ? value : null;

const decodeObject = (raw: unknown): JsonObject | null => {
  let value = raw;
  for (let attempt = 0; attempt < 2 && typeof value === 'string'; attempt += 1) {
    try {
      value = JSON.parse(value);
    } catch {
      return null;
    }
  }
  return asObject(value);
};

const parseCandidate = (value: unknown): AblationCandidateView | null => {
  const candidate = asObject(value);
  if (!candidate) return null;
  const id = text(candidate.id);
  const category = text(candidate.category);
  const title = text(candidate.title);
  if (!id || !category || !title) return null;
  const parentId = text(candidate.parent_id) || 'root';
  const depth = nonNegativeInteger(candidate.depth) ?? (parentId === 'root' ? 1 : 2);
  const metrics = Array.isArray(candidate.metrics)
    ? candidate.metrics.map(text).filter(Boolean).slice(0, 8)
    : [];
  return {
    id,
    parentId,
    depth: Math.max(1, depth),
    category,
    title,
    hypothesis: text(candidate.hypothesis),
    change: text(candidate.change),
    metrics,
    estimatedMinutes: nonNegativeInteger(candidate.estimated_minutes) ?? 0,
    estimatedGpuMinutes: nonNegativeInteger(candidate.estimated_gpu_minutes) ?? 0,
    informationGain: finite(candidate.information_gain) ?? 0,
    relevance: finite(candidate.relevance) ?? 0,
    reproducibility: finite(candidate.reproducibility) ?? 0,
    risk: finite(candidate.risk) ?? 0,
    score: finite(candidate.score) ?? 0,
    expansionReason: text(candidate.expansion_reason),
    evaluationReason: text(candidate.evaluation_reason),
    decisionReason: text(candidate.decision_reason),
  };
};

const candidateIds = (value: unknown): Set<string> => {
  if (!Array.isArray(value)) return new Set();
  return new Set(value.map((item) => text(asObject(item)?.id)).filter(Boolean));
};

const stringSet = (value: unknown): Set<string> => {
  if (!Array.isArray(value)) return new Set();
  return new Set(value.map(text).filter(Boolean));
};

export const parseAblationPlan = (raw: unknown): AblationPlanParseResult => {
  const source = decodeObject(raw);
  if (!source) return { ok: false, error: '消融计划不是有效 JSON。' };
  if (source.strategy !== 'bounded_tree_of_thoughts') {
    return { ok: false, error: '不支持的消融计划版本。' };
  }

  const budget = asObject(source.budget);
  const maxExperiments = nonNegativeInteger(budget?.max_experiments);
  const maxGpuMinutes = nonNegativeInteger(budget?.max_gpu_minutes);
  const maxWallMinutes = nonNegativeInteger(budget?.max_wall_minutes);
  const branchLimit = nonNegativeInteger(source.branch_limit);
  if (maxExperiments === null || maxGpuMinutes === null || maxWallMinutes === null || branchLimit === null) {
    return { ok: false, error: '消融计划缺少有效预算。' };
  }
  if (maxExperiments === 0 || maxWallMinutes === 0 || branchLimit === 0) {
    return { ok: false, error: '消融计划预算必须大于零。' };
  }

  if (!Array.isArray(source.candidates)) return { ok: false, error: '消融计划没有候选分支。' };
  const candidates = source.candidates.map(parseCandidate).filter((item): item is AblationCandidateView => item !== null);
  if (candidates.length === 0 || candidates.length !== source.candidates.length) {
    return { ok: false, error: '消融候选分支结构不完整。' };
  }
  if (candidates.length > branchLimit) return { ok: false, error: '消融候选超过分支上限。' };
  const byId = new Map(candidates.map((candidate) => [candidate.id, candidate]));
  if (byId.size !== candidates.length) return { ok: false, error: '消融候选 ID 重复。' };
  for (const candidate of candidates) {
    if (candidate.parentId === 'root') {
      if (candidate.depth !== 1) return { ok: false, error: `根候选 ${candidate.id} 的深度无效。` };
      continue;
    }
    const parent = byId.get(candidate.parentId);
    if (!parent || parent.depth + 1 !== candidate.depth || parent.category !== candidate.category) {
      return { ok: false, error: `候选 ${candidate.id} 的父分支无效。` };
    }
  }

  const computedDepth = Math.max(...candidates.map((candidate) => candidate.depth));
  const maxDepth = Math.max(1, nonNegativeInteger(source.max_depth) ?? computedDepth);
  const actualDepth = Math.max(1, nonNegativeInteger(source.actual_depth) ?? computedDepth);
  if (computedDepth > maxDepth || actualDepth !== computedDepth) {
    return { ok: false, error: '消融计划的树深度与候选谱系不一致。' };
  }
  const selectedIds = candidateIds(source.selected);
  for (const id of selectedIds) {
    if (!byId.has(id)) return { ok: false, error: `入选候选 ${id} 不在候选树中。` };
  }
  if (selectedIds.size > maxExperiments) return { ok: false, error: '入选候选超过实验预算。' };
  const expandedParentIds = stringSet(source.expanded_parent_ids);
  const prunedIds = stringSet(source.pruned_ids);
  for (const id of expandedParentIds) {
    if (!byId.has(id) || !candidates.some((candidate) => candidate.parentId === id)) {
      return { ok: false, error: `展开父分支 ${id} 没有对应子节点。` };
    }
  }
  for (const id of prunedIds) {
    if (!byId.has(id) || selectedIds.has(id)) return { ok: false, error: `剪枝候选 ${id} 的状态无效。` };
  }

  return {
    ok: true,
    plan: {
      strategy: 'bounded_tree_of_thoughts',
      maxDepth,
      actualDepth,
      branchLimit,
      budget: { maxExperiments, maxGpuMinutes, maxWallMinutes },
      candidates,
      selectedIds,
      expandedParentIds,
      prunedIds,
      selectionReason: text(source.selection_reason),
    },
  };
};
