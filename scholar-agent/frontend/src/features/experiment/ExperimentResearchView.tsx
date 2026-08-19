import { useMemo, useState } from 'react';
import {
  CheckCircle2,
  BarChart3,
  CircleDot,
  Clock3,
  FlaskConical,
  GitBranch,
  ListOrdered,
  ListTree,
  LockKeyhole,
  ShieldCheck,
  ShieldX,
  SkipForward,
  SlidersHorizontal,
  Target,
  Trophy,
  Waypoints,
  XCircle,
} from 'lucide-react';
import {
  formatExperimentDuration,
  formatExperimentNumber,
  parseExperimentLedger,
  parseExperimentValidation,
  type ExperimentCandidateView,
  type ExperimentLedgerView,
  type ExperimentParameterView,
  type ExperimentRouteSummaryView,
  type ExperimentStrategyView,
  type ExperimentTrialView,
  type ExperimentValidationView,
} from './experimentLedger';

interface ExperimentResearchViewProps {
  raw: string;
  expanded?: boolean;
}

type ResearchViewMode = 'portfolio' | 'tree' | 'timeline';

const parameterText = (parameters: Record<string, unknown>) =>
  Object.entries(parameters).map(([key, value]) => `${key}=${String(value)}`).join(' · ');

const valueText = (value: unknown) => {
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
};

const sameValue = (left: unknown, right: unknown) => valueText(left) === valueText(right);

const stopLabel = (reason: string) => ({
  baseline_target_reached: '基线已达到目标',
  target_score_reached: '达到目标分数',
  trial_budget_exhausted: '实验轮次用尽',
  wall_time_budget_exhausted: '时间预算用尽',
  candidate_space_exhausted: '候选空间已搜索完',
}[reason] ?? reason);

const statusLabel = (trial: ExperimentTrialView | undefined) => {
  if (!trial) return '未执行';
  if (trial.number === 0) return 'Baseline';
  return trial.status === 'kept' ? 'Keep' : trial.status === 'failed' ? 'Failed' : 'Reject';
};

const trialTone = (trial: ExperimentTrialView | undefined, isBest = false) => {
  if (isBest) return 'border-emerald-400 bg-emerald-50/50';
  if (!trial) return 'border-slate-200 bg-slate-50/50';
  if (trial.number === 0 || trial.status === 'kept') return 'border-emerald-200 bg-white';
  if (trial.status === 'failed') return 'border-amber-300 bg-amber-50/40';
  return 'border-rose-200 bg-white';
};

function Summary({ label, value, text, accent = false }: { label: string; value?: number | null; text?: string; accent?: boolean }) {
  return (
    <div className="border-r border-slate-200 px-3 py-2.5 text-center last:border-r-0">
      <div className={`truncate font-mono text-sm font-semibold ${accent ? 'text-emerald-700' : 'text-slate-900'}`}>
        {text ?? (value === null || value === undefined ? '-' : formatExperimentNumber(value))}
      </div>
      <div className="mt-1 text-[10px] text-slate-500">{label}</div>
    </div>
  );
}

function SearchProcessRail({ ledger }: { ledger: ExperimentLedgerView }) {
  const methodTrials = Math.max(0, ledger.trials.filter((trial) => trial.candidate.depth === 0).length - 1);
  const parameterTrials = ledger.trials.filter((trial) => trial.candidate.depth > 0).length;
  const methodTotal = Math.max(0, ledger.strategySpace.length - 1);
  const parameterStopped = parameterTrials === 0 && (ledger.stopReason === 'target_score_reached' || ledger.stopReason === 'baseline_target_reached');
  const parameterCount = ledger.strategySpace.reduce((total, strategy) => total + strategy.parameters.length, 0);
  const steps = [
    { label: '冻结空间', detail: `${ledger.strategySpace.length || new Set(ledger.trials.map((trial) => trial.candidate.strategy)).size} 个 Model · ${parameterCount} 个参数`, icon: LockKeyhole, state: 'done' },
    { label: '默认穷举', detail: `${methodTrials}/${methodTotal || methodTrials} 个默认配置完成`, icon: GitBranch, state: 'done' },
    { label: '路线 UCB', detail: 'Top-K 质量 + 探索 + 成本', icon: Waypoints, state: parameterTrials > 0 ? 'done' : parameterStopped ? 'skipped' : 'pending' },
    { label: 'Beam + UCT', detail: parameterStopped ? '目标已满足，参数搜索跳过' : `${parameterTrials} 个参数候选`, icon: SlidersHorizontal, state: parameterStopped ? 'skipped' : parameterTrials > 0 ? 'done' : 'pending' },
    { label: '异步执行', detail: `${ledger.resourceUsage.workerSlots} Agent · peak ${ledger.resourceUsage.peakParallelism}`, icon: FlaskConical, state: ledger.completedTrials > 0 ? 'done' : 'pending' },
    { label: 'Holdout', detail: '冻结全局最佳后独立验收', icon: ShieldCheck, state: 'pending' },
  ] as const;

  return (
    <div className="grid grid-cols-2 border-b border-slate-200 bg-white md:grid-cols-3 xl:grid-cols-6" aria-label="策略树生成进度">
      {steps.map((step, index) => {
        const Icon = step.icon;
        const done = step.state === 'done';
        const skipped = step.state === 'skipped';
        return (
          <div key={step.label} className="relative min-h-[74px] border-b border-r border-slate-200 px-3 py-3 last:border-r-0 md:border-b-0">
            <div className="flex items-center gap-2">
              <span className={`flex h-6 w-6 shrink-0 items-center justify-center rounded border ${done ? 'border-emerald-200 bg-emerald-50 text-emerald-700' : skipped ? 'border-amber-200 bg-amber-50 text-amber-700' : 'border-slate-200 bg-slate-50 text-slate-400'}`}>
                {skipped ? <SkipForward className="h-3.5 w-3.5" /> : <Icon className="h-3.5 w-3.5" />}
              </span>
              <span className="text-[11px] font-semibold text-slate-800">{index + 1}. {step.label}</span>
            </div>
            <div className="mt-2 text-[10px] leading-4 text-slate-500">{step.detail}</div>
          </div>
        );
      })}
    </div>
  );
}

