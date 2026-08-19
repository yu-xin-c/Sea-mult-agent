export interface ExperimentCandidateView {
  id: string;
  parentId: string;
  strategy: string;
  parameters: Record<string, unknown>;
  depth: number;
  changedParameter: string;
  reason: string;
}

export interface ExperimentParameterView {
  name: string;
  description: string;
  values: unknown[];
  defaultValue: unknown;
}

export interface ExperimentStrategyView {
  name: string;
  description: string;
  parameters: ExperimentParameterView[];
}

export interface ExperimentPolicyDecisionView {
  phase: string;
  policyVersion: string;
  route: string;
  frontierKind: string;
  beamRank: number;
  routeVisitCount: number;
  routeMeanReward: number;
  routeTopKMeanReward: number;
  routeBestReward: number;
  routeExplorationBonus: number;
  nodeVisitCount: number;
  nodeMeanReward: number;
  nodeExplorationBonus: number;
  virtualVisits: number;
  selectionScore: number;
  propensity: number;
  predictedReward: number | null;
  reasonCodes: string[];
  fallback: boolean;
}

export interface ExperimentTrialView {
  number: number;
  batch: number;
  worker: number;
  agentId: string;
  dispatchOrder: number;
  completionOrder: number;
  candidate: ExperimentCandidateView;
  backpropPath: string[];
  status: string;
  decision: string;
  reason: string;
  metrics: Record<string, number>;
  score: number | null;
  deltaFromBest: number | null;
  reward: number | null;
  policyDecision: ExperimentPolicyDecisionView | null;
  durationMs: number;
  error: string;
}

export interface ExperimentRankedCandidateView {
  trialNumber: number;
  candidate: ExperimentCandidateView;
  score: number;
  reward: number;
  durationMs: number;
}

export interface ExperimentRouteSummaryView {
  strategy: string;
  defaultScore: number | null;
  trialCount: number;
  bestScore: number | null;
  topKMeanReward: number;
  topCandidates: ExperimentRankedCandidateView[];
}

export interface ExperimentLedgerView {
  version: 'experiment.ledger/v1';
  campaignId: string;
  status: string;
  domain: string;
  adapter: string;
  metricKey: string;
  direction: 'maximize' | 'minimize';
  targetScore: number | null;
  baselineScore: number;
  bestScore: number;
  maxTrials: number;
  maxParallelTrials: number;
  beamWidth: number;
  explorationSlots: number;
  evaluationIsolation: string;
  schedulingPolicy: string;
  ablationPlanSha256: string;
  designedBranches: number;
  completedTrials: number;
  acceptedTrials: number;
  stopReason: string;
  bestCandidate: ExperimentCandidateView;
  strategySpace: ExperimentStrategyView[];
  routeSummaries: ExperimentRouteSummaryView[];
  trials: ExperimentTrialView[];
  resourceUsage: { evaluatorRuns: number; evaluatorTimeMs: number; wallDurationMs: number; peakParallelism: number; workerSlots: number };
}

export interface ExperimentEvidenceView {
  caseId: string;
  expected: string[];
  observed: string[];
  metrics: Record<string, number>;
  details: Record<string, unknown>;
}

export interface ExperimentValidationRunView {
  number: number;
  status: string;
  baselineScore: number;
  candidateScore: number;
  delta: number;
  targetReached: boolean;
  durationMs: number;
  evidence: ExperimentEvidenceView[];
  error: string;
}

export interface ExperimentValidationView {
  version: 'experiment.validation/v1';
  status: string;
  domain: string;
  adapter: string;
  metricKey: string;
  searchBaseline: number;
  searchBest: number;
  holdoutTarget: number | null;
  requestedRuns: number;
  passedRuns: number;
  protectedIntact: boolean;
  summary: string;
  runs: ExperimentValidationRunView[];
}

type LedgerParseResult = { ok: true; ledger: ExperimentLedgerView } | { ok: false; error: string };
type ValidationParseResult = { ok: true; validation: ExperimentValidationView } | { ok: false; error: string };

const objectValue = (value: unknown): Record<string, unknown> | null =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null;

