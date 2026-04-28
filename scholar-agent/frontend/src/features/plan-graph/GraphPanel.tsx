import { Background, Controls, Panel, ReactFlow } from '@xyflow/react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import { Play, TerminalSquare } from 'lucide-react';
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
  onRunAll: () => void;
}

const stringifyEntity = (value: unknown) => {
  if (Array.isArray(value)) return value.join(', ');
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (value == null || value === '') return '-';
  return String(value);
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
    onRunAll,
  } = props;

  return (
    <div className="flex-1 h-full relative">
      <div className="absolute top-4 left-4 z-10 bg-white px-4 py-2 rounded-lg shadow-sm border border-gray-200 max-w-md">
        <h2 className="font-semibold text-gray-700 flex items-center gap-2">
          <TerminalSquare className="w-4 h-4" />
          {graphTitle}
        </h2>
        <p className="text-xs text-gray-500 mt-1">{graphHint}</p>
        {intentContext && (
          <div className="mt-3 space-y-2 border-t border-gray-100 pt-3 text-xs text-gray-600">
            <div className="flex flex-wrap gap-2">
              <span className="rounded-full bg-blue-50 px-2 py-1 text-blue-700">intent_type: {intentContext.intent_type}</span>
              <span className="rounded-full bg-slate-100 px-2 py-1 text-slate-700">
                entities: {Object.keys(intentContext.entities || {}).length}
              </span>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="rounded-md bg-gray-50 px-2 py-1">frameworks: {stringifyEntity(intentContext.entities?.frameworks)}</div>
              <div className="rounded-md bg-gray-50 px-2 py-1">paper_title: {stringifyEntity(intentContext.entities?.paper_title)}</div>
              <div className="rounded-md bg-gray-50 px-2 py-1">needs_plot: {stringifyEntity(intentContext.entities?.needs_plot)}</div>
              <div className="rounded-md bg-gray-50 px-2 py-1">needs_fix: {stringifyEntity(intentContext.entities?.needs_fix)}</div>
              <div className="rounded-md bg-gray-50 px-2 py-1">
                needs_benchmark: {stringifyEntity(intentContext.entities?.needs_benchmark)}
              </div>
              <div className="rounded-md bg-gray-50 px-2 py-1">output_mode: {stringifyEntity(intentContext.entities?.output_mode)}</div>
            </div>
          </div>
        )}
      </div>

      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={onNodeClick}
        fitView
        className="bg-gray-50"
      >
        <Background color="#ccc" gap={16} />
        <Controls />
        <Panel position="top-right">
          <button
            onClick={onRunAll}
            disabled={isExecuting || nodes.filter((n) => n.data.task && n.data.status !== 'completed').length === 0}
            className="bg-blue-600 hover:bg-blue-700 text-white font-bold py-2 px-4 rounded-xl shadow-lg flex items-center gap-2 transition-all active:scale-95 disabled:opacity-50 disabled:grayscale"
          >
            <Play className="w-4 h-4 fill-current" />
            {runAllText}
          </button>
        </Panel>
      </ReactFlow>
    </div>
  );
}
