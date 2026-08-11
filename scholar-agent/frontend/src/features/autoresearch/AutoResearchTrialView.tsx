import { useMemo } from 'react';
import {
  AlertTriangle,
  ChartNoAxesCombined,
  CheckCircle2,
  Clock3,
  FileCode2,
  FlaskConical,
  Gauge,
  Minus,
  RotateCcw,
  ShieldAlert,
  ShieldCheck,
  TerminalSquare,
  TrendingDown,
  TrendingUp,
  XCircle,
} from 'lucide-react';
import {
  autoResearchGain,
  formatResearchDuration,
  formatResearchMetric,
  parseAutoResearchLedger,
  parseAutoResearchValidationReport,
  type AutoResearchLedgerView,
  type AutoResearchTrialView as TrialView,
  type AutoResearchValidationView,
} from './trialLedger';

interface AutoResearchTrialViewProps {
  rawLedger: string;
  expanded?: boolean;
}

const trialStatus = (trial: TrialView) => {
  if (trial.number === 0) {
    return { label: 'Baseline', className: 'border-blue-200 bg-blue-50 text-blue-800', icon: <FlaskConical className="h-3.5 w-3.5" /> };
  }
  if (trial.status === 'kept') {
    return { label: 'Keep', className: 'border-emerald-200 bg-emerald-50 text-emerald-800', icon: <CheckCircle2 className="h-3.5 w-3.5" /> };
  }
  if (trial.status === 'rejected') {
    return { label: 'Reject', className: 'border-rose-200 bg-rose-50 text-rose-800', icon: <XCircle className="h-3.5 w-3.5" /> };
  }
  return { label: trial.status || 'Unknown', className: 'border-amber-200 bg-amber-50 text-amber-900', icon: <AlertTriangle className="h-3.5 w-3.5" /> };
};

const pointColor = (trial: TrialView) => {
  if (trial.number === 0) return '#2563eb';
  if (trial.status === 'kept') return '#059669';
  if (trial.status === 'rejected') return '#e11d48';
  return '#64748b';
};

const stopReasonLabel = (reason: string) => {
  if (reason === 'target_score_reached') return '达到契约目标分数';
  if (reason === 'trial_budget_exhausted') return '达到候选轮次预算';
  if (reason === 'wall_time_budget_exhausted') return '达到运行时间预算';
  if (reason.startsWith('stop:')) return `Agent 主动停止：${reason.slice(5).trim()}`;
  if (reason.startsWith('unsupported:')) return `当前范围不支持：${reason.slice(12).trim()}`;
  if (reason.startsWith('candidate_model_error:')) return '候选模型异常，已保留当前最佳版本';
  return reason || '循环正常结束';
};