const textValue = (value: unknown): string => typeof value === 'string' ? value : '';
const numberValue = (value: unknown): number | null => typeof value === 'number' && Number.isFinite(value) ? value : null;
const integerValue = (value: unknown): number | null => {
  const number = numberValue(value);
  return number !== null && Number.isInteger(number) && number >= 0 ? number : null;
};

const decode = (raw: string | unknown): unknown => typeof raw === 'string' ? JSON.parse(raw) : raw;

const parseNumberMap = (value: unknown): Record<string, number> | null => {
  const object = objectValue(value);
  if (!object) return null;
  const entries = Object.entries(object);
  if (entries.some(([, item]) => numberValue(item) === null)) return null;
  return Object.fromEntries(entries) as Record<string, number>;
};

const parseCandidate = (value: unknown): ExperimentCandidateView | null => {
  const candidate = objectValue(value);
  const parameters = objectValue(candidate?.parameters);
  const depth = integerValue(candidate?.depth);
  if (!candidate || !textValue(candidate.id) || !textValue(candidate.strategy) || !parameters || depth === null) return null;
  return {
    id: textValue(candidate.id),
    parentId: textValue(candidate.parent_id),
    strategy: textValue(candidate.strategy),
    parameters,
    depth,
    changedParameter: textValue(candidate.changed_parameter),
    reason: textValue(candidate.reason),
  };
};

const parseParameter = (value: unknown): ExperimentParameterView | null => {
  const parameter = objectValue(value);
  if (!parameter || !textValue(parameter.name) || !Array.isArray(parameter.values) || parameter.values.length === 0 || !Object.hasOwn(parameter, 'default')) return null;
  return {
    name: textValue(parameter.name),
    description: textValue(parameter.description),
    values: parameter.values,
    defaultValue: parameter.default,
  };
};

const parseStrategy = (value: unknown): ExperimentStrategyView | null => {
  const strategy = objectValue(value);
  if (!strategy || !textValue(strategy.name) || !Array.isArray(strategy.parameters)) return null;
  const parameters = strategy.parameters.map(parseParameter);
  if (parameters.some((parameter) => parameter === null)) return null;
  return {
    name: textValue(strategy.name),
    description: textValue(strategy.description),
    parameters: parameters as ExperimentParameterView[],
  };
};

const parsePolicyDecision = (value: unknown): ExperimentPolicyDecisionView | null => {
  if (value === undefined || value === null) return null;
  const decision = objectValue(value);
  const propensity = numberValue(decision?.propensity);
  const predictedReward = decision?.predicted_reward === undefined || decision.predicted_reward === null ? null : numberValue(decision.predicted_reward);
  const routeVisitCount = integerValue(decision?.route_visit_count ?? 0);
  const beamRank = integerValue(decision?.beam_rank ?? 0);
  const nodeVisitCount = integerValue(decision?.node_visit_count ?? 0);
  const virtualVisits = integerValue(decision?.virtual_visits ?? 0);
  const statistics = [
    decision?.route_mean_reward ?? 0,
    decision?.route_top_k_mean_reward ?? 0,
    decision?.route_best_reward ?? 0,
    decision?.route_exploration_bonus ?? 0,
    decision?.node_mean_reward ?? 0,
    decision?.node_exploration_bonus ?? 0,
    decision?.selection_score ?? 0,
  ].map(numberValue);
  if (!decision || !textValue(decision.policy_version) || propensity === null || propensity <= 0 || propensity > 1 ||
      predictedReward === null && decision.predicted_reward !== null && decision.predicted_reward !== undefined ||
      routeVisitCount === null || beamRank === null || nodeVisitCount === null || virtualVisits === null || statistics.some((item) => item === null) ||
      !Array.isArray(decision.reason_codes) || typeof decision.fallback !== 'boolean') return null;
  return {
    phase: textValue(decision.phase),
    policyVersion: textValue(decision.policy_version),
    route: textValue(decision.route),
    frontierKind: textValue(decision.frontier_kind),
    beamRank,
    routeVisitCount,
    routeMeanReward: statistics[0] as number,
    routeTopKMeanReward: statistics[1] as number,
    routeBestReward: statistics[2] as number,
    routeExplorationBonus: statistics[3] as number,
    nodeVisitCount,
    nodeMeanReward: statistics[4] as number,
    nodeExplorationBonus: statistics[5] as number,
    virtualVisits,
    selectionScore: statistics[6] as number,
    propensity,
    predictedReward,
    reasonCodes: decision.reason_codes.filter((item): item is string => typeof item === 'string'),
    fallback: decision.fallback,
  };
};

