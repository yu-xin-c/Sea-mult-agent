import { useRef } from 'react';
import { ChevronDown, ChevronUp, FileText, Loader2, Paperclip, Send, Sparkles, X } from 'lucide-react';
import type { UploadedFile } from '../../contracts/api';

interface ChatComposerProps {
  prompt: string;
  loading: boolean;
  isLoggedIn: boolean;
  showSuggestions: boolean;
	pendingAttachments: UploadedFile[];
	uploadingAttachments: boolean;
	attachmentError: string;
  setPrompt: (value: string) => void;
  setShowSuggestions: (next: boolean) => void;
  onSendMessage: () => void;
	onAttachFiles: (files: File[]) => void;
	onRemoveAttachment: (uploadId: string) => void;
}

const suggestions = [
  '帮我画一个正弦函数和余弦函数的对比图',
  '复现一下 Transformer 论文的核心架构并跑通测试',
  '对比一下 LangChain 和 LlamaIndex 的 RAG 性能',
  '分析一下这篇论文的主要创新点和局限性',
  '帮我复现 Attention Is All You Need 论文的代码',
];

export function ChatComposer(props: ChatComposerProps) {
  const {
		prompt, loading, isLoggedIn, showSuggestions, pendingAttachments, uploadingAttachments, attachmentError,
		setPrompt, setShowSuggestions, onSendMessage, onAttachFiles, onRemoveAttachment,
	} = props;
	const fileInputRef = useRef<HTMLInputElement>(null);

  return (
    <div className="border-t border-gray-200 bg-white p-4">
      <div className="mb-2 flex items-center justify-between px-1">
        <button
          onClick={() => setShowSuggestions(!showSuggestions)}
          className="flex items-center gap-1.5 text-[10px] font-bold text-gray-400 uppercase tracking-widest hover:text-blue-500 transition-colors group"
        >
          <Sparkles className={`w-3 h-3 ${showSuggestions ? 'text-blue-500' : 'text-gray-400'} group-hover:animate-pulse`} />
          试试推荐指令
          {showSuggestions ? <ChevronDown className="w-3 h-3" /> : <ChevronUp className="w-3 h-3" />}
        </button>
      </div>

      {showSuggestions && (
        <div className="mb-4 flex flex-wrap gap-2 animate-in slide-in-from-bottom-2 duration-300">
          {suggestions.map((text) => (
            <button
              key={text}
              onClick={() => setPrompt(text)}
              className="text-[11px] bg-blue-50 text-blue-600 border border-blue-100 px-3 py-1.5 rounded-full hover:bg-blue-100 transition-all active:scale-95 shadow-sm hover:shadow-md"
            >
              {text}
            </button>
          ))}
        </div>
      )}

		{(pendingAttachments.length > 0 || uploadingAttachments || attachmentError) && (
			<div className="mb-2 flex flex-wrap gap-2" aria-live="polite">
				{pendingAttachments.map((attachment) => (
					<div key={attachment.id} className="flex max-w-full items-center gap-1.5 rounded border border-gray-200 bg-gray-50 px-2 py-1 text-xs text-gray-700">
						<FileText className="h-3.5 w-3.5 shrink-0 text-blue-600" />
						<span className="max-w-48 truncate">{attachment.name}</span>
						<span className="shrink-0 text-gray-400">{Math.max(1, Math.round(attachment.size / 1024))}KB</span>
						<button type="button" onClick={() => onRemoveAttachment(attachment.id)} className="rounded p-0.5 text-gray-400 hover:bg-gray-200 hover:text-gray-700" title="移除附件" aria-label={`移除 ${attachment.name}`}>
							<X className="h-3.5 w-3.5" />
						</button>
					</div>
				))}
				{uploadingAttachments && <span className="flex items-center gap-1 text-xs text-blue-600"><Loader2 className="h-3.5 w-3.5 animate-spin" />正在上传</span>}
				{attachmentError && <span className="w-full text-xs text-red-600">{attachmentError}</span>}
			</div>
		)}

		<input
			ref={fileInputRef}
			type="file"
			multiple
			accept=".pdf,.txt,.md,.json,.jsonl,.yaml,.yml,.toml,.py,.ipynb,.csv,.tsv"
			className="hidden"
			onChange={(event) => {
				onAttachFiles(Array.from(event.target.files ?? []));
				event.target.value = '';
			}}
		/>

		<div className="flex gap-2 relative">
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault();
              onSendMessage();
            }
          }}
          placeholder={
            isLoggedIn
              ? '例如：帮我用 LangChain 和 LlamaIndex 做一个 RAG 框架的对比评测...'
              : '游客模式下也可直接提问；登录后会持久保存会话记录'
          }
		  className="flex-1 resize-none rounded-lg border border-gray-300 p-3 pb-11 focus:outline-none focus:ring-2 focus:ring-blue-500 shadow-sm bg-gray-50 focus:bg-white transition-all"
          rows={3}
        />
		<button
			type="button"
			onClick={() => fileInputRef.current?.click()}
			disabled={loading || uploadingAttachments || pendingAttachments.length >= 8}
			className="absolute bottom-2 left-2 rounded p-2 text-gray-500 hover:bg-gray-200 hover:text-blue-600 disabled:cursor-not-allowed disabled:opacity-40"
			title="添加论文、数据或实验配置"
			aria-label="添加附件"
		>
			{uploadingAttachments ? <Loader2 className="h-5 w-5 animate-spin" /> : <Paperclip className="h-5 w-5" />}
		</button>
        <button
		  type="button"
          onClick={onSendMessage}
		  disabled={loading || uploadingAttachments || (!prompt.trim() && pendingAttachments.length === 0)}
          className="absolute right-2 bottom-2 p-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors shadow-lg"
		  title="发送消息"
		  aria-label="发送消息"
        >
          <Send className="w-5 h-5" />
        </button>
      </div>
    </div>
  );
}