function ParameterDomain({ parameter }: { parameter: ExperimentParameterView }) {
  return (
    <div className="grid grid-cols-[5rem_minmax(0,1fr)] gap-2 border-t border-slate-100 py-1.5 first:border-t-0" title={parameter.description}>
      <div className="truncate font-mono text-[10px] font-semibold text-slate-600">{parameter.name}</div>
      <div className="flex min-w-0 flex-wrap gap-1">
        {parameter.values.slice(0, 6).map((value, index) => {
          const active = sameValue(value, parameter.defaultValue);
          return (
            <span key={`${parameter.name}-${index}`} className={`rounded border px-1 py-0.5 font-mono text-[9px] ${active ? 'border-cyan-300 bg-cyan-50 font-semibold text-cyan-800' : 'border-slate-200 bg-white text-slate-500'}`}>
              {valueText(value)}
            </span>
          );
        })}
        {parameter.values.length > 6 && <span className="px-1 py-0.5 text-[9px] text-slate-400">+{parameter.values.length - 6}</span>}
      </div>
    </div>
  );
}

function StrategyBranch({
  strategy,
  rootTrial,
  childTrials,
  bestCandidate,
  stopReason,
  metricKey,
  direction,
  selectedCandidateID,
  onSelect,
}: {
  strategy: ExperimentStrategyView;
  rootTrial?: ExperimentTrialView;
  childTrials: ExperimentTrialView[];
  bestCandidate: ExperimentCandidateView;
  stopReason: string;
  metricKey: string;
  direction: 'maximize' | 'minimize';
  selectedCandidateID: string;
  onSelect: (candidateID: string) => void;
}) {
  const isBestBranch = bestCandidate.strategy === strategy.name;
  const stoppedAtTarget = stopReason === 'target_score_reached' || stopReason === 'baseline_target_reached';
  const RootIcon = rootTrial?.number === 0 || rootTrial?.status === 'kept' ? CheckCircle2 : rootTrial ? XCircle : CircleDot;
  const orderedChildren = [...childTrials].sort((left, right) => {
    if (left.score === null) return 1;
    if (right.score === null) return -1;
    return direction === 'minimize' ? left.score - right.score : right.score - left.score;
  });
  return (
    <section className={`relative flex min-w-0 flex-col rounded-md border ${trialTone(rootTrial, isBestBranch)}`}>
      <span className="absolute -top-5 left-1/2 hidden h-5 w-px -translate-x-1/2 bg-slate-300 md:block" aria-hidden="true" />
      <button
        type="button"
        disabled={!rootTrial}
        onClick={() => rootTrial && onSelect(rootTrial.candidate.id)}
        className={`w-full border-b border-slate-200 px-3 py-3 text-left transition-colors ${rootTrial ? 'hover:bg-white/70' : 'cursor-default'} ${selectedCandidateID === rootTrial?.candidate.id ? 'bg-cyan-50/60' : ''}`}
      >
        <div className="flex items-start justify-between gap-2">
          <div className="flex min-w-0 items-start gap-2">
            <RootIcon className={`mt-0.5 h-4 w-4 shrink-0 ${isBestBranch || rootTrial?.number === 0 ? 'text-emerald-700' : rootTrial ? 'text-rose-600' : 'text-slate-400'}`} />
            <div className="min-w-0">
              <div className="truncate font-mono text-xs font-semibold text-slate-900">{strategy.name}</div>
              <div className="mt-1 line-clamp-2 text-[10px] leading-4 text-slate-500">{strategy.description || '方法分支'}</div>
            </div>
          </div>
          <div className="shrink-0 text-right">
            <div className={`font-mono text-sm font-semibold ${isBestBranch ? 'text-emerald-700' : 'text-slate-900'}`}>{rootTrial?.score === null || rootTrial?.score === undefined ? '-' : formatExperimentNumber(rootTrial.score)}</div>
            <div className="text-[9px] text-slate-400">{metricKey}</div>
          </div>
        </div>
        <div className="mt-2 flex items-center justify-between gap-2 text-[9px]">
          <span className={`font-semibold ${isBestBranch || rootTrial?.number === 0 ? 'text-emerald-700' : rootTrial ? 'text-rose-700' : 'text-slate-400'}`}>{statusLabel(rootTrial)}</span>
          {isBestBranch && <span className="inline-flex items-center gap-1 text-emerald-700"><Trophy className="h-3 w-3" />当前最佳</span>}
        </div>
      </button>

      <div className="flex-1 px-3 py-2">
        <div className="mb-1 flex items-center justify-between gap-2">
          <span className="text-[10px] font-semibold text-slate-700">参数搜索域</span>
          <span className="text-[9px] text-slate-400">高亮为默认值</span>
        </div>
        {strategy.parameters.length > 0
          ? strategy.parameters.map((parameter) => <ParameterDomain key={parameter.name} parameter={parameter} />)
          : <div className="py-2 text-[10px] text-slate-400">该方法没有可搜索参数</div>}
      </div>

      {childTrials.length > 0 ? (
        <div className="border-t border-slate-200 px-3 py-2">
          <div className="mb-1 text-[10px] font-semibold text-slate-700">实际参数子节点</div>
          <div className="space-y-1">
            {orderedChildren.map((trial) => (
              <button key={trial.candidate.id} type="button" onClick={() => onSelect(trial.candidate.id)} className={`flex w-full items-center justify-between gap-2 rounded border px-2 py-1.5 text-left ${selectedCandidateID === trial.candidate.id ? 'border-cyan-300 bg-cyan-50' : 'border-slate-200 bg-white hover:border-slate-300'}`}>
                <span className="min-w-0 truncate font-mono text-[9px] text-slate-600">{trial.policyDecision?.frontierKind === 'exploration' ? '探索' : trial.policyDecision?.beamRank ? `Beam #${trial.policyDecision.beamRank}` : '参数'} · {trial.candidate.changedParameter} · d{trial.candidate.depth}</span>
                <span className="shrink-0 font-mono text-[10px] font-semibold text-slate-900">{trial.score === null ? 'N/A' : formatExperimentNumber(trial.score)}</span>
              </button>
            ))}
          </div>
        </div>
      ) : (
        <div className={`flex items-center gap-2 border-t px-3 py-2 text-[9px] ${stoppedAtTarget && rootTrial ? 'border-amber-200 bg-amber-50 text-amber-800' : 'border-slate-200 text-slate-400'}`}>
          {stoppedAtTarget && rootTrial ? <SkipForward className="h-3 w-3 shrink-0" /> : <CircleDot className="h-3 w-3 shrink-0" />}
          <span>{stoppedAtTarget && rootTrial ? '达到目标后，剩余参数候选被剪枝' : rootTrial ? '尚未展开参数子节点' : '方法分支尚未执行'}</span>
        </div>
      )}
    </section>
  );
}

