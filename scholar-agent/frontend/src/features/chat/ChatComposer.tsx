import { ChevronDown, ChevronUp, Send, Sparkles } from 'lucide-react';

interface ChatComposerProps {
  prompt: string;
  loading: boolean;
  isLoggedIn: boolean;
  showSuggestions: boolean;
  setPrompt: (value: string) => void;
  setShowSuggestions: (next: boolean) => void;
  onSendMessage: () => void;
}

const suggestions = [
  '帮我画一个正弦函数和余弦函数的对比图',
  '复现一下 Transformer 论文的核心架构并跑通测试',
  '对比一下 LangChain 和 LlamaIndex 的 RAG 性能',
  '分析一下这篇论文的主要创新点和局限性',
  '帮我复现 Attention Is All You Need 论文的代码',
];

export function ChatComposer(props: ChatComposerProps) {
  const { prompt, loading, isLoggedIn, showSuggestions, setPrompt, setShowSuggestions, onSendMessage } = props;

  return (
    <div className="p-4 bg-white border-t border-gray-200">
      <div className="flex items-center justify-between mb-2 px-1">
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
        <div className="flex flex-wrap gap-2 mb-4 animate-in slide-in-from-bottom-2 duration-300">
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
          className="flex-1 resize-none rounded-xl border border-gray-300 p-3 pr-12 focus:outline-none focus:ring-2 focus:ring-blue-500 shadow-sm bg-gray-50 focus:bg-white transition-all"
          rows={3}
        />
        <button
          onClick={onSendMessage}
          disabled={loading || !prompt.trim()}
          className="absolute right-2 bottom-2 p-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors shadow-lg"
        >
          <Send className="w-5 h-5" />
        </button>
      </div>
    </div>
  );
}
