import { StrictMode, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import '../src/index.css';
import type { ExecutionDisplayMode } from '../src/app/hooks/useScholarRuntime';
import type { Task } from '../src/contracts/api';
import { ExecutionSidebar } from '../src/features/execution/ExecutionSidebar';

const ledger = JSON.stringify({
  version: 'autoresearch.ledger/v1',
  spec_sha256: 'frozen-spec-fixture',
  status: 'completed',
  metric_key: 'metrics.macro_f1',
  direction: 'maximize',
  baseline_score: 0.672875816993464,
  best_score: 0.78125,
  max_trials: 3,
  completed_trials: 3,
  accepted_trials: 1,
  stop_reason: 'trial_budget_exhausted',
  resource_usage: {
    command_runs: 8,
    guard_runs: 4,
    evaluator_runs: 4,
    successful_commands: 7,
    failed_commands: 1,
    command_duration_ms: 4900,
    wall_duration_ms: 9400,
  },
  trials: [
    {
      number: 0,
      status: 'baseline',
      decision: 'keep',
      hypothesis: 'frozen baseline',
      reason: 'baseline establishes the initial score',
      metric: 0.672875816993464,
      started_at: '2026-08-07T00:00:00Z',
      finished_at: '2026-08-07T00:00:01.2Z',
    },
    {
      number: 1,
      status: 'kept',
      decision: 'keep',
      hypothesis: 'Adding bounded bilingual routing rules improves ambiguous intent classification.',
      reason: 'metrics.macro_f1 improved by 0.108374 (required 0.001)',
      metric: 0.78125,
      delta_from_best: 0.108374183006536,
      started_at: '2026-08-07T00:00:02Z',
      finished_at: '2026-08-07T00:00:05.6Z',
      patches: [
        {
          path: 'candidate.py',
          reason: 'Add focused repository, benchmark and AutoResearch phrase rules.',
          before_sha256: '7d39da592567260604ed6f411fcdbaad',
          after_sha256: 'cd6a70cb5ab382313c74a325560aa738',
        },
      ],
    },
    {
      number: 2,
      status: 'rejected',
      decision: 'reject',
      hypothesis: 'A broad experiment keyword may improve recall.',
      reason: 'metrics.macro_f1 delta -0.042 did not meet 0.001',
      metric: 0.73925,
      delta_from_best: -0.042,
      started_at: '2026-08-07T00:00:06Z',
      finished_at: '2026-08-07T00:00:08.1Z',
      patches: [
        {
          path: 'candidate.py',
          reason: 'Broaden experiment keyword matching.',
          before_sha256: 'cd6a70cb5ab382313c74a325560aa738',
          after_sha256: '4a44dc15364204a80fe80e9039455cc1',
        },
      ],
    },
    {
      number: 3,
      status: 'rejected',
      decision: 'reject',
      hypothesis: 'Compile and evaluator failure should preserve the best candidate.',
      reason: 'candidate execution failed: guard command exited with code 1',
      started_at: '2026-08-07T00:00:09Z',
      finished_at: '2026-08-07T00:00:09.4Z',
      patches: [
        {
          path: 'candidate.py',
          reason: 'Try a compact rule table.',
          before_sha256: 'cd6a70cb5ab382313c74a325560aa738',
          after_sha256: 'f5ca38f748a1d6eaf726b8a42fb575c3',
        },
      ],
    },
  ],
});

const task: Task = {
  ID: 'autoresearch-run-preview',
  Name: '运行受限 AutoResearch 循环',
  Type: 'autoresearch_run',
  Description: 'Run the frozen evaluator and retain only improving candidates.',
  AssignedTo: 'research_coding_agent',
  Status: 'completed',
  Dependencies: [],
  Result: ledger,
  StructuredData: ledger,
  Code: '# retained candidate\n',
};

export function Preview() {
  const [mode, setMode] = useState<ExecutionDisplayMode>('trials');
  const logsEndRef = useRef<HTMLDivElement>(null);
  const expanded = mode.endsWith('-expanded');
  return (
    <main className="relative flex h-screen justify-end overflow-hidden bg-slate-100">
      <ExecutionSidebar
        selectedTask={task}
        width={expanded ? '100%' : 'min(540px, 100%)'}
        isExecuting={false}
        displayMode={mode}
        executionLogs="[AutoResearch] completed 3 candidate trials"
        executionResult={ledger}
        executionCode={task.Code || ''}
        executionStructuredData={ledger}
        executionImage=""
        logsEndRef={logsEndRef}
        onClose={() => undefined}
        onExecute={() => undefined}
        onChangeDisplayMode={setMode}
      />
    </main>
  );
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Preview />
  </StrictMode>,
);
