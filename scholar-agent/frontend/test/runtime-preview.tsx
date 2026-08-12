import { StrictMode, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { useEdgesState, useNodesState } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import '../src/index.css';

import lightRagResult from '../../examples/autoresearch/real_repositories/results/2026-08-10_lightrag_target_stop_e2e.json';
import progressiveAblationPlan from './fixtures/progressive-ablation-plan.json';
import scientificExperimentLedger from './fixtures/scientific-autoresearch-ledger.json';
import scientificExperimentValidation from './fixtures/scientific-autoresearch-validation.json';
import type { ExecutionDisplayMode } from '../src/app/hooks/useScholarRuntime';
import { LeftWorkspaceChat } from '../src/app/components/LeftWorkspace';
import type { IntentContext, PlanGraph, Task } from '../src/contracts/api';
import { ExecutionSidebar } from '../src/features/execution/ExecutionSidebar';
import { GraphPanel } from '../src/features/plan-graph/GraphPanel';
import { buildGraphLayout, graphTaskToTask } from '../src/features/plan-graph/buildGraphLayout';

type PreviewMode = 'dashboard' | 'run' | 'validation' | 'ablation' | 'experiment' | 'experiment-validation';

const rawPlanGraph = lightRagResult.plan_graph as unknown as PlanGraph;
const planGraph: PlanGraph = {
  ...rawPlanGraph,
  nodes: rawPlanGraph.nodes.map((node) => ({
    ...node,
    dependencies: node.dependencies ?? [],
    required_artifacts: node.required_artifacts ?? [],
    output_artifacts: node.output_artifacts ?? [],
  })),
};

const findTask = (type: string): Task => {
  const graphTask = planGraph.nodes.find((node) => node.type === type);
  if (!graphTask) throw new Error(`Missing ${type} task in LightRAG result fixture`);
  return graphTaskToTask(graphTask);
};

const runTask = findTask('autoresearch_run');
const validationTask = findTask('autoresearch_validate');
const ablationTask: Task = {
  ID: 'ablation-design-preview',
  Name: '设计受限 RAG 消融实验',
  Type: 'ablation_design',
  Description: '在 45 分钟预算内选择三组高价值消融。',
  AssignedTo: 'data_agent',
  Status: 'completed',
  Dependencies: [],
  Inputs: {
    ablation_max_experiments: 3,
    ablation_max_gpu_minutes: 20,
    ablation_max_wall_minutes: 45,
  },
  Result: JSON.stringify(progressiveAblationPlan),
  StructuredData: JSON.stringify(progressiveAblationPlan),
};
const experimentTask: Task = {
  ID: 'scientific-autoresearch-preview',
  Name: '运行方法与超参数自动研究',
  Type: 'experiment_run',
  Description: '在冻结的工业检索数据上按 NDCG@1 搜索方法与参数。',
  AssignedTo: 'research_coding_agent',
  Status: 'completed',
  Dependencies: [],
  Inputs: { experiment_max_trials: 6, experiment_target_score: 0.6 },
  Result: JSON.stringify(scientificExperimentLedger),
  StructuredData: JSON.stringify(scientificExperimentLedger),
};
const experimentValidationTask: Task = {
  ID: 'scientific-autoresearch-validation-preview',
  Name: '验证最佳实验候选',
  Type: 'experiment_validate',
  Description: '在搜索过程未使用的 Holdout 上启动两个新进程复验。',
  AssignedTo: 'research_coding_agent',
  Status: 'completed',
  Dependencies: [experimentTask.ID],
  Inputs: { experiment_validation_runs: 2 },
  Result: JSON.stringify(scientificExperimentValidation),
  StructuredData: JSON.stringify(scientificExperimentValidation),
};
const previewMode = (new URLSearchParams(window.location.search).get('view') || 'dashboard') as PreviewMode;
const noOp = () => undefined;

const intentContext: IntentContext = {
  raw_intent: planGraph.user_intent,
  intent_type: planGraph.intent_type,
  entities: {
    repository_url: 'https://github.com/HKUDS/LightRAG',
    repository_revision: '24ee484864357865b20770e478b177ae68391796',
  },
  constraints: {
    max_trials: 8,
    max_wall_seconds: 300,
    validation_runs: 3,
  },
  metadata: {
    source_result: '2026-08-10_lightrag_target_stop_e2e.json',
  },
};

const chatHistory = [
  {
    role: 'user',
    text: '在 LightRAG 上做受限 AutoResearch，最多 8 轮，并用模型不可见的隐藏测试复验 3 次。',
  },
  {
    role: 'system',
    text: '已冻结代码版本、可编辑文件、公开与隐藏评测器，生成 8 节点执行计划。',
  },
  {
    role: 'system',
    text: '完成：公开指标 **0.50 → 1.00**；隐藏验证 **3/3 通过**。',
  },
];

const taskState = (task: Task) => ({
  result: task.Result || '',
  structuredData: task.StructuredData || task.Result || '',
  code: task.Code || '',
  image: task.ImageBase64 || '',
});

export function RuntimePreview() {
  const initialLayout = useMemo(() => buildGraphLayout(planGraph), []);
  const [nodes, , onNodesChange] = useNodesState(initialLayout.nodes);
  const [edges, , onEdgesChange] = useEdgesState(initialLayout.edges);
  const [displayMode, setDisplayMode] = useState<ExecutionDisplayMode>(
    previewMode === 'validation' || previewMode === 'experiment' || previewMode === 'experiment-validation'
      ? 'trials-expanded'
      : previewMode === 'ablation' ? 'ablation-expanded' : 'trials',
  );
  const logsEndRef = useRef<HTMLDivElement>(null);

  if (previewMode === 'ablation') {
    const state = taskState(ablationTask);
    return (
      <main className="flex h-screen overflow-hidden bg-slate-100">
        <ExecutionSidebar
          selectedTask={ablationTask}
          width="100%"
          isExecuting={false}
          displayMode={displayMode}
          executionLogs="[ToT] expanded 5 roots and 3 child branches"
          executionResult={state.result}
          executionCode={state.code}
          executionStructuredData={state.structuredData}
          executionImage={state.image}
          logsEndRef={logsEndRef}
          onClose={noOp}
          onExecute={noOp}
          onChangeDisplayMode={setDisplayMode}
        />
      </main>
    );
  }

  if (previewMode === 'validation') {
    const state = taskState(validationTask);
    return (
      <main className="flex h-screen overflow-hidden bg-slate-100">
        <ExecutionSidebar
          selectedTask={validationTask}
          width="100%"
          isExecuting={false}
          displayMode={displayMode}
          executionLogs="[Validation] hidden holdout completed: 3/3 runs passed"
          executionResult={state.result}
          executionCode={state.code}
          executionStructuredData={state.structuredData}
          executionImage={state.image}
          logsEndRef={logsEndRef}
          onClose={noOp}
          onExecute={noOp}
          onChangeDisplayMode={setDisplayMode}
        />
      </main>
    );
  }

  if (previewMode === 'experiment' || previewMode === 'experiment-validation') {
    const task = previewMode === 'experiment' ? experimentTask : experimentValidationTask;
    const state = taskState(task);
    return (
      <main className="flex h-screen overflow-hidden bg-slate-100">
        <ExecutionSidebar
          selectedTask={task}
          width="100%"
          isExecuting={false}
          displayMode={displayMode}
          executionLogs={previewMode === 'experiment'
            ? '[Experiment] baseline=0.4000\n[Trial 3] keep graph_hybrid=0.6000\n[Experiment] target_score_reached'
            : '[Validation] holdout baseline=0.3333 candidate=0.6667 passed=2/2'}
          executionResult={state.result}
          executionCode={state.code}
          executionStructuredData={state.structuredData}
          executionImage={state.image}
          logsEndRef={logsEndRef}
          onClose={noOp}
          onExecute={noOp}
          onChangeDisplayMode={setDisplayMode}
        />
      </main>
    );
  }

  const showRunPanel = previewMode === 'run';
  const state = taskState(runTask);

  return (
    <main className="flex h-screen overflow-hidden bg-slate-100 font-sans">
      <LeftWorkspaceChat
        widthPercent={showRunPanel ? 26 : 30}
        state={{
          chatHistory,
          loading: false,
          prompt: '',
          showSuggestions: false,
          pendingAttachments: [],
          uploadingAttachments: false,
          attachmentError: '',
          isLoggedIn: true,
          userId: 'autoresearch-demo',
          loginInput: 'autoresearch-demo',
          activeSessionId: 'lightrag-target-stop',
          sessions: [
            {
              id: 'lightrag-target-stop',
              title: 'LightRAG 隐藏验证实验',
              createdAt: '2026-08-10T10:56:14Z',
              updatedAt: '2026-08-10T10:57:23Z',
              messageCount: chatHistory.length,
            },
          ],
        }}
        chatActions={{
          setPrompt: noOp,
          setShowSuggestions: noOp,
          onSendMessage: noOp,
          setLoginInput: noOp,
          onLogin: noOp,
          onCreateSession: noOp,
          onSwitchSession: noOp,
          onAttachFiles: noOp,
          onRemoveAttachment: noOp,
        }}
        pdfActions={{ onOpenPdf: noOp, onClosePdf: noOp, onFullTranslation: noOp }}
        taskActions={{ onOpenTaskView: noOp }}
      />

      <div className="w-1.5 shrink-0 bg-slate-200" />

      <section className="flex min-w-0 flex-1 overflow-hidden">
        <GraphPanel
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={noOp}
          intentContext={intentContext}
          runAllText="运行计划"
          graphTitle="LightRAG AutoResearch 执行流程"
          graphHint={planGraph.user_intent}
          isExecuting={false}
          requiresApproval={false}
          onRunAll={noOp}
          onApproveAndRun={noOp}
          onCancel={noOp}
          onRetryFailed={noOp}
        />

        {showRunPanel && (
          <ExecutionSidebar
            selectedTask={runTask}
            width="min(500px, 42vw)"
            isExecuting={false}
            displayMode={displayMode}
            executionLogs={'[AutoResearch] baseline metrics.aggregation_score=0.5\n[Trial 1] keep: score=1.0\n[AutoResearch] target_score_reached'}
            executionResult={state.result}
            executionCode={state.code}
            executionStructuredData={state.structuredData}
            executionImage={state.image}
            logsEndRef={logsEndRef}
            onClose={noOp}
            onExecute={noOp}
            onChangeDisplayMode={setDisplayMode}
          />
        )}
      </section>
    </main>
  );
}

type PreviewRootElement = HTMLElement & {
  __runtimePreviewRoot?: ReturnType<typeof createRoot>;
};

const rootElement = document.getElementById('root') as PreviewRootElement;
const previewRoot = rootElement.__runtimePreviewRoot ?? createRoot(rootElement);
rootElement.__runtimePreviewRoot = previewRoot;

previewRoot.render(
  <StrictMode>
    <RuntimePreview />
  </StrictMode>,
);
