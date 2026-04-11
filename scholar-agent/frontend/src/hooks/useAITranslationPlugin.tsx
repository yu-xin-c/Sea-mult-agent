import { useState } from 'react';
import { 
  highlightPlugin, 
  type RenderHighlightTargetProps, 
  type RenderHighlightContentProps 
} from '@react-pdf-viewer/highlight';
import { Sparkles, X, Loader2, MessageSquare } from 'lucide-react';
import '@react-pdf-viewer/highlight/lib/styles/index.css';

/**
 * AI 划词翻译插件
 * @param onAskAI - 可选的回调函数，用于将选中文本一键发送给左侧的主力聊天面板
 */
export const useAITranslationPlugin = (onAskAI?: (text: string) => void) => {
  const [translatedText, setTranslatedText] = useState('');
  const [isTranslating, setIsTranslating] = useState(false);

  // 关键节点：调用大模型 API 进行翻译
  const fetchTranslation = async (text: string) => {
    if (!text || !text.trim()) return;

    setIsTranslating(true);
    setTranslatedText('');
    
    try {
      // 在这个演示版本中，我们模拟 AI 翻译逻辑。
      // 实际应用中，这里应该调用后端的翻译接口
      const mockResult = `【AI 译文】：在真实的实现中，这里将调用 DeepSeek 返回关于 "${text.substring(0, 30)}..." 的高质量中文学术翻译。系统已自动识别上下文专业术语。`;
      
      for (let i = 0; i < mockResult.length; i++) {
        await new Promise(r => setTimeout(r, 20));
        setTranslatedText(prev => prev + mockResult[i]);
      }
    } catch (error) {
      setTranslatedText('请求 AI 翻译时发生网络错误，请重试。');
    } finally {
      setIsTranslating(false);
    }
  };

  const highlightPluginInstance = highlightPlugin({
    renderHighlightTarget: (props: RenderHighlightTargetProps) => (
      <div
        style={{
          position: 'absolute',
          left: `${props.selectionRegion.left}%`,
          top: `${props.selectionRegion.top + props.selectionRegion.height}%`,
          transform: 'translate(0, 8px)',
          zIndex: 100,
        }}
        className="bg-white border border-blue-200 rounded-lg shadow-2xl animate-in fade-in zoom-in duration-200"
      >
        <button
          onClick={() => {
            props.toggle();
            fetchTranslation(props.selectedText);
          }}
          className="flex items-center gap-2 px-3 py-2 text-sm font-semibold text-blue-600 hover:bg-blue-50 rounded-lg transition-all"
        >
          <Sparkles className="w-4 h-4" />
          🪄 AI 翻译至中文
        </button>
      </div>
    ),
    
    renderHighlightContent: (props: RenderHighlightContentProps) => (
      <div
        style={{
          position: 'absolute',
          left: `${props.selectionRegion.left}%`,
          top: `${props.selectionRegion.top + props.selectionRegion.height}%`,
          transform: 'translate(0, 8px)',
          zIndex: 110,
          width: '320px',
        }}
        className="bg-white border border-gray-200 rounded-xl shadow-2xl overflow-hidden flex flex-col animate-in slide-in-from-top-2 duration-300"
      >
        <div className="bg-blue-600 px-3 py-2 flex justify-between items-center text-white">
          <span className="text-xs font-bold flex items-center gap-1">
            <Sparkles className="w-3 h-3" />
            AI 翻译助手
          </span>
          <button 
            onClick={props.cancel} 
            className="text-blue-100 hover:text-white transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
        
        <div className="p-4 max-h-64 overflow-y-auto text-sm text-gray-700 leading-relaxed bg-white">
          {isTranslating && !translatedText ? (
            <div className="flex flex-col items-center justify-center py-4 gap-2 text-blue-500">
              <Loader2 className="w-6 h-6 animate-spin" />
              <span className="text-xs animate-pulse">正在深度解析文献上下文...</span>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="p-3 bg-gray-50 rounded-lg border border-gray-100 italic text-gray-500 text-xs">
                "{props.selectedText.substring(0, 100)}{props.selectedText.length > 100 ? '...' : ''}"
              </div>
              <p className="text-gray-800 font-medium">{translatedText}</p>
              
              {onAskAI && (
                <button 
                  onClick={() => {
                    onAskAI(props.selectedText);
                    props.cancel();
                  }}
                  className="w-full flex items-center justify-center gap-2 mt-2 py-2 bg-blue-50 hover:bg-blue-100 border border-blue-200 rounded-lg text-xs text-blue-700 font-semibold transition-all"
                >
                  <MessageSquare className="w-3 h-3" />
                  针对此段落向 ScholarAgent 追问
                </button>
              )}
            </div>
          )}
        </div>
      </div>
    ),
  });

  return highlightPluginInstance;
};
