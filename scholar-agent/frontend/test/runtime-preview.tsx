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
import type { GraphTask, IntentContext, PlanGraph, Task } from '../src/contracts/api';
import { ExecutionSidebar } from '../src/features/execution/ExecutionSidebar';
import { GraphPanel } from '../src/features/plan-graph/GraphPanel';
import { buildGraphLayout, graphTaskToTask } from '../src/features/plan-graph/buildGraphLayout';

type PreviewMode = 'dashboard' | 'run' | 'validation' | 'ablation' | 'experiment' | 'experiment-validation';

const rawPlanGraph = lightRagResult.plan_graph as unknown as PlanGraph;
const repositoryPlanGraph: PlanGraph = {
  ...rawPlanGraph,
  nodes: rawPlanGraph.nodes.map((node) => ({
    ...node,
    dependencies: node.dependencies ?? [],
    required_artifacts: node.required_artifacts ?? [],
    output_artifacts: node.output_artifacts ?? [],
  })),
};

const findTask = (type: string): Task => {
  const graphTask = repositoryPlanGraph.nodes.find((node) => node.type === type);
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
  Inputs: {
    research_domain: 'retrieval', experiment_max_trials: 6, experiment_max_parallel_trials: 4,
    experiment_target_score: 0.6, ablation_max_experiments: 5,
  },
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

const previewGraphTask = (
  id: string,
  name: string,
  type: string,
  assignedTo: string,
  dependencies: string[],
  task?: Task,
  parallelizable = false,
): GraphTask => ({
  id,
  name,
  type,
  description: task?.Description || name,
  assigned_to: assignedTo,
  status: 'completed',
  dependencies,
  inputs: task?.Inputs || {},
  required_artifacts: [],
  output_artifacts: [],
  parallelizable,
  priority: 0,
  retry_limit: 1,
  run_count: 1,
  execution_epoch: 1,
  contract: { version: 'task.contract/v1', input_artifacts: [], output_artifacts: [], allowed_tools: [] },
  result: task?.Result,
  structured_data: task?.StructuredData,
});

const scientificNodes = [
  previewGraphTask('experiment-data-preview', '适配用户研究数据', 'experiment_dataset_prepare', 'research_coding_agent', []),
  previewGraphTask('experiment-spec-preview', '冻结指标、策略空间与预算', 'experiment_spec', 'research_coding_agent', ['experiment-data-preview']),
  previewGraphTask(ablationTask.ID, 'ToT 展开并筛选实验方向', 'ablation_design', 'data_agent', ['experiment-spec-preview'], ablationTask, true),
  previewGraphTask('experiment-runtime-preview', '准备隔离运行环境', 'prepare_runtime', 'sandbox_agent', ['experiment-spec-preview'], undefined, true),
  previewGraphTask('experiment-install-preview', '安装实验依赖', 'install_dependencies', 'sandbox_agent', ['experiment-runtime-preview']),
  previewGraphTask(experimentTask.ID, '运行多策略异步搜索', 'experiment_run', 'research_coding_agent', ['experiment-install-preview', ablationTask.ID], experimentTask),
  previewGraphTask(experimentValidationTask.ID, '在隐藏 Holdout 上复验最佳候选', 'experiment_validate', 'research_coding_agent', [experimentTask.ID], experimentValidationTask),
  previewGraphTask('experiment-report-preview', '汇总可解释研究证据', 'verify_result', 'data_agent', [experimentValidationTask.ID]),
];

const scientificPlanGraph: PlanGraph = {
  ...repositoryPlanGraph,
  id: 'scientific-autoresearch-parallel-preview',
  user_intent: '上传工业数据，在固定 NDCG@1 与 6 次预算内，完整比较 Model 默认配置，再用 UCB、Beam 和 UCT 异步搜索参数。',
  intent_type: 'AutoResearch',
  status: 'completed',
  nodes: scientificNodes,
  edges: scientificNodes.flatMap((node) => node.dependencies.map((dependency) => ({
    id: `${dependency}-${node.id}`,
    from: dependency,
    to: node.id,
    type: 'control',
  }))),
};

const intentContext: IntentContext = {
  raw_intent: scientificPlanGraph.user_intent,
  intent_type: scientificPlanGraph.intent_type,
  entities: {
    research_domain: 'retrieval',
    experiment_adapter: 'retrieval.v1',
  },
  constraints: {
    max_trials: 6,
    max_parallel_trials: 4,
    max_wall_seconds: 180,
    validation_runs: 2,
  },
  metadata: {
    source_result: 'scientific-autoresearch-ledger.json',
  },
};

const chatHistory = [
  {
    role: 'user',
    text: '上传工业检索数据，在 6 次预算内比较 Model 组合，并用 4 个 Search Agent 搜索参数。',
  },
  {
    role: 'system',
    text: '已冻结数据、指标、策略空间和 Reward；ToT 设计与运行环境正在异步准备。',
  },
  {
    role: 'system',
    text: '完成：搜索指标 **0.40 → 0.64**；已冻结最佳候选等待隐藏 Holdout。',
  },
];

const taskState = (task: Task) => ({
  result: task.Result || '',
  structuredData: task.StructuredData || task.Result || '',
  code: task.Code || '',
  image: task.ImageBase64 || '',
});

export function RuntimePreview() {
  const activePlanGraph = previewMode === 'run' ? repositoryPlanGraph : scientificPlanGraph;
  const initialLayout = useMemo(() => buildGraphLayout(activePlanGraph), [activePlanGraph]);
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
            ? '[Experiment] all model defaults completed\n[UCB] route=hybrid_rrf\n[UCT] beam=1 alpha=0.8\n[Agent 02] keep score=0.6400'
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
          activeSessionId: showRunPanel ? 'lightrag-target-stop' : 'scientific-autoresearch-parallel',
          sessions: [
            {
              id: showRunPanel ? 'lightrag-target-stop' : 'scientific-autoresearch-parallel',
              title: showRunPanel ? 'LightRAG 隐藏验证实验' : '工业数据多策略实验',
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

      <section className="flex h-full min-w-0 flex-1 overflow-hidden">
        <GraphPanel
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={noOp}
          intentContext={intentContext}
          runAllText="运行计划"
          graphTitle={showRunPanel ? 'LightRAG AutoResearch 执行流程' : 'Scientific AutoResearch 执行流程'}
          graphHint={activePlanGraph.user_intent}
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
