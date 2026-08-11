export interface AutoResearchPatchView {
  path: string;
  reason: string;
  beforeSha256: string;
  afterSha256: string;
}

export interface AutoResearchTrialView {
  number: number;
  status: string;
  decision: string;
  diagnosis: string;
  hypothesis: string;
  reason: string;
  metric: number | null;
  metricSamples: number[];
  metricStdDev: number;
  metricAggregation: 'mean' | 'median' | 'worst';
  deltaFromBest: number | null;
  durationMs: number;
  patches: AutoResearchPatchView[];
}

export interface AutoResearchResourceView {
  commandRuns: number;
  guardRuns: number;
  evaluatorRuns: number;
  successfulCommands: number;
  failedCommands: number;
  commandDurationMs: number;
  wallDurationMs: number;
}

export interface AutoResearchLedgerView {
  version: 'autoresearch.ledger/v1';
  status: string;
  metricKey: string;
  direction: 'maximize' | 'minimize';
  targetScore: number | null;
  searchRuns: number;
  searchAggregation: 'mean' | 'median' | 'worst';
  baselineScore: number;
  bestScore: number;
  maxTrials: number;
  completedTrials: number;
  acceptedTrials: number;
  stopReason: string;
  resourceUsage: AutoResearchResourceView | null;
  trials: AutoResearchTrialView[];
}

export interface AutoResearchValidationRunView {
  number: number;
  status: string;
  observedScore: number | null;
  deltaFromBaseline: number | null;
  scoreMatches: boolean;
  error: string;
  durationMs: number;
}

export interface AutoResearchValidationView {
  version: 'autoresearch.validation/v1';
  status: string;
  validationMode: 'hidden_holdout' | 'search_evaluator_replay';
  metricKey: string;
  searchBestScore: number;
  holdoutBaselineScore: number | null;
  expectedScore: number;
  meanScore: number;
  stddev: number;
  requestedRuns: number;
  completedRuns: number;
  passedRuns: number;
  failedRuns: number;
  failureRate: number;
  protectedIntact: boolean;
  workspaceIntact: boolean;
  candidateIntact: boolean;
  summary: string;
  resourceUsage: AutoResearchResourceView | null;
  runs: AutoResearchValidationRunView[];
}

export type AutoResearchLedgerParseResult =
  | { ok: true; ledger: AutoResearchLedgerView }
  | { ok: false; error: string };

export type AutoResearchValidationParseResult =
  | { ok: true; validation: AutoResearchValidationView }
  | { ok: false; error: string };

type JsonObject = Record<string, unknown>;

const asObject = (value: unknown): JsonObject | null =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? (value as JsonObject) : null;

const finiteNumber = (value: unknown): number | null =>
  typeof value === 'number' && Number.isFinite(value) ? value : null;

const integer = (value: unknown): number | null =>
  typeof value === 'number' && Number.isInteger(value) && value >= 0 ? value : null;

const textValue = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

const commandDuration = (value: unknown): number => {
  const command = asObject(value);
  return Math.max(0, finiteNumber(command?.duration_ms) ?? 0);
};

const trialDuration = (trial: JsonObject): number => {
  const startedAt = Date.parse(textValue(trial.started_at));
  const finishedAt = Date.parse(textValue(trial.finished_at));
  if (Number.isFinite(startedAt) && Number.isFinite(finishedAt) && finishedAt >= startedAt) {
    return finishedAt - startedAt;
  }

  const guards = Array.isArray(trial.guard_results) ? trial.guard_results.reduce((sum, item) => sum + commandDuration(item), 0) : 0;
  const evaluations = Array.isArray(trial.eval_results)
    ? trial.eval_results.reduce((sum, item) => sum + commandDuration(item), 0)
    : commandDuration(trial.eval_result);
  return guards + evaluations;
};

const parsePatch = (value: unknown): AutoResearchPatchView | null => {
  const patch = asObject(value);
  if (!patch) return null;
  const path = textValue(patch.path);
  if (!path) return null;
  return {
    path,
    reason: textValue(patch.reason),
    beforeSha256: textValue(patch.before_sha256),
    afterSha256: textValue(patch.after_sha256),
  };
};

