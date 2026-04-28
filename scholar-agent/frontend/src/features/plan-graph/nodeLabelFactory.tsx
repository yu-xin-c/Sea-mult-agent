import { getAgentIcon } from '../shared/agentVisuals';

interface CreateTaskNodeLabelOptions {
  assignedTo: string;
  taskName: string;
  status: string;
  level?: number;
}

export const createTaskNodeLabel = (options: CreateTaskNodeLabelOptions) => {
  const { assignedTo, taskName, status, level } = options;
  return (
    <div className="flex flex-col gap-2 p-2 w-56">
      <div className="flex items-center justify-between border-b pb-2">
        <div className="flex items-center gap-2">
          {getAgentIcon(assignedTo)}
          <span className="font-semibold text-xs text-gray-700">{assignedTo}</span>
        </div>
        {typeof level === 'number' && <span className="text-[10px] uppercase tracking-wide text-gray-400">L{level + 1}</span>}
      </div>
      <div className="text-sm text-gray-800 text-left font-medium">{taskName}</div>
      <div className="text-xs text-gray-400 capitalize text-left">状态: {status}</div>
    </div>
  );
};