function MetricTrend({ ledger }: { ledger: AutoResearchLedgerView }) {
  const measured = ledger.trials.filter((trial) => trial.metric !== null);
  const width = 640;
  const height = 220;
  const padding = { left: 72, right: 20, top: 20, bottom: 36 };
  const plotWidth = width - padding.left - padding.right;
  const plotHeight = height - padding.top - padding.bottom;
  const values = measured.map((trial) => trial.metric as number);
  const rawMin = Math.min(...values, ledger.baselineScore, ledger.bestScore);
  const rawMax = Math.max(...values, ledger.baselineScore, ledger.bestScore);
  const span = Math.max(Math.abs(rawMax - rawMin), Math.max(Math.abs(rawMax), 1) * 0.02);
  const minValue = rawMin - span * 0.12;
  const maxValue = rawMax + span * 0.12;
  const trialDomain = Math.max(1, ledger.maxTrials, ...ledger.trials.map((trial) => trial.number));
  const x = (trial: number) => padding.left + (trial / trialDomain) * plotWidth;
  const y = (metric: number) => padding.top + ((maxValue - metric) / (maxValue - minValue)) * plotHeight;
  const points = measured.map((trial) => `${x(trial.number)},${y(trial.metric as number)}`).join(' ');
  const ticks = [maxValue, (maxValue + minValue) / 2, minValue];

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      className="block h-auto w-full"
      role="img"
      aria-label={`${ledger.metricKey} 的 AutoResearch 试验趋势`}
    >
      {ticks.map((tick) => (
        <g key={tick}>
          <line x1={padding.left} x2={width - padding.right} y1={y(tick)} y2={y(tick)} stroke="#e2e8f0" strokeWidth="1" />
          <text x={padding.left - 10} y={y(tick) + 4} textAnchor="end" fill="#64748b" fontSize="11">
            {formatResearchMetric(tick)}
          </text>
        </g>
      ))}
      <line x1={padding.left} x2={width - padding.right} y1={height - padding.bottom} y2={height - padding.bottom} stroke="#94a3b8" strokeWidth="1" />
      {Array.from({ length: trialDomain + 1 }, (_, trial) => (
        <g key={trial}>
          <line x1={x(trial)} x2={x(trial)} y1={height - padding.bottom} y2={height - padding.bottom + 5} stroke="#94a3b8" />
          <text x={x(trial)} y={height - 12} textAnchor="middle" fill="#64748b" fontSize="11">
            {trial === 0 ? 'B' : trial}
          </text>
        </g>
      ))}
      {measured.length > 1 && <polyline points={points} fill="none" stroke="#334155" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />}
      {measured.map((trial) => (
        <g key={trial.number}>
          <circle cx={x(trial.number)} cy={y(trial.metric as number)} r="6" fill={pointColor(trial)} stroke="#ffffff" strokeWidth="2">
            <title>{`${trial.number === 0 ? 'Baseline' : `Trial ${trial.number}`}: ${formatResearchMetric(trial.metric as number)} (${trial.status})`}</title>
          </circle>
        </g>
      ))}
    </svg>
  );
}

function SummaryValue({ label, value, tone = 'slate' }: { label: string; value: string; tone?: 'slate' | 'emerald' | 'blue' | 'amber' }) {
  const toneClass = {
    slate: 'text-slate-900',
    emerald: 'text-emerald-700',
    blue: 'text-blue-700',
    amber: 'text-amber-700',
  }[tone];
  return (
    <div className="min-w-0 border-r border-slate-200 px-3 py-3 text-center last:border-r-0">
      <div className={`truncate text-sm font-semibold ${toneClass}`} title={value}>{value}</div>
      <div className="mt-1 text-[10px] font-medium text-slate-500">{label}</div>
    </div>
  );
}

