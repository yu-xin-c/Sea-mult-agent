import { LogIn, MessageSquarePlus, UserRound } from 'lucide-react';

interface SessionSummary {
  id: string;
  title: string;
  updatedAt: string;
  messageCount: number;
}

interface SessionManagerState {
  isLoggedIn: boolean;
  userId: string | null;
  loginInput: string;
  activeSessionId: string | null;
  sessions: SessionSummary[];
  loading: boolean;
}

interface SessionManagerActions {
  setLoginInput: (value: string) => void;
  onLogin: () => void;
  onCreateSession: () => void;
  onSwitchSession: (sessionId: string) => void;
}

interface ChatSessionManagerProps {
  state: SessionManagerState;
  actions: SessionManagerActions;
}

const formatTime = (value: string) => {
  const time = new Date(value);
  if (Number.isNaN(time.getTime())) return '--';
  return time.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
};

export function ChatSessionManager(props: ChatSessionManagerProps) {
  const { state, actions } = props;

  return (
    <div className={`border-b border-gray-200 p-4 ${state.isLoggedIn ? 'bg-slate-50' : 'bg-gradient-to-r from-blue-50 to-slate-50'}`}>
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-semibold text-slate-700">
            <UserRound className="h-4 w-4 text-blue-600" />
            {state.isLoggedIn ? '当前用户' : '游客模式'}
          </div>
          <div className="mt-1 text-xs leading-5 text-slate-500">
            {state.isLoggedIn
              ? state.userId
              : '未登录也可直接聊天和切换临时 session；登录后才会持久保存会话记录。'}
          </div>
        </div>
        <button
          onClick={actions.onCreateSession}
          disabled={state.loading}
          className="inline-flex items-center gap-1 rounded-lg border border-blue-200 bg-white px-3 py-2 text-xs font-medium text-blue-600 transition hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <MessageSquarePlus className="h-4 w-4" />
          {state.isLoggedIn ? '新建 Session' : '新建临时 Session'}
        </button>
      </div>

      {!state.isLoggedIn && (
        <div className="mt-3 flex gap-2">
          <input
            value={state.loginInput}
            onChange={(event) => actions.setLoginInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                actions.onLogin();
              }
            }}
            placeholder="请输入用户 ID，例如 alice"
            className="flex-1 rounded-lg border border-blue-200 bg-white px-3 py-2 text-sm outline-none transition focus:border-blue-400 focus:ring-2 focus:ring-blue-200"
          />
          <button
            onClick={actions.onLogin}
            disabled={state.loading || !state.loginInput.trim()}
            className="inline-flex items-center gap-1 rounded-lg bg-blue-600 px-3 py-2 text-sm font-medium text-white transition hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            <LogIn className="h-4 w-4" />
            登录
          </button>
        </div>
      )}

      <div className="mt-3 space-y-2">
        {state.sessions.map((session) => {
          const isActive = session.id === state.activeSessionId;
          return (
            <button
              key={session.id}
              onClick={() => actions.onSwitchSession(session.id)}
              className={`w-full rounded-xl border px-3 py-2 text-left transition ${
                isActive ? 'border-blue-300 bg-blue-50 shadow-sm' : 'border-gray-200 bg-white hover:border-blue-200 hover:bg-slate-50'
              }`}
            >
              <div className="flex items-center justify-between gap-3">
                <div className="truncate text-sm font-medium text-slate-700">{session.title}</div>
                <div className="shrink-0 text-[11px] text-slate-400">{session.messageCount} 条</div>
              </div>
              <div className="mt-1 flex items-center justify-between gap-3 text-[11px] text-slate-400">
                <span className="truncate">{session.id}</span>
                <span className="shrink-0">{formatTime(session.updatedAt)}</span>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}
