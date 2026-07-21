import type { RefObject } from 'react';
import { Code, Eye, FileText, GitBranch, Loader2, Maximize2, Play, TerminalSquare, X } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import type { ExecutionDisplayMode } from '../../app/hooks/useScholarRuntime';
import type { Task } from '../../contracts/api';
import { ClaimEvidenceGraphView } from '../claim-evidence/ClaimEvidenceGraphView';
import { getAgentIcon } from '../shared/agentVisuals';

type CompactMode = Exclude<ExecutionDisplayMode, 'report-expanded' | 'plot-expanded' | 'evidence-expanded'>;

interface ExecutionSidebarProps {
  selectedTask: Task;
  width: string;
  isExecuting: boolean;
  displayMode: ExecutionDisplayMode;
  executionLogs: string;
  executionResult: string;
  executionCode: string;
  executionStructuredData: string;
  executionImage: string;
  logsEndRef: RefObject<HTMLDivElement | null>;
  onClose: () => void;
  onExecute: () => void;
  onChangeDisplayMode: (mode: ExecutionDisplayMode) => void;
}

const resolveCompactMode = (displayMode: ExecutionDisplayMode): CompactMode => {
  if (displayMode === 'report-expanded') return 'report';
  if (displayMode === 'plot-expanded') return 'plot';
  if (displayMode === 'evidence-expanded') return 'evidence';
  return displayMode;
};

const reportAgents = new Set(['librarian_agent', 'data_agent', 'research_coding_agent']);

export function ExecutionSidebar(props: ExecutionSidebarProps) {
  const {
    selectedTask,
    width,
    isExecuting,
    displayMode,
    executionLogs,
    executionResult,
    executionCode,
    executionStructuredData,
    executionImage,
    logsEndRef,
    onClose,
    onExecute,
    onChangeDisplayMode,
  } = props;

  const activeMode = resolveCompactMode(displayMode);
  const isExpanded = displayMode === 'report-expanded' || displayMode === 'plot-expanded' || displayMode === 'evidence-expanded';

  return (
    <div
      style={{ width }}
      className={`scholar-execution-sidebar z-20 flex flex-col border-l border-gray-200 bg-white shadow-2xl transition-all duration-300 ${
        isExpanded ? 'absolute inset-0' : 'relative'
      }`}
    >
      {displayMode === 'evidence-expanded' ? (
        <ExpandedEvidenceView title={selectedTask.Name} rawGraph={executionStructuredData} onClose={() => onChangeDisplayMode('evidence')} />
      ) : displayMode === 'plot-expanded' ? (
        <ExpandedPlotView executionImage={executionImage} onClose={() => onChangeDisplayMode('plot')} />
      ) : displayMode === 'report-expanded' ? (
        <ExpandedReportView title={selectedTask.Name} executionResult={executionResult} onClose={() => onChangeDisplayMode('report')} />
      ) : (
        <ExecutionSidebarShell
          selectedTask={selectedTask}
          isExecuting={isExecuting}
          activeMode={activeMode}
          executionLogs={executionLogs}
          executionResult={executionResult}
          executionCode={executionCode}
          executionStructuredData={executionStructuredData}
          executionImage={executionImage}
          logsEndRef={logsEndRef}
          onClose={onClose}
          onExecute={onExecute}
          onChangeDisplayMode={onChangeDisplayMode}
        />
      )}
    </div>
  );
}

interface ExpandedEvidenceViewProps {
  title: string;
  rawGraph: string;
  onClose: () => void;
}

function ExpandedEvidenceView({ title, rawGraph, onClose }: ExpandedEvidenceViewProps) {
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-white p-6">
      <div className="mb-4 flex flex-shrink-0 items-center justify-between border-b border-slate-200 pb-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-semibold text-cyan-800">
            <GitBranch className="h-4 w-4" />
            Claim-to-Evidence Graph
          </div>
          <h2 className="mt-1 truncate text-xl font-semibold text-slate-900">{title}</h2>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="p-2 text-slate-500 transition-colors hover:bg-slate-100 hover:text-slate-900"
          title="退出全屏证据图"
          aria-label="退出全屏证据图"
        >
          <X className="h-5 w-5" />
        </button>
      </div>
      <div className="min-h-0 flex-1 border border-slate-200">
        <ClaimEvidenceGraphView rawGraph={rawGraph} expanded />
      </div>
    </div>
  );
}

interface ExpandedPlotViewProps {
  executionImage: string;
  onClose: () => void;
}

