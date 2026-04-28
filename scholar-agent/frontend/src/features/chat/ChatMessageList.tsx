import { Bot, FileText, Languages, Maximize2, X } from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { ChatMessage } from '../../contracts/api';

interface PdfActions {
  onOpenPdf: () => void;
  onClosePdf: () => void;
  onFullTranslation: () => void;
}

interface TaskActions {
  onOpenTaskView: (taskId: string, mode: 'plot' | 'report') => void;
}

interface ChatMessageListProps {
  chatHistory: ChatMessage[];
  loading: boolean;
  isLoggedIn: boolean;
  pdfActions: PdfActions;
  taskActions: TaskActions;
}

export function ChatMessageList(props: ChatMessageListProps) {
  const { chatHistory, loading, isLoggedIn, pdfActions, taskActions } = props;

  return (
    <div className="flex-1 overflow-y-auto p-4 space-y-4">
      {!isLoggedIn && (
        <div className="rounded-2xl border border-dashed border-blue-200 bg-blue-50 px-4 py-3 text-sm leading-6 text-slate-600">
          当前为游客模式：可以直接聊天和新建临时 session，但刷新页面后不会保留会话记录。
        </div>
      )}
      {chatHistory.map((msg, i) => (
        <div key={i} className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
          <div className={`max-w-[85%] flex flex-col gap-2 ${msg.role === 'user' ? 'items-end' : 'items-start'}`}>
            <div
              className={`rounded-2xl px-4 py-3 shadow-sm ${
                msg.role === 'user' ? 'bg-blue-600 text-white rounded-br-none' : 'bg-gray-100 text-gray-800 rounded-bl-none'
              }`}
            >
              {msg.role === 'user' ? (
                msg.text
              ) : (
                <div className="prose prose-sm prose-slate max-w-none prose-p:leading-snug prose-pre:my-1 prose-pre:bg-gray-800 prose-pre:text-gray-100 prose-code:text-blue-600 prose-code:bg-blue-50 prose-code:px-1 prose-code:rounded">
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>{msg.text}</ReactMarkdown>
                </div>
              )}
            </div>

            {msg.actions && msg.actions.length > 0 && (
              <div className="flex gap-2 mt-1 animate-in fade-in slide-in-from-top-1 duration-300">
                {msg.actions.includes('open_pdf') && (
                  <button
                    onClick={pdfActions.onOpenPdf}
                    className="flex items-center gap-1 text-xs bg-white text-blue-600 border border-blue-200 px-3 py-1.5 rounded-full hover:bg-blue-50 shadow-sm transition-all active:scale-95"
                  >
                    <FileText className="w-3 h-3" />
                    打开论文原文
                  </button>
                )}
                {msg.actions.includes('translate_full') && (
                  <button
                    onClick={pdfActions.onFullTranslation}
                    className="flex items-center gap-1 text-xs bg-white text-purple-600 border border-purple-200 px-3 py-1.5 rounded-full hover:bg-purple-50 shadow-sm transition-all active:scale-95"
                  >
                    <Languages className="w-3 h-3" />
                    全文翻译
                  </button>
                )}
                {msg.actions.includes('close_pdf') && (
                  <button
                    onClick={pdfActions.onClosePdf}
                    className="flex items-center gap-1 text-xs bg-white text-gray-600 border border-gray-200 px-3 py-1.5 rounded-full hover:bg-gray-50 shadow-sm transition-all active:scale-95"
                  >
                    <X className="w-3 h-3" />
                    关闭阅读器
                  </button>
                )}
                {msg.actions.includes('view_plot') && msg.taskId && (
                  <button
                    onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'plot')}
                    className="flex items-center gap-1 text-xs bg-white text-orange-600 border border-orange-200 px-3 py-1.5 rounded-full hover:bg-orange-50 shadow-sm transition-all active:scale-95"
                  >
                    <Maximize2 className="w-3 h-3" />
                    查看生成的图表
                  </button>
                )}
                {msg.actions.includes('view_report') && msg.taskId && (
                  <button
                    onClick={() => taskActions.onOpenTaskView(msg.taskId as string, 'report')}
                    className="flex items-center gap-1 text-xs bg-white text-green-600 border border-green-200 px-3 py-1.5 rounded-full hover:bg-green-50 shadow-sm transition-all active:scale-95"
                  >
                    <FileText className="w-3 h-3" />
                    查看分析报告
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
      ))}
      {loading && (
        <div className="flex justify-start">
          <div className="bg-gray-100 rounded-2xl rounded-bl-none px-4 py-3 text-gray-500 animate-pulse flex items-center gap-2">
            <Bot className="w-4 h-4" />
            正在使用 Planner 编排多智能体任务拓扑图...
          </div>
        </div>
      )}
    </div>
  );
}