const parseTrial = (value: unknown, baselineScore: number): AutoResearchTrialView | null => {
  const trial = asObject(value);
  if (!trial) return null;
  const number = integer(trial.number);
  const status = textValue(trial.status);
  const decision = textValue(trial.decision);
  if (number === null || !status || !decision) return null;

  const metric = finiteNumber(trial.metric) ?? (number === 0 ? baselineScore : null);
  const metricSamples = Array.isArray(trial.metric_samples)
    ? trial.metric_samples.map(finiteNumber).filter((sample): sample is number => sample !== null)
    : (metric === null ? [] : [metric]);
  const metricAggregation = trial.metric_aggregation === 'median' || trial.metric_aggregation === 'worst'
    ? trial.metric_aggregation
    : 'mean';
  const patches = Array.isArray(trial.patches)
    ? trial.patches.map(parsePatch).filter((item): item is AutoResearchPatchView => item !== null)
    : [];

  return {
    number,
    status,
    decision,
    diagnosis: textValue(trial.diagnosis),
    hypothesis: textValue(trial.hypothesis),
    reason: textValue(trial.reason),
    metric,
    metricSamples,
    metricStdDev: finiteNumber(trial.metric_stddev) ?? 0,
    metricAggregation,
    deltaFromBest: finiteNumber(trial.delta_from_best),
    durationMs: trialDuration(trial),
    patches,
  };
};

const parseResourceUsage = (value: unknown): AutoResearchResourceView | null => {
  const usage = asObject(value);
  if (!usage) return null;
  const commandRuns = integer(usage.command_runs);
  const guardRuns = integer(usage.guard_runs);
  const evaluatorRuns = integer(usage.evaluator_runs);
  const successfulCommands = integer(usage.successful_commands);
  const failedCommands = integer(usage.failed_commands);
  const commandDurationMs = finiteNumber(usage.command_duration_ms);
  const wallDurationMs = finiteNumber(usage.wall_duration_ms);
  if (
    commandRuns === null ||
    guardRuns === null ||
    evaluatorRuns === null ||
    successfulCommands === null ||
    failedCommands === null ||
    commandDurationMs === null || commandDurationMs < 0 ||
    wallDurationMs === null || wallDurationMs < 0 ||
    commandRuns !== guardRuns + evaluatorRuns ||
    commandRuns !== successfulCommands + failedCommands
  ) {
    return null;
  }
  return {
    commandRuns,
    guardRuns,
    evaluatorRuns,
    successfulCommands,
    failedCommands,
    commandDurationMs,
    wallDurationMs,
  };
};

const parseValidationRun = (value: unknown): AutoResearchValidationRunView | null => {
  const run = asObject(value);
  if (!run) return null;
  const number = integer(run.number);
  const status = textValue(run.status);
  if (number === null || number < 1 || !status || typeof run.score_matches !== 'boolean') return null;
  const startedAt = Date.parse(textValue(run.started_at));
  const finishedAt = Date.parse(textValue(run.finished_at));
  const durationMs = Number.isFinite(startedAt) && Number.isFinite(finishedAt) && finishedAt >= startedAt
    ? finishedAt - startedAt
    : 0;
  return {
    number,
    status,
    observedScore: finiteNumber(run.observed_score),
    deltaFromBaseline: finiteNumber(run.delta_from_baseline),
    scoreMatches: run.score_matches,
    error: textValue(run.error),
    durationMs,
  };
};

const decodeInput = (raw: string | unknown): unknown => {
  let value: unknown = raw;
  for (let pass = 0; pass < 2 && typeof value === 'string'; pass += 1) {
    value = JSON.parse(value);
  }
  return value;
};

export function parseAutoResearchLedger(raw: string | unknown): AutoResearchLedgerParseResult {
  try {
    const candidate = asObject(decodeInput(raw));
    if (!candidate) return { ok: false, error: '实验账本不是 JSON 对象。' };
    if (candidate.version !== 'autoresearch.ledger/v1') {
      return { ok: false, error: '实验账本版本不受支持。' };
    }

    const metricKey = textValue(candidate.metric_key);
    const direction = candidate.direction;
    const targetScore = candidate.target_score === undefined || candidate.target_score === null
      ? null
      : finiteNumber(candidate.target_score);
    const rawSearchRuns = integer(candidate.search_runs);
    const searchRuns = rawSearchRuns === null || rawSearchRuns < 1 ? 1 : rawSearchRuns;
    const searchAggregation = candidate.search_aggregation === 'median' || candidate.search_aggregation === 'worst'
      ? candidate.search_aggregation
      : 'mean';
    const baselineScore = finiteNumber(candidate.baseline_score);
    const bestScore = finiteNumber(candidate.best_score);
    const maxTrials = integer(candidate.max_trials);
    const completedTrials = integer(candidate.completed_trials);
    const acceptedTrials = integer(candidate.accepted_trials);
    const resourceUsage = candidate.resource_usage === undefined ? null : parseResourceUsage(candidate.resource_usage);
    if (
      !metricKey ||
      (direction !== 'maximize' && direction !== 'minimize') ||
      (candidate.target_score !== undefined && candidate.target_score !== null && targetScore === null) ||
      baselineScore === null ||
      bestScore === null ||
      maxTrials === null ||
      completedTrials === null ||
      acceptedTrials === null ||
      !Array.isArray(candidate.trials)
    ) {
      return { ok: false, error: '实验账本缺少指标、预算或试验字段。' };
    }
    if (candidate.resource_usage !== undefined && resourceUsage === null) {
      return { ok: false, error: '实验账本的执行资源摘要不一致。' };
    }

    const trials = candidate.trials.map((trial) => parseTrial(trial, baselineScore));
    if (trials.some((trial) => trial === null)) {
      return { ok: false, error: '实验账本包含无法识别的试验记录。' };
    }
    const normalizedTrials = trials as AutoResearchTrialView[];
    const numbers = new Set(normalizedTrials.map((trial) => trial.number));
    if (numbers.size !== normalizedTrials.length || !numbers.has(0)) {
      return { ok: false, error: '实验编号重复或缺少 baseline。' };
    }

    return {
      ok: true,
      ledger: {
        version: 'autoresearch.ledger/v1',
        status: textValue(candidate.status),
        metricKey,
        direction,
        targetScore,
        searchRuns,
        searchAggregation,
        baselineScore,
        bestScore,
        maxTrials,
        completedTrials,
        acceptedTrials,
        stopReason: textValue(candidate.stop_reason),
        resourceUsage,
        trials: normalizedTrials.sort((left, right) => left.number - right.number),
      },
    };
  } catch {
    return { ok: false, error: '实验账本 JSON 无法解析。' };
  }
}