const parseTrial = (value: unknown): ExperimentTrialView | null => {
  const trial = objectValue(value);
  const number = integerValue(trial?.number);
  const batch = integerValue(trial?.batch ?? 0);
  const worker = integerValue(trial?.worker ?? 1);
  const dispatchOrder = integerValue(trial?.dispatch_order ?? number ?? 0);
  const completionOrder = integerValue(trial?.completion_order ?? number ?? 0);
  const candidate = parseCandidate(trial?.candidate);
  const backpropPath = Array.isArray(trial?.backprop_path) ? trial.backprop_path.filter((item): item is string => typeof item === 'string') : [];
  const score = trial?.score === undefined || trial.score === null ? null : numberValue(trial.score);
  const delta = trial?.delta_from_best === undefined || trial.delta_from_best === null ? null : numberValue(trial.delta_from_best);
  const reward = trial?.reward === undefined || trial.reward === null ? null : numberValue(trial.reward);
  const policyDecision = parsePolicyDecision(trial?.policy_decision);
  const duration = integerValue(trial?.duration_ms);
  const metrics = parseNumberMap(trial?.metrics ?? {});
  if (!trial || number === null || batch === null || worker === null || dispatchOrder === null || completionOrder === null || !candidate || score === null && trial.score !== null && trial.score !== undefined ||
      delta === null && trial.delta_from_best !== null && trial.delta_from_best !== undefined ||
      reward === null && trial.reward !== null && trial.reward !== undefined ||
      policyDecision === null && trial.policy_decision !== null && trial.policy_decision !== undefined || duration === null || !metrics) return null;
  return {
    number,
    batch,
    worker,
    agentId: textValue(trial.agent_id) || (number === 0 ? 'baseline' : `search-agent-${String(worker).padStart(2, '0')}`),
    dispatchOrder,
    completionOrder,
    candidate,
    backpropPath,
    status: textValue(trial.status),
    decision: textValue(trial.decision),
    reason: textValue(trial.reason),
    metrics,
    score,
    deltaFromBest: delta,
    reward,
    policyDecision,
    durationMs: duration,
    error: textValue(trial.error),
  };
};

const parseRankedCandidate = (value: unknown): ExperimentRankedCandidateView | null => {
  const ranked = objectValue(value);
  const trialNumber = integerValue(ranked?.trial_number);
  const candidate = parseCandidate(ranked?.candidate);
  const score = numberValue(ranked?.score);
  const reward = numberValue(ranked?.reward);
  const durationMs = integerValue(ranked?.duration_ms);
  if (!ranked || trialNumber === null || !candidate || score === null || reward === null || durationMs === null) return null;
  return { trialNumber, candidate, score, reward, durationMs };
};

const parseRouteSummary = (value: unknown): ExperimentRouteSummaryView | null => {
  const summary = objectValue(value);
  const defaultScore = summary?.default_score === undefined || summary.default_score === null ? null : numberValue(summary.default_score);
  const bestScore = summary?.best_score === undefined || summary.best_score === null ? null : numberValue(summary.best_score);
  const trialCount = integerValue(summary?.trial_count);
  const topKMeanReward = numberValue(summary?.top_k_mean_reward);
  if (!summary || !textValue(summary.strategy) || defaultScore === null && summary.default_score !== null && summary.default_score !== undefined ||
      bestScore === null && summary.best_score !== null && summary.best_score !== undefined || trialCount === null || topKMeanReward === null || !Array.isArray(summary.top_candidates)) return null;
  const topCandidates = summary.top_candidates.map(parseRankedCandidate);
  if (topCandidates.some((candidate) => candidate === null)) return null;
  return { strategy: textValue(summary.strategy), defaultScore, trialCount, bestScore, topKMeanReward, topCandidates: topCandidates as ExperimentRankedCandidateView[] };
};

