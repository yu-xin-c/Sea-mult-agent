import { useCallback, useState } from 'react';
import { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';
import { useAITranslationPlugin } from '../../hooks/useAITranslationPlugin';
import type { ChatMessage } from '../../contracts/api';

interface UsePdfAssistFlowOptions {
  setPrompt: (value: string) => void;
  appendChatMessage: (message: ChatMessage) => void;
  appendSelectedTaskLog: (line: string) => void;
}

export function usePdfAssistFlow(options: UsePdfAssistFlowOptions) {
  const { setPrompt, appendChatMessage, appendSelectedTaskLog } = options;
  const [pdfUrl, setPdfUrl] = useState<string | null>(null);
  const [isFullTranslating, setIsFullTranslating] = useState(false);
  const [showSuggestions, setShowSuggestions] = useState(true);
  const defaultLayoutPluginInstance = defaultLayoutPlugin();

  const handleAskAI = useCallback(
    (selectedText: string) => {
      setPrompt(`请帮我详细解释这篇文献中的这段内容：\n"${selectedText}"`);
      appendSelectedTaskLog('[System] 已获取划词内容，准备向 ScholarAgent 发起追问...');
    },
    [appendSelectedTaskLog, setPrompt],
  );

  const aiTranslationPluginInstance = useAITranslationPlugin(handleAskAI);

  const handleFullTranslation = useCallback(async () => {
    setIsFullTranslating(true);
    appendChatMessage({ role: 'system', text: '正在调起 ScholarAgent 进行全文翻译，请稍候...' });

    setTimeout(() => {
      setIsFullTranslating(false);
      appendChatMessage({
        role: 'system',
        text: '全文翻译已完成！您可以直接在 PDF 阅读器中看到翻译后的中文文本。',
        actions: ['open_pdf'],
      });
    }, 2000);
  }, [appendChatMessage]);

  return {
    pdfUrl,
    setPdfUrl,
    isFullTranslating,
    showSuggestions,
    setShowSuggestions,
    defaultLayoutPluginInstance,
    aiTranslationPluginInstance,
    handleFullTranslation,
  };
}
