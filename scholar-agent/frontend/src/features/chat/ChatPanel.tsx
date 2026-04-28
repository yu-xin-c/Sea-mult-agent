import type { ChatMessage } from '../../contracts/api';
import { ChatComposer } from './ChatComposer';
import { ChatMessageList } from './ChatMessageList';
import { ChatSessionManager } from './ChatSessionManager';

interface ChatPanelState {
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
}

interface ChatActions {
  setPrompt: (value: string) => void;
  setShowSuggestions: (next: boolean) => void;
  onSendMessage: () => void;
  setLoginInput: (value: string) => void;
  onLogin: () => void;
  onCreateSession: () => void;
  onSwitchSession: (sessionId: string) => void;
}

interface PdfActions {
  onOpenPdf: () => void;
  onClosePdf: () => void;
  onFullTranslation: () => void;
}

interface TaskActions {
  onOpenTaskView: (taskId: string, mode: 'plot' | 'report') => void;
}

interface ChatPanelProps {
  state: ChatPanelState;
  chatActions: ChatActions;
  pdfActions: PdfActions;
  taskActions: TaskActions;
}

export function ChatPanel(props: ChatPanelProps) {
  const { state, chatActions, pdfActions, taskActions } = props;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <ChatSessionManager
        state={{
          isLoggedIn: state.isLoggedIn,
          userId: state.userId,
          loginInput: state.loginInput,
          activeSessionId: state.activeSessionId,
          sessions: state.sessions,
          loading: state.loading,
        }}
        actions={{
          setLoginInput: chatActions.setLoginInput,
          onLogin: chatActions.onLogin,
          onCreateSession: chatActions.onCreateSession,
          onSwitchSession: chatActions.onSwitchSession,
        }}
      />
      <ChatMessageList
        chatHistory={state.chatHistory}
        loading={state.loading}
        isLoggedIn={state.isLoggedIn}
        pdfActions={pdfActions}
        taskActions={taskActions}
      />
      <ChatComposer
        prompt={state.prompt}
        loading={state.loading}
        isLoggedIn={state.isLoggedIn}
        showSuggestions={state.showSuggestions}
        setPrompt={chatActions.setPrompt}
        setShowSuggestions={chatActions.setShowSuggestions}
        onSendMessage={chatActions.onSendMessage}
      />
    </div>
  );
}
