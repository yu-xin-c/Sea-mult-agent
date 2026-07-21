import { CheckCircle2, Circle, CircleAlert, Clock3, LoaderCircle, ShieldAlert } from 'lucide-react';
import { getAgentIcon } from '../shared/agentVisuals';

interface CreateTaskNodeLabelOptions {
  assignedTo: string;
  taskName: string;
  status: string;
  step?: number;
}

const agentLabels: Record<string, string> = {
  librarian_agent: '文献智能体',
  coder_agent: '代码智能体',
  sandbox_agent: '沙箱智能体',
  data_agent: '数据智能体',
  research_coding_agent: '科研 Coding 智能体',
  general_agent: '通用智能体',
};

const statusMeta: Record<string, { label: string; className: string; icon: typeof Circle }> = {
  pending: { label: '等待', className: 'text-slate-500', icon: Clock3 },
  ready: { label: '就绪', className: 'text-blue-600', icon: Circle },
  in_progress: { label: '运行中', className: 'text-amber-600', icon: LoaderCircle },
  completed: { label: '已完成', className: 'text-emerald-600', icon: CheckCircle2 },
  failed: { label: '失败', className: 'text-red-600', icon: CircleAlert },
  blocked: { label: '已阻塞', className: 'text-slate-600', icon: ShieldAlert },
};

const getPrimaryTaskName = (taskName: string) => taskName.split(/\s+\/\s+/)[0]?.trim() || taskName;

export const createTaskNodeLabel = (options: CreateTaskNodeLabelOptions) => {
  const { assignedTo, taskName, status, step } = options;
  const statusState = statusMeta[status] ?? statusMeta.pending;
  const StatusIcon = statusState.icon;

  return (
    <div className="flex min-h-[90px] w-full flex-col px-3 py-2.5 text-left">
      <div className="flex items-center justify-between gap-2 text-xs">
        <div className="flex min-w-0 items-center gap-1.5 text-slate-500">
          {getAgentIcon(assignedTo)}
          <span className="truncate font-medium">{agentLabels[assignedTo] ?? assignedTo}</span>
        </div>
        <div className={`flex shrink-0 items-center gap-1 font-medium ${statusState.className}`}>
          <StatusIcon className={`h-3.5 w-3.5 ${status === 'in_progress' ? 'animate-spin' : ''}`} />
          <span>{statusState.label}</span>
        </div>
      </div>
      <div className="mt-2 flex min-w-0 gap-2">
        {typeof step === 'number' && (
          <span className="shrink-0 font-mono text-xs font-semibold text-slate-400">{String(step).padStart(2, '0')}</span>
        )}
        <div className="line-clamp-2 text-[13px] font-semibold leading-5 text-slate-800" title={taskName}>
          {getPrimaryTaskName(taskName)}
        </div>
      </div>
    </div>
  );
};