function ExpandedPlotView({ executionImage, onClose }: ExpandedPlotViewProps) {
  return (
    <div className="flex-1 flex flex-col p-10 bg-white animate-in zoom-in-95 duration-300 overflow-hidden">
      <div className="flex-shrink-0 flex items-center justify-between mb-8 pb-6 border-b border-gray-100">
        <div className="flex items-center gap-5">
          <div className="p-4 bg-purple-600 rounded-3xl text-white shadow-xl rotate-3">
            <Maximize2 className="w-8 h-8" />
          </div>
          <div>
            <h2 className="text-3xl font-black text-gray-900 tracking-tight">生成的图表可视化</h2>
            <div className="flex items-center gap-2 mt-1">
              <span className="text-xs font-bold bg-purple-100 text-purple-700 px-2 py-0.5 rounded-full uppercase tracking-wider">Visual Result</span>
              <span className="text-sm text-gray-400 font-medium">Rendered by Matplotlib in Sandbox</span>
            </div>
          </div>
        </div>
        <button
          onClick={onClose}
          className="p-4 hover:bg-red-50 hover:text-red-500 rounded-3xl transition-all text-gray-400 active:scale-90 shadow-sm hover:shadow-md"
        >
          <X className="w-8 h-8" />
        </button>
      </div>
      <div className="flex-1 flex items-center justify-center overflow-hidden bg-gray-50 rounded-3xl p-8 border border-gray-100 shadow-inner">
        <img
          src={`data:image/png;base64,${executionImage}`}
          alt="Full Resolution Plot"
          className="max-w-full max-h-full object-contain rounded-xl shadow-2xl transition-transform hover:scale-105 duration-500"
        />
      </div>
    </div>
  );
}

interface ExpandedReportViewProps {
  title: string;
  executionResult: string;
  onClose: () => void;
}

function ExpandedReportView({ title, executionResult, onClose }: ExpandedReportViewProps) {
  return (
    <div className="flex-1 flex flex-col p-10 bg-white animate-in zoom-in-95 duration-300 overflow-hidden">
      <div className="flex-shrink-0 flex items-center justify-between mb-8 pb-6 border-b border-gray-100">
        <div className="flex items-center gap-5">
          <div className="p-4 bg-blue-600 rounded-3xl text-white shadow-xl rotate-3">
            <FileText className="w-8 h-8" />
          </div>
          <div>
            <h2 className="text-3xl font-black text-gray-900 tracking-tight">{title}</h2>
            <div className="flex items-center gap-2 mt-1">
              <span className="text-xs font-bold bg-blue-100 text-blue-700 px-2 py-0.5 rounded-full uppercase tracking-wider">Analysis Report</span>
              <span className="text-sm text-gray-400 font-medium">Powered by ScholarAgent Insight Engine</span>
            </div>
          </div>
        </div>
        <button
          onClick={onClose}
          className="p-4 hover:bg-red-50 hover:text-red-500 rounded-3xl transition-all text-gray-400 active:scale-90 shadow-sm hover:shadow-md"
        >
          <X className="w-8 h-8" />
        </button>
      </div>
      <div className="flex-1 overflow-y-auto px-4 min-h-0 scrollbar-thin scrollbar-thumb-gray-200">
        <div className="max-w-4xl mx-auto prose prose-slate prose-lg lg:prose-xl text-gray-800 prose-headings:text-blue-900 prose-strong:text-blue-700 prose-code:bg-blue-50 prose-code:text-blue-600 prose-code:px-2 prose-code:py-0.5 prose-code:rounded-lg prose-img:rounded-3xl prose-img:shadow-2xl pb-10">
          <ReactMarkdown remarkPlugins={[remarkGfm, remarkMath]} rehypePlugins={[rehypeKatex]}>
            {executionResult}
          </ReactMarkdown>
        </div>
      </div>
    </div>
  );
}

interface ExecutionSidebarShellProps {
  selectedTask: Task;
  isExecuting: boolean;
  activeMode: CompactMode;
  executionLogs: string;
  executionResult: string;
  executionCode: string;
  executionStructuredData: string;
  executionImage: string;
  logsEndRef: RefObject<HTMLDivElement | null>;
  onClose: () => void;
  onExecute: () => void;
  onChangeDisplayMode: (mode: ExecutionDisplayMode) => void;
}