export function parseAutoResearchValidationReport(raw: string | unknown): AutoResearchValidationParseResult {
  try {
    const candidate = asObject(decodeInput(raw));
    if (!candidate || candidate.version !== 'autoresearch.validation/v1') {
      return { ok: false, error: '验收报告版本不受支持。' };
    }
    const validationMode = candidate.validation_mode;
    const metricKey = textValue(candidate.metric_key);
    const searchBestScore = finiteNumber(candidate.search_best_score);
    const expectedScore = finiteNumber(candidate.expected_score);
    const meanScore = finiteNumber(candidate.mean_score);
    const stddev = finiteNumber(candidate.stddev);
    const requestedRuns = integer(candidate.requested_runs);
    const completedRuns = integer(candidate.completed_runs);
    const passedRuns = integer(candidate.passed_runs);
    const failedRuns = integer(candidate.failed_runs);
    const failureRate = finiteNumber(candidate.failure_rate);
    const resourceUsage = candidate.resource_usage === undefined ? null : parseResourceUsage(candidate.resource_usage);
    if (
      (validationMode !== 'hidden_holdout' && validationMode !== 'search_evaluator_replay') ||
      !metricKey || searchBestScore === null || expectedScore === null || meanScore === null || stddev === null ||
      requestedRuns === null || completedRuns === null || passedRuns === null || failedRuns === null ||
      failureRate === null || failureRate < 0 || failureRate > 1 || !Array.isArray(candidate.runs) ||
      typeof candidate.protected_intact !== 'boolean' || typeof candidate.workspace_intact !== 'boolean' ||
      typeof candidate.candidate_intact !== 'boolean'
    ) {
      return { ok: false, error: '验收报告缺少模式、指标、轮次或完整性字段。' };
    }
    if (candidate.resource_usage !== undefined && resourceUsage === null) {
      return { ok: false, error: '验收报告的执行资源摘要不一致。' };
    }
    const runs = candidate.runs.map(parseValidationRun);
    if (runs.some((run) => run === null)) {
      return { ok: false, error: '验收报告包含无法识别的运行记录。' };
    }
    return {
      ok: true,
      validation: {
        version: 'autoresearch.validation/v1',
        status: textValue(candidate.status),
        validationMode,
        metricKey,
        searchBestScore,
        holdoutBaselineScore: finiteNumber(candidate.holdout_baseline_score),
        expectedScore,
        meanScore,
        stddev,
        requestedRuns,
        completedRuns,
        passedRuns,
        failedRuns,
        failureRate,
        protectedIntact: candidate.protected_intact,
        workspaceIntact: candidate.workspace_intact,
        candidateIntact: candidate.candidate_intact,
        summary: textValue(candidate.summary),
        resourceUsage,
        runs: runs as AutoResearchValidationRunView[],
      },
    };
  } catch {
    return { ok: false, error: '验收报告 JSON 无法解析。' };
  }
}

export const autoResearchGain = (ledger: AutoResearchLedgerView): number =>
  ledger.direction === 'minimize'
    ? ledger.baselineScore - ledger.bestScore
    : ledger.bestScore - ledger.baselineScore;

export const formatResearchMetric = (value: number): string =>
  new Intl.NumberFormat('zh-CN', { maximumSignificantDigits: 6 }).format(value);

export const formatResearchDuration = (durationMs: number): string => {
  if (durationMs < 1000) return `${Math.round(durationMs)} ms`;
  if (durationMs < 60_000) return `${(durationMs / 1000).toFixed(durationMs < 10_000 ? 1 : 0)} s`;
  const minutes = Math.floor(durationMs / 60_000);
  const seconds = Math.round((durationMs % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
};