function TrialRow({ trial, metricKey }: { trial: TrialView; metricKey: string }) {
  const status = trialStatus(trial);
  return (
    <li className="rounded-md border border-slate-200 bg-white px-4 py-3 shadow-sm">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className={`inline-flex items-center gap-1 rounded-md border px-2 py-1 text-[11px] font-semibold ${status.className}`}>
              {status.icon}
              {status.label}
            </span>
            <span className="text-xs font-semibold text-slate-800">{trial.number === 0 ? '冻结基线' : `候选 ${trial.number}`}</span>
            {trial.durationMs > 0 && (
              <span className="inline-flex items-center gap-1 text-[11px] text-slate-500">
                <Clock3 className="h-3.5 w-3.5" />
                {formatResearchDuration(trial.durationMs)}
              </span>
            )}
          </div>
          {trial.diagnosis && (
            <div className="mt-2 border-l-2 border-cyan-200 pl-2 text-[11px] leading-5 text-slate-600">
              <span className="mr-1 font-semibold text-cyan-800">诊断</span>
              <span className="break-words">{trial.diagnosis}</span>
            </div>
          )}
          {trial.hypothesis && <div className="mt-2 break-words text-xs font-medium leading-5 text-slate-800">{trial.hypothesis}</div>}
          {trial.reason && <div className="mt-1 break-words text-[11px] leading-5 text-slate-600">{trial.reason}</div>}
        </div>
        <div className="shrink-0 text-right">
          <div className="font-mono text-sm font-semibold text-slate-900">
            {trial.metric === null ? 'N/A' : formatResearchMetric(trial.metric)}
          </div>
          <div className="mt-1 text-[10px] text-slate-500">{metricKey}</div>
          {trial.metricSamples.length > 1 && (
            <div className="mt-1 font-mono text-[10px] text-slate-500">
              n={trial.metricSamples.length} · {trial.metricAggregation} · σ {formatResearchMetric(trial.metricStdDev)}
            </div>
          )}
          {trial.deltaFromBest !== null && (
            <div className={`mt-1 font-mono text-[11px] ${trial.deltaFromBest > 0 ? 'text-emerald-700' : trial.deltaFromBest < 0 ? 'text-rose-700' : 'text-slate-500'}`}>
              {trial.deltaFromBest > 0 ? '+' : ''}{formatResearchMetric(trial.deltaFromBest)}
            </div>
          )}
        </div>
      </div>
      {trial.patches.length > 0 && (
        <div className="mt-3 border-t border-slate-100 pt-3">
          {trial.patches.map((patch) => (
            <div key={`${trial.number}-${patch.path}`} className="mb-2 flex items-start gap-2 last:mb-0">
              <FileCode2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-cyan-700" />
              <div className="min-w-0 flex-1">
                <div className="break-all font-mono text-[11px] font-semibold text-slate-800">{patch.path}</div>
                {patch.reason && <div className="mt-0.5 break-words text-[11px] leading-4 text-slate-600">{patch.reason}</div>}
                {(patch.beforeSha256 || patch.afterSha256) && (
                  <div className="mt-1 break-all font-mono text-[10px] text-slate-400">
                    {(patch.beforeSha256 || 'unknown').slice(0, 8)} → {(patch.afterSha256 || 'unknown').slice(0, 8)}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </li>
  );
}

function AutoResearchValidationPanel({ validation, expanded }: { validation: AutoResearchValidationView; expanded: boolean }) {
  const hidden = validation.validationMode === 'hidden_holdout';
  const passed = validation.status === 'validated';
  const BannerIcon = hidden ? (passed ? ShieldCheck : ShieldAlert) : RotateCcw;
  const bannerTone = hidden
    ? passed ? 'border-emerald-200 bg-emerald-50 text-emerald-900' : 'border-rose-200 bg-rose-50 text-rose-900'
    : 'border-amber-200 bg-amber-50 text-amber-900';
  const title = hidden
    ? passed ? '隐藏验收通过' : '隐藏验收未通过'
    : '仅完成公开评测重放';
  const integrity = [
    ['保护文件', validation.protectedIntact],
    ['仓库源码', validation.workspaceIntact],
    ['候选哈希', validation.candidateIntact],
  ] as const;

  return (
    <div className={`flex min-h-0 flex-col bg-white ${expanded ? 'h-full' : ''}`}>
      <div className={`border-b px-4 py-4 ${bannerTone}`}>
        <div className="flex items-start gap-3">
          <BannerIcon className="mt-0.5 h-5 w-5 shrink-0" />
          <div className="min-w-0">
            <div className="text-sm font-semibold">{title}</div>
            <div className="mt-1 break-words text-xs leading-5 opacity-80">
              {hidden ? '候选模型未看到该验收命令、源码或基线。' : '该结果复用了搜索阶段的评测器，不能视为独立或隐藏验收。'}
            </div>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 border-b border-slate-200 bg-slate-50 sm:grid-cols-4">
        <SummaryValue label="搜索最佳" value={formatResearchMetric(validation.searchBestScore)} tone="blue" />
        <SummaryValue label={hidden ? '隐藏基线' : '搜索分数'} value={formatResearchMetric(validation.holdoutBaselineScore ?? validation.searchBestScore)} />
        <SummaryValue label={hidden ? '验收阈值' : '重放期望'} value={formatResearchMetric(validation.expectedScore)} tone="amber" />
        <SummaryValue label="验收均值" value={formatResearchMetric(validation.meanScore)} tone={passed ? 'emerald' : 'amber'} />
      </div>

      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-slate-200 px-4 py-3 text-[11px] text-slate-600">
        <span className="font-semibold text-slate-800">完整性</span>
        {integrity.map(([label, intact]) => (
          <span key={label} className={`inline-flex items-center gap-1 ${intact ? 'text-emerald-700' : 'font-semibold text-rose-700'}`}>
            {intact ? <CheckCircle2 className="h-3.5 w-3.5" /> : <XCircle className="h-3.5 w-3.5" />}
            {label}
          </span>
        ))}
        <span>通过 {validation.passedRuns} / {validation.requestedRuns}</span>
        <span>stddev {formatResearchMetric(validation.stddev)}</span>
        <span>失败率 {formatResearchMetric(validation.failureRate * 100)}%</span>
      </div>

      {validation.resourceUsage && (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-slate-200 px-4 py-2.5 text-[11px] text-slate-600">
          <span className="inline-flex items-center gap-1.5 font-semibold text-slate-800">
            <Gauge className="h-3.5 w-3.5 text-cyan-700" />
            验收资源
          </span>
          <span>{validation.resourceUsage.commandRuns} 次命令</span>
          <span>Guard {validation.resourceUsage.guardRuns} / Eval {validation.resourceUsage.evaluatorRuns}</span>
          <span>墙钟 {formatResearchDuration(validation.resourceUsage.wallDurationMs)}</span>
        </div>
      )}

      <section className="min-h-0 flex-1 px-4 py-4">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <div className="text-sm font-semibold text-slate-900">逐轮验收</div>
          <div className="break-all font-mono text-[11px] text-slate-500">{validation.metricKey}</div>
        </div>
        <ol className={`space-y-2 ${expanded ? '' : 'max-h-[30rem] overflow-y-auto pr-1'}`}>
          {validation.runs.map((run) => (
            <li key={run.number} className="border-b border-slate-100 px-1 py-3 last:border-b-0">
              <div className="flex items-start justify-between gap-4">
                <div className="flex min-w-0 items-start gap-2">
                  {run.scoreMatches ? <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-700" /> : <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-rose-700" />}
                  <div className="min-w-0">
                    <div className="text-xs font-semibold text-slate-800">Run {run.number} · {run.scoreMatches ? 'Pass' : 'Fail'}</div>
                    {run.error && <div className="mt-1 break-words text-[11px] leading-5 text-rose-700">{run.error}</div>}
                  </div>
                </div>
                <div className="shrink-0 text-right">
                  <div className="font-mono text-xs font-semibold text-slate-900">{run.observedScore === null ? 'N/A' : formatResearchMetric(run.observedScore)}</div>
                  {run.deltaFromBaseline !== null && <div className="mt-1 font-mono text-[10px] text-slate-500">delta {run.deltaFromBaseline >= 0 ? '+' : ''}{formatResearchMetric(run.deltaFromBaseline)}</div>}
                  {run.durationMs > 0 && <div className="mt-1 text-[10px] text-slate-400">{formatResearchDuration(run.durationMs)}</div>}
                </div>
              </div>
            </li>
          ))}
        </ol>
        {validation.summary && <div className="mt-3 border-t border-slate-200 pt-3 text-[11px] leading-5 text-slate-600">{validation.summary}</div>}
      </section>
    </div>
  );
}

export function AutoResearchTrialView({ rawLedger, expanded = false }: AutoResearchTrialViewProps) {
  const validation = useMemo(() => parseAutoResearchValidationReport(rawLedger), [rawLedger]);
  const parsed = useMemo(() => parseAutoResearchLedger(rawLedger), [rawLedger]);
  if (validation.ok) {
    return <AutoResearchValidationPanel validation={validation.validation} expanded={expanded} />;
  }
  if (!parsed.ok) {
    return (
      <div className="flex min-h-48 items-center justify-center border border-rose-200 bg-rose-50 px-6 text-center text-sm text-rose-800" role="alert">
        <div>
          <AlertTriangle className="mx-auto mb-2 h-5 w-5" />
          <div className="font-semibold">实验账本无法显示</div>
          <div className="mt-1 text-xs text-rose-700">{parsed.error}</div>
        </div>
      </div>
    );
  }

  const ledger = parsed.ledger;
  const gain = autoResearchGain(ledger);
  const DirectionIcon = ledger.direction === 'minimize' ? TrendingDown : TrendingUp;
  const measuredCount = ledger.trials.filter((trial) => trial.metric !== null).length;

  return (
    <div className={`flex min-h-0 flex-col bg-white ${expanded ? 'h-full' : ''}`}>
      <div className="grid grid-cols-2 border-y border-slate-200 bg-slate-50 sm:grid-cols-4">
        <SummaryValue label="Baseline" value={formatResearchMetric(ledger.baselineScore)} tone="blue" />
        <SummaryValue label="Best" value={formatResearchMetric(ledger.bestScore)} tone="emerald" />
        <SummaryValue label="有效提升" value={`${gain >= 0 ? '+' : ''}${formatResearchMetric(gain)}`} tone={gain > 0 ? 'emerald' : 'amber'} />
        <SummaryValue label="Keep / Completed" value={`${ledger.acceptedTrials} / ${ledger.completedTrials}`} />
      </div>

      {ledger.resourceUsage && (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-slate-200 px-4 py-2.5 text-[11px] text-slate-600">
          <span className="inline-flex items-center gap-1.5 font-semibold text-slate-800">
            <Gauge className="h-3.5 w-3.5 text-cyan-700" />
            执行资源
          </span>
          <span className="inline-flex items-center gap-1.5">
            <TerminalSquare className="h-3.5 w-3.5" />
            {ledger.resourceUsage.commandRuns} 次命令
          </span>
          <span>Guard {ledger.resourceUsage.guardRuns} / Eval {ledger.resourceUsage.evaluatorRuns}</span>
          <span>搜索测量 {ledger.searchRuns}× {ledger.searchAggregation}</span>
          {ledger.targetScore !== null && <span>目标 {formatResearchMetric(ledger.targetScore)}</span>}
          <span>命令累计 {formatResearchDuration(ledger.resourceUsage.commandDurationMs)}</span>
          <span>墙钟 {formatResearchDuration(ledger.resourceUsage.wallDurationMs)}</span>
          {ledger.resourceUsage.failedCommands > 0 && (
            <span className="font-semibold text-rose-700">失败命令 {ledger.resourceUsage.failedCommands}</span>
          )}
        </div>
      )}

      <section className="border-b border-slate-200 px-4 py-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2 text-sm font-semibold text-slate-900">
            <ChartNoAxesCombined className="h-4 w-4 text-cyan-700" />
            <span className="break-all">{ledger.metricKey}</span>
          </div>
          <span className="inline-flex items-center gap-1 rounded-md border border-slate-200 bg-white px-2 py-1 text-[11px] font-medium text-slate-600">
            <DirectionIcon className="h-3.5 w-3.5" />
            {ledger.direction === 'minimize' ? '越低越好' : '越高越好'}
          </span>
        </div>
        <div className="mx-auto mt-3 w-full max-w-4xl">
          {measuredCount > 0 ? <MetricTrend ledger={ledger} /> : (
            <div className="flex h-44 items-center justify-center border border-dashed border-slate-300 text-xs text-slate-500">
              没有可绘制的指标记录
            </div>
          )}
        </div>
        <div className="mt-1 flex flex-wrap items-center justify-center gap-4 text-[10px] text-slate-500">
          <span className="inline-flex items-center gap-1"><span className="h-2.5 w-2.5 rounded-full bg-blue-600" />Baseline</span>
          <span className="inline-flex items-center gap-1"><span className="h-2.5 w-2.5 rounded-full bg-emerald-600" />Keep</span>
          <span className="inline-flex items-center gap-1"><span className="h-2.5 w-2.5 rounded-full bg-rose-600" />Reject</span>
        </div>
      </section>

      <section className="min-h-0 flex-1 px-4 py-4">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2 text-sm font-semibold text-slate-900">
            <FlaskConical className="h-4 w-4 text-blue-700" />
            试验记录
          </div>
          <div className="inline-flex items-center gap-1 text-[11px] text-slate-500">
            {gain === 0 ? <Minus className="h-3.5 w-3.5" /> : <DirectionIcon className="h-3.5 w-3.5" />}
            {stopReasonLabel(ledger.stopReason)}
          </div>
        </div>
        <ol className={`space-y-3 ${expanded ? '' : 'max-h-[34rem] overflow-y-auto pr-1'}`}>
          {ledger.trials.map((trial) => <TrialRow key={trial.number} trial={trial} metricKey={ledger.metricKey} />)}
        </ol>
      </section>
    </div>
  );
}