function CandidateDetail({ trial, metricKey }: { trial: ExperimentTrialView; metricKey: string }) {
  const kept = trial.number === 0 || trial.status === 'kept';
  return (
    <aside className="min-w-0 border-t border-slate-200 bg-slate-50/60 p-4 xl:border-l xl:border-t-0">
      <div className="flex items-center justify-between gap-2">
        <div className="text-xs font-semibold text-slate-900">候选详情</div>
        <span className={`rounded border px-1.5 py-0.5 text-[9px] font-semibold ${kept ? 'border-emerald-200 bg-emerald-50 text-emerald-700' : 'border-rose-200 bg-rose-50 text-rose-700'}`}>{statusLabel(trial)}</span>
      </div>
      <div className="mt-4">
        <div className="font-mono text-sm font-semibold text-slate-900">{trial.candidate.strategy}</div>
        <div className="mt-1 break-all font-mono text-[9px] text-slate-400">{trial.candidate.id}</div>
      </div>
      <div className="mt-4 grid grid-cols-2 border-y border-slate-200 py-3">
        <div>
          <div className="font-mono text-lg font-semibold text-slate-900">{trial.score === null ? 'N/A' : formatExperimentNumber(trial.score)}</div>
          <div className="mt-1 text-[9px] text-slate-500">{metricKey}</div>
        </div>
        <div className="border-l border-slate-200 pl-4">
          <div className="font-mono text-sm font-semibold text-slate-900">{formatExperimentDuration(trial.durationMs)}</div>
          <div className="mt-1 text-[9px] text-slate-500">Evaluator 耗时</div>
        </div>
      </div>
      <div className="mt-4">
        <div className="text-[10px] font-semibold text-slate-700">系统判定</div>
        <p className="mt-1 text-[10px] leading-5 text-slate-600">{trial.reason || trial.candidate.reason}</p>
      </div>
      {trial.policyDecision && (
        <div className="mt-4 border-y border-cyan-100 bg-cyan-50/50 px-2 py-2.5">
          <div className="flex items-center justify-between gap-2">
            <div className="text-[10px] font-semibold text-cyan-900">策略选择</div>
            <div className="flex items-center gap-1"><span className={`rounded border px-1.5 py-0.5 text-[9px] font-semibold ${trial.policyDecision.frontierKind === 'exploration' ? 'border-violet-200 bg-violet-50 text-violet-800' : 'border-emerald-200 bg-emerald-50 text-emerald-800'}`}>{trial.policyDecision.frontierKind === 'exploration' ? '探索通道' : trial.policyDecision.beamRank ? `Beam #${trial.policyDecision.beamRank}` : '默认穷举'}</span><span className={`rounded border px-1.5 py-0.5 text-[9px] font-semibold ${trial.policyDecision.fallback ? 'border-amber-200 bg-amber-50 text-amber-800' : 'border-cyan-200 bg-white text-cyan-800'}`}>{trial.policyDecision.fallback ? 'Go fallback' : 'Python Policy'}</span></div>
          </div>
          <div className="mt-2 flex items-center justify-between gap-2 text-[9px]"><span className="break-all font-mono text-slate-700">{trial.policyDecision.policyVersion}</span><span className="shrink-0 font-mono text-cyan-800">{trial.policyDecision.phase || 'legacy'}</span></div>
          <div className="mt-2 grid grid-cols-3 gap-2 border-y border-cyan-100 py-2 text-[9px]">
            <div><div className="font-mono font-semibold text-slate-800">{formatExperimentNumber(trial.policyDecision.routeTopKMeanReward)}</div><div className="mt-0.5 text-slate-500">UCB · Top-K Q</div></div>
            <div><div className="font-mono font-semibold text-slate-800">+{formatExperimentNumber(trial.policyDecision.routeExplorationBonus)}</div><div className="mt-0.5 text-slate-500">路线探索项</div></div>
            <div><div className="font-mono font-semibold text-slate-800">{trial.policyDecision.routeVisitCount}</div><div className="mt-0.5 text-slate-500">路线访问</div></div>
            <div><div className="font-mono font-semibold text-slate-800">{formatExperimentNumber(trial.policyDecision.nodeMeanReward)}</div><div className="mt-0.5 text-slate-500">UCT · Node Q</div></div>
            <div><div className="font-mono font-semibold text-slate-800">+{formatExperimentNumber(trial.policyDecision.nodeExplorationBonus)}</div><div className="mt-0.5 text-slate-500">节点探索项</div></div>
            <div><div className="font-mono font-semibold text-slate-800">{trial.policyDecision.nodeVisitCount}</div><div className="mt-0.5 text-slate-500">节点访问</div></div>
          </div>
          <div className="mt-2 grid grid-cols-3 gap-2 text-[9px]">
            <div><div className="font-mono font-semibold text-slate-800">{formatExperimentNumber(trial.policyDecision.selectionScore)}</div><div className="mt-0.5 text-slate-500">选择分</div></div>
            <div><div className="font-mono font-semibold text-slate-800">{trial.policyDecision.virtualVisits}</div><div className="mt-0.5 text-slate-500">虚拟访问</div></div>
            <div><div className="font-mono font-semibold text-slate-800">{trial.reward === null ? '-' : formatExperimentNumber(trial.reward)}</div><div className="mt-0.5 text-slate-500">实际 Reward</div></div>
          </div>
          {trial.policyDecision.reasonCodes.length > 0 && <div className="mt-2 break-words text-[9px] leading-4 text-cyan-800">{trial.policyDecision.reasonCodes.join(' · ')}</div>}
        </div>
      )}
      <div className="mt-4">
        <div className="text-[10px] font-semibold text-slate-700">实际配置</div>
        <dl className="mt-2 divide-y divide-slate-200 border-y border-slate-200">
          {Object.entries(trial.candidate.parameters).map(([key, value]) => (
            <div key={key} className="flex items-center justify-between gap-3 py-1.5 text-[9px]">
              <dt className="truncate font-mono text-slate-500">{key}</dt>
              <dd className="shrink-0 font-mono font-semibold text-slate-800">{valueText(value)}</dd>
            </div>
          ))}
        </dl>
      </div>
      <div className="mt-4">
        <div className="text-[10px] font-semibold text-slate-700">全部指标</div>
        <div className="mt-2 space-y-1">
          {Object.entries(trial.metrics).map(([key, value]) => (
            <div key={key} className="flex items-center justify-between gap-3 text-[9px]"><span className="font-mono text-slate-500">{key}</span><strong className="font-mono text-slate-800">{formatExperimentNumber(value)}</strong></div>
          ))}
        </div>
      </div>
      {trial.candidate.parentId && <div className="mt-4 break-all border-t border-slate-200 pt-3 text-[9px] text-slate-500">父节点：<span className="font-mono">{trial.candidate.parentId}</span></div>}
    </aside>
  );
}

