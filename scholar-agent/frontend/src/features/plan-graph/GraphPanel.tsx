import { Background, Controls, ReactFlow } from '@xyflow/react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { CheckCircle2, GitBranch, LoaderCircle, Play, RotateCcw, ShieldCheck, Square } from 'lucide-react';
import type { IntentContext } from '../../contracts/api';

interface GraphPanelProps {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: OnNodesChange<Node>;
  onEdgesChange: OnEdgesChange<Edge>;
  onNodeClick: (_: unknown, node: Node) => void;
  intentContext: IntentContext | null;
  runAllText: string;
  graphTitle: string;
  graphHint: string;
  isExecuting: boolean;
	requiresApproval: boolean;
  onRunAll: () => void;
	onApproveAndRun: () => void;
	onCancel: () => void;
	onRetryFailed: () => void;
}

const intentLabels: Record<string, string> = {
  Paper_Reproduction: '论文复现',
  Framework_Comparison: '框架对比',
  Code_Execution: '代码执行',
  General: '通用任务',
};

export function GraphPanel(props: GraphPanelProps) {
  const {
    nodes,
    edges,
    onNodesChange,
    onEdgesChange,
    onNodeClick,
    intentContext,
    runAllText,
    graphTitle,
    graphHint,
    isExecuting,
	requiresApproval,
    onRunAll,
	onApproveAndRun,
	onCancel,
	onRetryFailed,
  } = props;
  const completedCount = nodes.filter((node) => node.data.status === 'completed').length;
  const runningCount = nodes.filter((node) => node.data.status === 'in_progress').length;
	const failedCount = nodes.filter((node) => node.data.status === 'failed').length;
  const pendingCount = nodes.filter((node) => node.data.task && node.data.status !== 'completed').length;
  const intentLabel = intentContext ? intentLabels[intentContext.intent_type] ?? intentContext.intent_type : null;
  const subtitle = intentContext?.raw_intent || graphHint;

  return (
    <div className="flex h-full min-w-0 flex-1 flex-col bg-slate-50">
      <div className="shrink-0 border-b border-slate-200 bg-white px-5 py-4">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0">
            <h2 className="flex items-center gap-2 text-sm font-semibold text-slate-800">
              <GitBranch className="h-4 w-4 text-blue-600" />
              {graphTitle}
            </h2>
            <p className="mt-1.5 max-w-3xl truncate text-xs text-slate-500" title={subtitle}>
              {subtitle}
            </p>
            <div className="mt-3 flex flex-wrap items-center gap-2 text-xs">
              {intentLabel && <span className="rounded-md bg-blue-50 px-2 py-1 font-medium text-blue-700">{intentLabel}</span>}
              <span className="rounded-md bg-slate-100 px-2 py-1 text-slate-600">{nodes.length} 个步骤</span>
              <span className="flex items-center gap-1 rounded-md bg-emerald-50 px-2 py-1 text-emerald-700">
                <CheckCircle2 className="h-3.5 w-3.5" />
                {completedCount}/{nodes.length} 完成
              </span>
              {runningCount > 0 && (
                <span className="flex items-center gap-1 rounded-md bg-amber-50 px-2 py-1 text-amber-700">
                  <LoaderCircle className="h-3.5 w-3.5 animate-spin" />
                  {runningCount} 运行中
                </span>
              )}
			  {failedCount > 0 && <span className="rounded-md bg-rose-50 px-2 py-1 font-medium text-rose-700">{failedCount} 失败</span>}
            </div>
          </div>
		  <div className="flex shrink-0 items-center gap-2">
			{isExecuting ? (
			  <button
				type="button"
				onClick={onCancel}
				title="取消计划"
				className="flex items-center gap-2 rounded-md border border-rose-200 bg-white px-3.5 py-2 text-sm font-semibold text-rose-700 transition-colors hover:bg-rose-50"
			  >
				<Square className="h-3.5 w-3.5 fill-current" />
				取消
			  </button>
			) : requiresApproval ? (
			  <button
				type="button"
				onClick={onApproveAndRun}
				className="flex items-center gap-2 rounded-md bg-emerald-600 px-3.5 py-2 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-emerald-700"
			  >
				<ShieldCheck className="h-4 w-4" />
				审批并运行
			  </button>
			) : failedCount > 0 ? (
			  <button
				type="button"
				onClick={onRetryFailed}
				className="flex items-center gap-2 rounded-md bg-amber-600 px-3.5 py-2 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-amber-700"
			  >
				<RotateCcw className="h-4 w-4" />
				重试失败节点
			  </button>
			) : (
			  <button
				type="button"
				onClick={onRunAll}
				disabled={pendingCount === 0}
				className="flex items-center gap-2 rounded-md bg-blue-600 px-3.5 py-2 text-sm font-semibold text-white shadow-sm transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:bg-slate-300"
			  >
				<Play className="h-4 w-4 fill-current" />
				{runAllText}
			  </button>
			)}
		  </div>
        </div>
      </div>

      <div className="relative min-h-0 flex-1">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={onNodeClick}
          nodesConnectable={false}
          nodesDraggable={false}
          fitView
          fitViewOptions={{ padding: 0.16, maxZoom: 1.08 }}
          className="bg-slate-50"
        >
          <Background color="#cbd5e1" gap={20} size={1} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
    </div>
  );
}
