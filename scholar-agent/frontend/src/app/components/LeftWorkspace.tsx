import type { ReactNode } from 'react';
import { Bot, X } from 'lucide-react';
import { ChatPanel } from '../../features/chat/ChatPanel';
import { PdfPanel } from '../../features/pdf-viewer/PdfPanel';
import type { ChatMessage } from '../../contracts/api';
import type { Plugin } from '@react-pdf-viewer/core';
import type { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';

interface LeftWorkspaceShellProps {
  widthPercent: number;
  showClosePdf: boolean;
  onClosePdf: () => void;
  children: ReactNode;
}

function LeftWorkspaceShell(props: LeftWorkspaceShellProps) {
  const { widthPercent, showClosePdf, onClosePdf, children } = props;

  return (
    <div
      style={{ width: `${widthPercent}%` }}
      className="flex flex-col bg-white border-r border-gray-200 shadow-xl z-10 flex-shrink-0 transition-all duration-300 relative"
    >
      <div className="p-4 border-b border-gray-200 bg-blue-600 text-white flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Bot className="w-6 h-6" />
          <h1 className="text-xl font-bold tracking-wide">ScholarAgent</h1>
        </div>
        {showClosePdf && (
          <button onClick={onClosePdf} className="text-blue-100 hover:text-white p-1 hover:bg-blue-500 rounded-full transition-colors">
            <X className="w-5 h-5" />
          </button>
        )}
      </div>
      {children}
    </div>
  );
}

interface LeftWorkspaceChatProps {
  widthPercent: number;
  state: {
    chatHistory: ChatMessage[];
    loading: boolean;
    prompt: string;
    showSuggestions: boolean;
    isLoggedIn: boolean;
    userId: string | null;
    loginInput: string;
    activeSessionId: string | null;
    sessions: Array<{
      id: string;
      title: string;
      createdAt: string;
      updatedAt: string;
      messageCount: number;
    }>;
  };
  chatActions: {
    setPrompt: (value: string) => void;
    setShowSuggestions: (next: boolean) => void;
    onSendMessage: () => void;
    setLoginInput: (value: string) => void;
    onLogin: () => void;
    onCreateSession: () => void;
    onSwitchSession: (sessionId: string) => void;
  };
  pdfActions: {
    onOpenPdf: () => void;
    onClosePdf: () => void;
    onFullTranslation: () => void;
  };
  taskActions: {
    onOpenTaskView: (taskId: string, mode: 'plot' | 'report') => void;
  };
}

function LeftWorkspaceChat(props: LeftWorkspaceChatProps) {
  const { widthPercent, state, chatActions, pdfActions, taskActions } = props;
  return (
    <LeftWorkspaceShell widthPercent={widthPercent} showClosePdf={false} onClosePdf={pdfActions.onClosePdf}>
      <ChatPanel state={state} chatActions={chatActions} pdfActions={pdfActions} taskActions={taskActions} />
    </LeftWorkspaceShell>
  );
}

interface LeftWorkspacePdfProps {
  widthPercent: number;
  pdfUrl: string;
  isFullTranslating: boolean;
  onClosePdf: () => void;
  onFullTranslation: () => void;
  defaultLayoutPluginInstance: ReturnType<typeof defaultLayoutPlugin>;
  aiTranslationPluginInstance: Plugin;
}

function LeftWorkspacePdf(props: LeftWorkspacePdfProps) {
  const { widthPercent, pdfUrl, isFullTranslating, onClosePdf, onFullTranslation, defaultLayoutPluginInstance, aiTranslationPluginInstance } = props;
  return (
    <LeftWorkspaceShell widthPercent={widthPercent} showClosePdf onClosePdf={onClosePdf}>
      <PdfPanel
        pdfUrl={pdfUrl}
        isFullTranslating={isFullTranslating}
        onFullTranslation={onFullTranslation}
        defaultLayoutPluginInstance={defaultLayoutPluginInstance}
        aiTranslationPluginInstance={aiTranslationPluginInstance}
      />
    </LeftWorkspaceShell>
  );
}

export const LeftWorkspace = {
  Chat: LeftWorkspaceChat,
  Pdf: LeftWorkspacePdf,
};