const fallbackRouteSummaries = (ledger: ExperimentLedgerView): ExperimentRouteSummaryView[] => {
  const routes = new Map<string, ExperimentTrialView[]>();
  ledger.trials.forEach((trial) => {
    if (trial.score === null) return;
    routes.set(trial.candidate.strategy, [...(routes.get(trial.candidate.strategy) ?? []), trial]);
  });
  return [...routes.entries()].map(([strategy, trials]) => {
    const ranked = [...trials].sort((left, right) => {
      const scoreDelta = ledger.direction === 'minimize' ? (left.score ?? 0) - (right.score ?? 0) : (right.score ?? 0) - (left.score ?? 0);
      return scoreDelta || left.durationMs - right.durationMs;
    });
    const top = ranked.slice(0, 3);
    return {
      strategy,
      defaultScore: trials.find((trial) => trial.candidate.depth === 0)?.score ?? null,
      trialCount: trials.length,
      bestScore: top[0]?.score ?? null,
      topKMeanReward: top.reduce((sum, trial) => sum + (trial.reward ?? 0), 0) / Math.max(1, top.length),
      topCandidates: top.map((trial) => ({
        trialNumber: trial.number,
        candidate: trial.candidate,
        score: trial.score ?? 0,
        reward: trial.reward ?? 0,
        durationMs: trial.durationMs,
      })),
    };
  });
};