export function parseExperimentLedger(raw: string | unknown): LedgerParseResult {
  try {
    const value = objectValue(decode(raw));
    if (!value || value.version !== 'experiment.ledger/v1') return { ok: false, error: '实验账本版本不受支持。' };
    const direction = value.direction;
    const target = value.target_score === undefined || value.target_score === null ? null : numberValue(value.target_score);
    const baseline = numberValue(value.baseline_score);
    const best = numberValue(value.best_score);
    const maxTrials = integerValue(value.max_trials);
    const maxParallelTrials = integerValue(value.max_parallel_trials ?? 1);
    const beamWidth = integerValue(value.beam_width ?? 3);
    const explorationSlots = integerValue(value.exploration_slots ?? 1);
    const designedBranches = integerValue(value.designed_branches ?? 0);
    const completed = integerValue(value.completed_trials);
    const accepted = integerValue(value.accepted_trials);
    const bestCandidate = parseCandidate(value.best_candidate);
    const rawUsage = objectValue(value.resource_usage);
    const evaluatorRuns = integerValue(rawUsage?.evaluator_runs);
    const evaluatorTime = integerValue(rawUsage?.evaluator_time_ms);
    const wallDuration = integerValue(rawUsage?.wall_duration_ms);
    const peakParallelism = integerValue(rawUsage?.peak_parallelism ?? 1);
    const workerSlots = integerValue(rawUsage?.worker_slots ?? maxParallelTrials ?? 1);
    if ((direction !== 'maximize' && direction !== 'minimize') || baseline === null || best === null || maxTrials === null || maxParallelTrials === null || maxParallelTrials < 1 || beamWidth === null || beamWidth < 1 || explorationSlots === null || explorationSlots < 1 || designedBranches === null || completed === null || accepted === null || !bestCandidate || !Array.isArray(value.trials) || !rawUsage || evaluatorRuns === null || evaluatorTime === null || wallDuration === null || peakParallelism === null || peakParallelism < 1 || workerSlots === null || workerSlots < 1 || target === null && value.target_score !== null && value.target_score !== undefined) {
      return { ok: false, error: '实验账本缺少候选、指标或预算字段。' };
    }
    const trials = value.trials.map(parseTrial);
    if (trials.some((trial) => trial === null)) return { ok: false, error: '实验账本包含无效候选。' };
    const rawStrategySpace = value.strategy_space === undefined ? [] : value.strategy_space;
    if (!Array.isArray(rawStrategySpace)) return { ok: false, error: '实验账本中的策略空间无效。' };
    const strategySpace = rawStrategySpace.map(parseStrategy);
    if (strategySpace.some((strategy) => strategy === null)) return { ok: false, error: '实验账本中的方法或参数定义无效。' };
    const normalizedTrials = trials as ExperimentTrialView[];
    const rawRouteSummaries = value.route_summaries === undefined ? [] : value.route_summaries;
    if (!Array.isArray(rawRouteSummaries)) return { ok: false, error: '实验账本中的路线榜单无效。' };
    const routeSummaries = rawRouteSummaries.map(parseRouteSummary);
    if (routeSummaries.some((summary) => summary === null)) return { ok: false, error: '实验账本中的路线候选无效。' };
    const ids = new Set(normalizedTrials.map((trial) => trial.candidate.id));
    if (ids.size !== normalizedTrials.length || normalizedTrials[0]?.number !== 0) return { ok: false, error: '实验候选重复或缺少基线。' };
    for (const trial of normalizedTrials) {
      if (trial.candidate.parentId && !ids.has(trial.candidate.parentId)) return { ok: false, error: '实验候选父节点不存在。' };
    }
    return {
      ok: true,
      ledger: {
        version: 'experiment.ledger/v1',
        campaignId: textValue(value.campaign_id),
        status: textValue(value.status),
        domain: textValue(value.domain),
        adapter: textValue(value.adapter),
        metricKey: textValue(value.metric_key),
        direction,
        targetScore: target,
        baselineScore: baseline,
        bestScore: best,
        maxTrials,
        maxParallelTrials,
        beamWidth,
        explorationSlots,
        evaluationIsolation: textValue(value.evaluation_isolation) || 'serial/v1',
        schedulingPolicy: textValue(value.scheduling_policy) || 'bounded-batch/v1',
        ablationPlanSha256: textValue(value.ablation_plan_sha256),
        designedBranches,
        completedTrials: completed,
        acceptedTrials: accepted,
        stopReason: textValue(value.stop_reason),
        bestCandidate,
        strategySpace: strategySpace as ExperimentStrategyView[],
        routeSummaries: routeSummaries as ExperimentRouteSummaryView[],
        trials: normalizedTrials,
        resourceUsage: { evaluatorRuns, evaluatorTimeMs: evaluatorTime, wallDurationMs: wallDuration, peakParallelism, workerSlots },
      },
    };
  } catch {
    return { ok: false, error: '实验账本 JSON 无法解析。' };
  }
}

