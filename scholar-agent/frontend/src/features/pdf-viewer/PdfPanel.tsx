import { FileUp, Languages } from 'lucide-react';
import { Viewer, Worker } from '@react-pdf-viewer/core';
import type { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';
import type { Plugin } from '@react-pdf-viewer/core';

interface PdfPanelProps {
  pdfUrl: string;
  isFullTranslating: boolean;
  onFullTranslation: () => void;
  defaultLayoutPluginInstance: ReturnType<typeof defaultLayoutPlugin>;
  aiTranslationPluginInstance: Plugin;
}

export function PdfPanel(props: PdfPanelProps) {
  const {
    pdfUrl,
    isFullTranslating,
    onFullTranslation,
    defaultLayoutPluginInstance,
    aiTranslationPluginInstance,
  } = props;

  return (
    <div className="flex-1 overflow-hidden flex flex-col">
      <div className="bg-gray-100 p-2 text-xs text-gray-500 text-center border-b border-gray-200 flex justify-between items-center px-4">
        <span className="font-medium">正在阅读: Attention Is All You Need.pdf</span>
        <div className="flex items-center gap-2">
          <button
            onClick={onFullTranslation}
            disabled={isFullTranslating}
            className="flex items-center gap-1 text-blue-600 hover:text-blue-700 bg-white px-2 py-1 rounded border border-blue-200 shadow-sm transition-all active:scale-95"
          >
            <Languages className={`w-3 h-3 ${isFullTranslating ? 'animate-spin' : ''}`} />
            {isFullTranslating ? '翻译中...' : '全文翻译'}
          </button>
          <span className="flex items-center gap-1 text-gray-400">
            <FileUp className="w-3 h-3" /> 切换文档
          </span>
        </div>
      </div>
      <div className="flex-1 overflow-y-auto relative">
        <Worker workerUrl="https://unpkg.com/pdfjs-dist@3.11.174/build/pdf.worker.min.js">
          <div style={{ height: '100%', width: '100%' }}>
            <Viewer fileUrl={pdfUrl} plugins={[defaultLayoutPluginInstance, aiTranslationPluginInstance]} />
          </div>
        </Worker>
      </div>
    </div>
  );
}
