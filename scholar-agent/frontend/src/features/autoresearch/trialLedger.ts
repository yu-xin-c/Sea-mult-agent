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
  hypothesis: string;
  reason: string;
  metric: number | null;
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
  baselineScore: number;
  bestScore: number;
  maxTrials: number;
  completedTrials: number;
  acceptedTrials: number;
  stopReason: string;
  resourceUsage: AutoResearchResourceView | null;
  trials: AutoResearchTrialView[];
}

export type AutoResearchLedgerParseResult =
  | { ok: true; ledger: AutoResearchLedgerView }
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
  return guards + commandDuration(trial.eval_result);
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
  const patches = Array.isArray(trial.patches)
    ? trial.patches.map(parsePatch).filter((item): item is AutoResearchPatchView => item !== null)
    : [];

  return {
    number,
    status,
    decision,
    hypothesis: textValue(trial.hypothesis),
    reason: textValue(trial.reason),
    metric,
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
    const baselineScore = finiteNumber(candidate.baseline_score);
    const bestScore = finiteNumber(candidate.best_score);
    const maxTrials = integer(candidate.max_trials);
    const completedTrials = integer(candidate.completed_trials);
    const acceptedTrials = integer(candidate.accepted_trials);
    const resourceUsage = candidate.resource_usage === undefined ? null : parseResourceUsage(candidate.resource_usage);
    if (
      !metricKey ||
      (direction !== 'maximize' && direction !== 'minimize') ||
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