const parseEvidence = (value: unknown): ExperimentEvidenceView | null => {
  const evidence = objectValue(value);
  if (!evidence || !textValue(evidence.case_id)) return null;
  const expected = Array.isArray(evidence.expected) ? evidence.expected.filter((item): item is string => typeof item === 'string') : [];
  const observed = Array.isArray(evidence.observed) ? evidence.observed.filter((item): item is string => typeof item === 'string') : [];
  const metrics = parseNumberMap(evidence.metrics ?? {});
  const details = objectValue(evidence.details ?? {});
  if (!metrics || !details) return null;
  return { caseId: textValue(evidence.case_id), expected, observed, metrics, details };
};

const parseValidationRun = (value: unknown): ExperimentValidationRunView | null => {
  const run = objectValue(value);
  const number = integerValue(run?.number);
  const baseline = numberValue(run?.baseline_score);
  const candidate = numberValue(run?.candidate_score);
  const delta = numberValue(run?.delta);
  const duration = integerValue(run?.duration_ms);
  if (!run || number === null || baseline === null || candidate === null || delta === null || duration === null || typeof run.target_reached !== 'boolean' || !Array.isArray(run.evidence)) return null;
  const evidence = run.evidence.map(parseEvidence);
  if (evidence.some((item) => item === null)) return null;
  return {
    number,
    status: textValue(run.status),
    baselineScore: baseline,
    candidateScore: candidate,
    delta,
    targetReached: run.target_reached,
    durationMs: duration,
    evidence: evidence as ExperimentEvidenceView[],
    error: textValue(run.error),
  };
};

export function parseExperimentValidation(raw: string | unknown): ValidationParseResult {
  try {
    const value = objectValue(decode(raw));
    if (!value || value.version !== 'experiment.validation/v1') return { ok: false, error: '验收报告版本不受支持。' };
    const searchBaseline = numberValue(value.search_baseline);
    const searchBest = numberValue(value.search_best);
    const target = value.holdout_target === undefined || value.holdout_target === null ? null : numberValue(value.holdout_target);
    const requested = integerValue(value.requested_runs);
    const passed = integerValue(value.passed_runs);
    if (searchBaseline === null || searchBest === null || requested === null || passed === null || typeof value.protected_intact !== 'boolean' || !Array.isArray(value.runs) || target === null && value.holdout_target !== null && value.holdout_target !== undefined) {
      return { ok: false, error: '验收报告缺少分数、轮次或完整性字段。' };
    }
    const runs = value.runs.map(parseValidationRun);
    if (runs.some((run) => run === null)) return { ok: false, error: '验收报告包含无效运行。' };
    return {
      ok: true,
      validation: {
        version: 'experiment.validation/v1',
        status: textValue(value.status),
        domain: textValue(value.domain),
        adapter: textValue(value.adapter),
        metricKey: textValue(value.metric_key),
        searchBaseline,
        searchBest,
        holdoutTarget: target,
        requestedRuns: requested,
        passedRuns: passed,
        protectedIntact: value.protected_intact,
        summary: textValue(value.summary),
        runs: runs as ExperimentValidationRunView[],
      },
    };
  } catch {
    return { ok: false, error: '验收报告 JSON 无法解析。' };
  }
}

export const formatExperimentNumber = (value: number): string =>
  new Intl.NumberFormat('zh-CN', { maximumSignificantDigits: 6 }).format(value);

export const formatExperimentDuration = (milliseconds: number): string => {
  if (milliseconds < 1000) return `${Math.round(milliseconds)} ms`;
  if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(1)} s`;
  return `${Math.floor(milliseconds / 60_000)}m ${Math.round(milliseconds % 60_000 / 1000)}s`;
};