function RoutePortfolioPanel({ ledger }: { ledger: ExperimentLedgerView }) {
  const summaries = ledger.routeSummaries.length > 0 ? ledger.routeSummaries : fallbackRouteSummaries(ledger);
  const ordered = [...summaries].sort((left, right) => {
    if (left.bestScore === null) return 1;
    if (right.bestScore === null) return -1;
    return ledger.direction === 'minimize' ? left.bestScore - right.bestScore : right.bestScore - left.bestScore;
  });
  return (
    <div className="min-h-full bg-white">
      <div className="grid grid-cols-1 border-b border-slate-200 md:grid-cols-2 xl:grid-cols-4">
        {ordered.map((summary) => {
          const winningRoute = summary.topCandidates.some((candidate) => candidate.candidate.id === ledger.bestCandidate.id);
          return (
            <section key={summary.strategy} className={`min-w-0 border-b border-r border-slate-200 px-4 py-4 md:[&:nth-last-child(-n+2)]:border-b-0 xl:border-b-0 ${winningRoute ? 'bg-emerald-50/40' : 'bg-white'}`}>
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-1.5"><span className="truncate font-mono text-xs font-semibold text-slate-900">{summary.strategy}</span>{winningRoute && <Trophy className="h-3.5 w-3.5 shrink-0 text-emerald-700" />}</div>
                  <div className="mt-1 text-[9px] text-slate-500">{summary.trialCount} trials · Top-{summary.topCandidates.length}</div>
                </div>
                <div className="shrink-0 text-right"><div className="font-mono text-sm font-semibold text-slate-900">{summary.bestScore === null ? '-' : formatExperimentNumber(summary.bestScore)}</div><div className="text-[9px] text-slate-400">best {ledger.metricKey}</div></div>
              </div>
              <div className="mt-3 grid grid-cols-2 border-y border-slate-200 py-2 text-[9px]">
                <div><div className="font-mono font-semibold text-slate-800">{summary.defaultScore === null ? '-' : formatExperimentNumber(summary.defaultScore)}</div><div className="mt-0.5 text-slate-500">默认配置</div></div>
                <div className="border-l border-slate-200 pl-3"><div className="font-mono font-semibold text-slate-800">{formatExperimentNumber(summary.topKMeanReward)}</div><div className="mt-0.5 text-slate-500">Top-K Reward</div></div>
              </div>
              <ol className="mt-2 divide-y divide-slate-100">
                {summary.topCandidates.map((candidate, index) => {
                  const trial = ledger.trials.find((item) => item.number === candidate.trialNumber);
                  return (
                    <li key={candidate.candidate.id} className="py-2">
                      <div className="flex items-center justify-between gap-2"><span className="inline-flex min-w-0 items-center gap-1.5"><span className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-sm text-[9px] font-semibold ${index === 0 ? 'bg-emerald-100 text-emerald-800' : 'bg-slate-100 text-slate-600'}`}>{index + 1}</span><span className="truncate font-mono text-[9px] text-slate-600">{candidate.candidate.changedParameter || 'default'}</span></span><strong className="shrink-0 font-mono text-[10px] text-slate-900">{formatExperimentNumber(candidate.score)}</strong></div>
                      <div className="mt-1 flex items-center justify-between gap-2 text-[8px] text-slate-400"><span className="truncate font-mono">{trial?.policyDecision?.frontierKind === 'exploration' ? '探索通道' : trial?.policyDecision?.beamRank ? `Beam #${trial.policyDecision.beamRank}` : 'Model default'}</span><span className="font-mono">R {formatExperimentNumber(candidate.reward)}</span></div>
                    </li>
                  );
                })}
              </ol>
            </section>
          );
        })}
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-200 bg-slate-50 px-4 py-3 text-[10px] text-slate-600">
        <span>{ordered.length} routes · {ordered.reduce((total, route) => total + route.topCandidates.length, 0)} ranked candidates · Validation</span>
        <span className="font-mono font-semibold text-slate-800">global best · {ledger.bestCandidate.strategy} · {formatExperimentNumber(ledger.bestScore)}</span>
      </div>
    </div>
  );
}