function ExecutionSidebarShell(props: ExecutionSidebarShellProps) {
  const {
    selectedTask,
    isExecuting,
    activeMode,
    executionLogs,
    executionResult,
    executionCode,
    executionStructuredData,
    executionImage,
    logsEndRef,
    onClose,
    onExecute,
    onChangeDisplayMode,
  } = props;
  const ablationBudget = selectedTask.Type === 'ablation_design' ? selectedTask.Inputs : undefined;

  return (
    <>
      <div className="p-4 border-b border-gray-200 flex justify-between items-center bg-gray-50">
        <h3 className="font-bold text-gray-800 flex items-center gap-2 text-base">
          {getAgentIcon(selectedTask.AssignedTo)}
          节点执行面板
        </h3>
        <button
          type="button"
          onClick={onClose}
          className="text-gray-500 hover:text-gray-700 p-1.5 hover:bg-gray-200 rounded-full transition-all"
          title="关闭节点执行面板"
          aria-label="关闭节点执行面板"
        >
          <X className="w-5 h-5" />
        </button>
      </div>

      <div className="p-5 flex-1 overflow-y-auto flex flex-col gap-5">
        <div className="bg-white p-4 rounded-2xl border border-gray-100 shadow-sm">
          <label className="text-[10px] font-bold text-gray-400 uppercase tracking-wider block mb-1">任务名称</label>
          <div className="text-base font-bold text-gray-800 leading-tight">{selectedTask.Name}</div>
        </div>

        <div className="flex items-center justify-between px-1">
          <label className="text-xs font-bold text-gray-500 uppercase tracking-tight">负责 Agent</label>
          <div className="text-xs font-black text-blue-700 bg-blue-50 px-3 py-1.5 rounded-full border border-blue-100 shadow-sm font-mono">
            {selectedTask.AssignedTo}
          </div>
        </div>

        {ablationBudget && (
          <div className="grid grid-cols-3 border-y border-gray-100 py-3 text-center">
            <BudgetValue label="实验" value={ablationBudget.ablation_max_experiments} suffix="组" />
            <BudgetValue label="GPU" value={ablationBudget.ablation_max_gpu_minutes} suffix="分钟" />
            <BudgetValue label="总耗时" value={ablationBudget.ablation_max_wall_minutes} suffix="分钟" />
          </div>
        )}

        <button
          onClick={onExecute}
          disabled={isExecuting}
          className="w-full bg-blue-600 hover:bg-blue-700 text-white font-black py-4 px-6 rounded-2xl flex items-center justify-center gap-3 disabled:opacity-50 disabled:cursor-not-allowed transition-all shadow-[0_10px_20px_-10px_rgba(37,99,235,0.5)] active:scale-[0.98] active:shadow-inner"
        >
          {isExecuting ? (
            <span className="animate-pulse flex items-center gap-2">
              <Loader2 className="w-5 h-5 animate-spin" />
              正在深度解析...
            </span>
          ) : (
            <>
              <Play className="w-5 h-5 fill-current" />
              启动 Agent 任务
            </>
          )}
        </button>

        {(executionResult || executionCode || executionStructuredData) && (
          <div className="flex border-b border-gray-100 mt-2 items-center justify-between">
            <div className="flex flex-1">
              <button
                onClick={() => onChangeDisplayMode('logs')}
                className={`flex-1 py-3 text-xs font-black text-center border-b-2 transition-all ${
                  activeMode === 'logs' ? 'border-blue-500 text-blue-600' : 'border-transparent text-gray-400 hover:text-gray-600'
                }`}
              >
                实时日志
              </button>
              {executionCode && (
                <button
                  onClick={() => onChangeDisplayMode('code')}
                  className={`flex-1 py-3 text-xs font-black text-center border-b-2 flex items-center justify-center gap-1 transition-all ${
                    activeMode === 'code' ? 'border-blue-500 text-blue-600' : 'border-transparent text-gray-400 hover:text-gray-600'
                  }`}
                >
                  <Code className="w-4 h-4" />
                  沙箱代码
                </button>
              )}
              {executionImage && (
                <button
                  onClick={() => onChangeDisplayMode('plot')}
                  className={`flex-1 py-3 text-xs font-black text-center border-b-2 flex items-center justify-center gap-1 transition-all ${
                    activeMode === 'plot' ? 'border-blue-500 text-blue-600' : 'border-transparent text-gray-400 hover:text-gray-600'
                  }`}
                >
                  <Maximize2 className="w-4 h-4" />
                  生成图表
                </button>
              )}
              {selectedTask.Type === 'claim_evidence_build' && executionStructuredData && (
                <button
                  onClick={() => onChangeDisplayMode('evidence')}
                  className={`flex-1 py-3 text-xs font-black text-center border-b-2 flex items-center justify-center gap-1 transition-all ${
                    activeMode === 'evidence' ? 'border-cyan-600 text-cyan-700' : 'border-transparent text-gray-400 hover:text-gray-600'
                  }`}
                >
                  <GitBranch className="w-4 h-4" />
                  证据图
                </button>
              )}
              {reportAgents.has(selectedTask.AssignedTo) && executionResult && (
                <button
                  onClick={() => onChangeDisplayMode('report')}
                  className={`flex-1 py-3 text-xs font-black text-center border-b-2 flex items-center justify-center gap-1 transition-all ${
                    activeMode === 'report' ? 'border-blue-500 text-blue-600' : 'border-transparent text-gray-400 hover:text-gray-600'
                  }`}
                >
                  <Eye className="w-4 h-4" />
                  分析报告
                </button>
              )}
            </div>
            {activeMode === 'report' && (
              <button
                onClick={() => onChangeDisplayMode('report-expanded')}
                className="ml-3 p-2.5 text-blue-500 hover:bg-blue-50 rounded-xl transition-all active:scale-90 border border-blue-50 shadow-sm"
                title="全屏阅读报告"
              >
                <Maximize2 className="w-4 h-4" />
              </button>
            )}
            {activeMode === 'plot' && (
              <button
                onClick={() => onChangeDisplayMode('plot-expanded')}
                className="ml-3 p-2.5 text-blue-500 hover:bg-blue-50 rounded-xl transition-all active:scale-90 border border-blue-50 shadow-sm"
                title="全屏查看图表"
              >
                <Maximize2 className="w-4 h-4" />
              </button>
            )}
            {activeMode === 'evidence' && (
              <button
                onClick={() => onChangeDisplayMode('evidence-expanded')}
                className="ml-3 p-2.5 text-cyan-700 hover:bg-cyan-50 rounded-xl transition-all active:scale-90 border border-cyan-100 shadow-sm"
                title="全屏查看证据图"
              >
                <Maximize2 className="w-4 h-4" />
              </button>
            )}
          </div>
        )}

        <div className="mt-1 flex-1 flex flex-col min-h-0">
          {activeMode === 'logs' ? (
            <>
              <label className="text-[10px] font-bold text-gray-400 uppercase mb-2 flex items-center gap-1 tracking-wider">
                <TerminalSquare className="w-3 h-3" />
                Pipeline Output
              </label>
              <div className="bg-gray-900 rounded-2xl p-5 flex-1 overflow-y-auto font-mono text-[11px] text-green-400 leading-relaxed shadow-2xl border border-gray-800 whitespace-pre-wrap selection:bg-green-800 selection:text-white scrollbar-thin scrollbar-thumb-gray-700">
                {executionLogs || '>>> 准备就绪，等待响应...'}
                {executionResult && !reportAgents.has(selectedTask.AssignedTo) && (
                  <div className="mt-5 pt-5 border-t border-gray-800 text-blue-400 font-bold">
                    [Output]:<br />
                    {executionResult}
                  </div>
                )}
                <div ref={logsEndRef} />
              </div>
            </>
          ) : activeMode === 'code' ? (
            <div className="bg-gray-50 rounded-2xl border border-gray-200 p-6 flex-1 overflow-y-auto shadow-inner prose prose-slate prose-sm max-w-none text-gray-800 h-64">
              <ReactMarkdown remarkPlugins={[remarkGfm, remarkMath]} rehypePlugins={[rehypeKatex]}>
                {`\`\`\`python\n${executionCode}\n\`\`\``}
              </ReactMarkdown>
            </div>
          ) : activeMode === 'plot' ? (
            <div className="bg-white rounded-2xl border border-gray-100 p-2 flex-1 flex flex-col items-center justify-center overflow-hidden shadow-inner h-64">
              <img src={`data:image/png;base64,${executionImage}`} alt="Generated Plot" className="max-w-full max-h-full object-contain rounded-lg shadow-md" />
              <div className="mt-2 text-[10px] text-gray-400">点击下方按钮可全屏查看</div>
            </div>
          ) : activeMode === 'evidence' ? (
            <div className="h-96 min-h-0 flex-1 overflow-hidden border border-slate-200 bg-white">
              <ClaimEvidenceGraphView rawGraph={executionStructuredData} />
            </div>
          ) : (
            <div className="bg-white rounded-2xl border border-gray-100 p-6 flex-1 overflow-y-auto shadow-inner prose prose-slate prose-sm max-w-none text-gray-800 prose-headings:text-blue-900 prose-strong:text-blue-700 prose-code:bg-blue-50 prose-code:text-blue-600 prose-code:px-1 prose-code:rounded h-64">
              <ReactMarkdown remarkPlugins={[remarkGfm, remarkMath]} rehypePlugins={[rehypeKatex]}>
                {executionResult}
              </ReactMarkdown>
            </div>
          )}
        </div>
      </div>
    </>
  );
}

function BudgetValue({ label, value, suffix }: { label: string; value: unknown; suffix: string }) {
  const displayValue = typeof value === 'number' || typeof value === 'string' ? String(value) : '-';
  return (
    <div>
      <div className="text-[10px] font-bold text-gray-400">{label}</div>
      <div className="mt-1 text-sm font-bold text-gray-800">{displayValue} {suffix}</div>
    </div>
  );
}