function StrategyTreePanel({ ledger }: { ledger: ExperimentLedgerView }) {
  const bestTrial = ledger.trials.find((trial) => trial.candidate.id === ledger.bestCandidate.id) ?? ledger.trials[0];
  const [selectedCandidateID, setSelectedCandidateID] = useState(bestTrial?.candidate.id ?? '');
  const selectedTrial = ledger.trials.find((trial) => trial.candidate.id === selectedCandidateID) ?? bestTrial;
  const fallbackStrategies = useMemo(() => {
    const seen = new Set<string>();
    return ledger.trials.flatMap((trial) => {
      if (seen.has(trial.candidate.strategy)) return [];
      seen.add(trial.candidate.strategy);
      return [{ name: trial.candidate.strategy, description: '', parameters: [] }];
    });
  }, [ledger.trials]);
  const strategies = ledger.strategySpace.length > 0 ? ledger.strategySpace : fallbackStrategies;

  if (!selectedTrial) return null;
  return (
    <div className="grid min-h-full xl:grid-cols-[minmax(0,1fr)_17rem]">
      <div className="min-w-0 p-4">
        <div className="mx-auto flex max-w-xl items-center justify-center gap-3 rounded-md border border-cyan-200 bg-cyan-50 px-4 py-2.5 text-center">
          <Target className="h-4 w-4 shrink-0 text-cyan-700" />
          <div className="min-w-0 text-xs font-semibold text-slate-800">
            优化 {ledger.metricKey}
            <span className="ml-2 font-mono text-cyan-800">{ledger.direction}{ledger.targetScore === null ? '' : ` · target ${formatExperimentNumber(ledger.targetScore)}`}</span>
          </div>
        </div>
        <div className="mx-auto h-5 w-px bg-slate-300" aria-hidden="true" />
        <div className="relative">
          <div className="absolute left-[12.5%] right-[12.5%] top-0 hidden h-px bg-slate-300 md:block" aria-hidden="true" />
          <div className="grid grid-cols-1 gap-3 pt-5 md:grid-cols-2 lg:grid-cols-4">
            {strategies.map((strategy) => {
              const rootTrial = ledger.trials.find((trial) => trial.candidate.strategy === strategy.name && trial.candidate.depth === 0);
              const childTrials = ledger.trials.filter((trial) => trial.candidate.strategy === strategy.name && trial.candidate.depth > 0);
              return (
                <StrategyBranch
                  key={strategy.name}
                  strategy={strategy}
                  rootTrial={rootTrial}
                  childTrials={childTrials}
                  bestCandidate={ledger.bestCandidate}
                  stopReason={ledger.stopReason}
                  metricKey={ledger.metricKey}
                  direction={ledger.direction}
                  selectedCandidateID={selectedCandidateID}
                  onSelect={setSelectedCandidateID}
                />
              );
            })}
          </div>
        </div>
      </div>
      <CandidateDetail trial={selectedTrial} metricKey={ledger.metricKey} />
    </div>
  );
}

function TrialRow({ trial, metricKey }: { trial: ExperimentTrialView; metricKey: string }) {
  const baseline = trial.number === 0;
  const kept = trial.status === 'kept' || baseline;
  const StatusIcon = kept ? CheckCircle2 : XCircle;
  return (
    <li className="relative pl-9">
      <span className="absolute left-3 top-7 h-[calc(100%+0.5rem)] w-px bg-slate-200 last:hidden" aria-hidden="true" />
      <span className={`absolute left-0 top-2 flex h-6 w-6 items-center justify-center rounded border font-mono text-[9px] ${kept ? 'border-emerald-200 bg-emerald-50 text-emerald-700' : 'border-rose-200 bg-rose-50 text-rose-700'}`}>{trial.number}</span>
      <div className={`rounded-md border px-3 py-3 ${trialTone(trial)}`}>
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 flex-1 items-start gap-2">
            <StatusIcon className={`mt-0.5 h-4 w-4 shrink-0 ${kept ? 'text-emerald-700' : 'text-rose-600'}`} />
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2"><span className="font-mono text-xs font-semibold text-slate-900">{trial.candidate.strategy}</span>{trial.candidate.changedParameter && <span className="rounded border border-cyan-200 bg-cyan-50 px-1.5 py-0.5 text-[9px] font-semibold text-cyan-800">{trial.candidate.changedParameter}</span>}</div>
              <div className="mt-1 break-words font-mono text-[9px] leading-4 text-slate-500">{parameterText(trial.candidate.parameters) || 'default'}</div>
              <div className="mt-1 text-[10px] leading-4 text-slate-600">{trial.reason || trial.candidate.reason}</div>
            </div>
          </div>
          <div className="shrink-0 text-right"><div className="font-mono text-sm font-semibold text-slate-900">{trial.score === null ? 'N/A' : formatExperimentNumber(trial.score)}</div><div className="text-[9px] text-slate-500">{metricKey}</div></div>
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-x-3 border-t border-slate-200/70 pt-2 text-[9px] text-slate-500"><span className="font-semibold text-slate-700">{statusLabel(trial)}</span><span className="inline-flex items-center gap-1"><Clock3 className="h-3 w-3" />{formatExperimentDuration(trial.durationMs)}</span>{trial.number > 0 && <span className="font-mono text-cyan-700">{trial.agentId} · D{trial.dispatchOrder}/C{trial.completionOrder}</span>}{trial.policyDecision && <span className="font-mono">{trial.policyDecision.frontierKind === 'exploration' ? 'explore' : trial.policyDecision.beamRank ? `beam-${trial.policyDecision.beamRank}` : trial.policyDecision.phase}</span>}{trial.reward !== null && <span className="font-mono">reward {formatExperimentNumber(trial.reward)}</span>}{trial.candidate.parentId && <span className="font-mono">depth {trial.candidate.depth}</span>}</div>
      </div>
    </li>
  );
}

function ValidationPanel({ validation, expanded }: { validation: ExperimentValidationView; expanded: boolean }) {
  const passed = validation.status === 'validated';
  const Icon = passed ? ShieldCheck : ShieldX;
  const evidence = validation.runs[0]?.evidence ?? [];
  return (
    <div className={`flex min-h-0 flex-col bg-white ${expanded ? 'h-full' : ''}`}>
      <div className={`border-b px-4 py-4 ${passed ? 'border-emerald-200 bg-emerald-50 text-emerald-900' : 'border-rose-200 bg-rose-50 text-rose-900'}`}>
        <div className="flex items-start gap-3"><Icon className="mt-0.5 h-5 w-5 shrink-0" /><div><div className="text-sm font-semibold">{passed ? 'Holdout 验收通过' : 'Holdout 验收未通过'}</div><div className="mt-1 text-xs opacity-80">{validation.domain} · {validation.adapter} · 完整性 {validation.protectedIntact ? '通过' : '失败'}</div></div></div>
      </div>
      <div className="grid grid-cols-2 border-b border-slate-200 sm:grid-cols-4"><Summary label="搜索基线" value={validation.searchBaseline} /><Summary label="搜索最佳" value={validation.searchBest} accent /><Summary label="Holdout 目标" value={validation.holdoutTarget} /><Summary label="重复通过" text={`${validation.passedRuns}/${validation.requestedRuns}`} accent={passed} /></div>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
        <div className="mb-3 text-sm font-semibold text-slate-900">独立复验</div>
        <ol className="divide-y divide-slate-100 border-y border-slate-200">
          {validation.runs.map((run) => <li key={run.number} className="flex items-center justify-between gap-4 py-3 text-xs"><div className="flex min-w-0 items-center gap-2">{run.status === 'passed' ? <CheckCircle2 className="h-4 w-4 shrink-0 text-emerald-700" /> : <XCircle className="h-4 w-4 shrink-0 text-rose-700" />}<span className="font-semibold text-slate-800">Run {run.number}</span>{run.error && <span className="truncate text-rose-700" title={run.error}>{run.error}</span>}</div><div className="shrink-0 font-mono text-slate-600">{formatExperimentNumber(run.baselineScore)} → <strong className="text-slate-900">{formatExperimentNumber(run.candidateScore)}</strong></div></li>)}
        </ol>
        {evidence.length > 0 && <div className="mt-5"><div className="mb-2 text-sm font-semibold text-slate-900">样例证据</div><div className="overflow-x-auto border border-slate-200"><table className="w-full min-w-[34rem] text-left text-[11px]"><thead className="bg-slate-50 text-slate-500"><tr><th className="px-3 py-2">Case</th><th className="px-3 py-2">Expected</th><th className="px-3 py-2">Observed</th><th className="px-3 py-2">Metrics</th></tr></thead><tbody className="divide-y divide-slate-100">{evidence.slice(0, 8).map((item) => <tr key={item.caseId}><td className="px-3 py-2 font-mono font-semibold">{item.caseId}</td><td className="px-3 py-2 font-mono">{item.expected.join(', ')}</td><td className="max-w-xs truncate px-3 py-2 font-mono" title={item.observed.join(', ')}>{item.observed.join(', ')}</td><td className="px-3 py-2 font-mono">{Object.entries(item.metrics).map(([key, value]) => `${key}=${formatExperimentNumber(value)}`).join(' · ')}</td></tr>)}</tbody></table></div></div>}
        {validation.summary && <div className="mt-4 text-[11px] leading-5 text-slate-600">{validation.summary}</div>}
      </div>
    </div>
  );
}

function ExperimentRunPanel({ ledger, expanded }: { ledger: ExperimentLedgerView; expanded: boolean }) {
  const [viewMode, setViewMode] = useState<ResearchViewMode>('portfolio');
  return (
    <div className={`flex min-h-0 flex-col bg-white ${expanded ? 'h-full' : ''}`}>
      <div className="border-b border-slate-200 bg-slate-50 px-4 py-3">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0"><div className="flex items-center gap-2 text-sm font-semibold text-slate-900"><FlaskConical className="h-4 w-4 text-cyan-700" />{ledger.domain} 自动研究</div><div className="mt-1 text-[10px] text-slate-500">{ledger.adapter} · {stopLabel(ledger.stopReason)}</div></div>
          <div className="flex flex-wrap items-center justify-end gap-2 text-[10px] font-semibold"><span className="border border-violet-200 bg-violet-50 px-2 py-1 text-violet-700">Beam {ledger.beamWidth} + Explore {ledger.explorationSlots}</span><span className="border border-cyan-200 bg-cyan-50 px-2 py-1 text-cyan-800">Agent {ledger.resourceUsage.peakParallelism}/{ledger.resourceUsage.workerSlots}</span><span className="flex items-center gap-1 text-slate-600"><Target className="h-4 w-4 text-cyan-700" />{ledger.targetScore === null ? '无目标阈值' : `目标 ${formatExperimentNumber(ledger.targetScore)}`}</span></div>
        </div>
      </div>
      <div className="grid grid-cols-2 border-b border-slate-200 sm:grid-cols-4"><Summary label="Baseline" value={ledger.baselineScore} /><Summary label="Best" value={ledger.bestScore} accent /><Summary label="Keep" text={String(ledger.acceptedTrials)} /><Summary label="Trials" text={`${ledger.completedTrials}/${ledger.maxTrials}`} /></div>
      <SearchProcessRail ledger={ledger} />
      <div className="flex items-center justify-between gap-3 border-b border-slate-200 bg-white px-4 py-2">
        <div className="min-w-0"><div className="text-xs font-semibold text-slate-900">候选搜索全景</div><div className="mt-0.5 truncate text-[9px] text-slate-500">Model 默认穷举 → UCB 路线预算 → Beam + UCT → 异步 Agent → Holdout</div></div>
        <div className="flex shrink-0 rounded border border-slate-200 bg-slate-50 p-0.5" role="group" aria-label="实验视图模式">
          <button type="button" onClick={() => setViewMode('portfolio')} className={`flex h-7 items-center gap-1.5 rounded px-2 text-[10px] font-semibold ${viewMode === 'portfolio' ? 'bg-white text-cyan-800 shadow-sm' : 'text-slate-500 hover:text-slate-800'}`}><BarChart3 className="h-3.5 w-3.5" />全局榜单</button>
          <button type="button" onClick={() => setViewMode('tree')} className={`flex h-7 items-center gap-1.5 rounded px-2 text-[10px] font-semibold ${viewMode === 'tree' ? 'bg-white text-cyan-800 shadow-sm' : 'text-slate-500 hover:text-slate-800'}`}><ListTree className="h-3.5 w-3.5" />策略树</button>
          <button type="button" onClick={() => setViewMode('timeline')} className={`flex h-7 items-center gap-1.5 rounded px-2 text-[10px] font-semibold ${viewMode === 'timeline' ? 'bg-white text-cyan-800 shadow-sm' : 'text-slate-500 hover:text-slate-800'}`}><ListOrdered className="h-3.5 w-3.5" />时间线</button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {viewMode === 'portfolio' ? <RoutePortfolioPanel ledger={ledger} /> : viewMode === 'tree' ? <StrategyTreePanel ledger={ledger} /> : <div className="mx-auto max-w-4xl px-4 py-4"><ol className="space-y-2">{ledger.trials.map((trial) => <TrialRow key={trial.candidate.id} trial={trial} metricKey={ledger.metricKey} />)}</ol><div className="mt-4 flex flex-wrap gap-x-5 gap-y-2 border-t border-slate-200 pt-3 text-[10px] text-slate-500"><span>Evaluator {ledger.resourceUsage.evaluatorRuns} runs</span><span>累计执行 {formatExperimentDuration(ledger.resourceUsage.evaluatorTimeMs)}</span><span>墙钟 {formatExperimentDuration(ledger.resourceUsage.wallDurationMs)}</span><span>峰值并发 {ledger.resourceUsage.peakParallelism}</span><span className="font-mono">{ledger.evaluationIsolation}</span></div></div>}
      </div>
    </div>
  );
}

export function ExperimentResearchView({ raw, expanded = false }: ExperimentResearchViewProps) {
  const validation = useMemo(() => parseExperimentValidation(raw), [raw]);
  const parsed = useMemo(() => parseExperimentLedger(raw), [raw]);
  if (validation.ok) return <ValidationPanel validation={validation.validation} expanded={expanded} />;
  if (!parsed.ok) return <div className="flex h-full items-center justify-center p-6 text-center text-sm text-rose-700">{parsed.error}</div>;
  return <ExperimentRunPanel ledger={parsed.ledger} expanded={expanded} />;
}
