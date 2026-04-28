import { useCallback, useEffect, useMemo, useState } from 'react';
import type { ChatMessage, IntentContext, PlanGraph } from '../../contracts/api';
import { chat, createPlan } from '../../services/api/scholarApi';
import { uiText } from '../constants/uiText';

const isTaskIntent = (input: string) => {
  const normalized = input.toLowerCase();
  return [
    /对比|比较|评估|选型|复现|执行|运行|画图|绘图|代码|论文|报告|总结|分析/,
    /rag|benchmark|plot|matplotlib|python|reproduce|summary|report/,
  ].some((pattern) => pattern.test(normalized));
};

interface UseScholarChatFlowOptions {
  onPlanGraphChanged: (planGraph: PlanGraph | null) => void;
}

interface ChatSessionRecord {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  chatHistory: ChatMessage[];
  activePlanId: string | null;
  intentContext: IntentContext | null;
  planGraph: PlanGraph | null;
}

interface PersistedChatState {
  userId: string | null;
  activeSessionId: string | null;
  sessions: ChatSessionRecord[];
}

interface SessionSummary {
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  messageCount: number;
}

const STORAGE_KEY = 'scholar-agent.chat-sessions.v1';

const createId = (prefix: string) => {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2, 10)}`;
};

const createWelcomeMessage = (): ChatMessage => ({
  role: 'system',
  text: uiText.appWelcome,
});

const createSessionRecord = (index: number): ChatSessionRecord => {
  const now = new Date().toISOString();
  return {
    id: createId('session'),
    title: `新会话 ${index}`,
    createdAt: now,
    updatedAt: now,
    chatHistory: [createWelcomeMessage()],
    activePlanId: null,
    intentContext: null,
    planGraph: null,
  };
};

const createSessionState = (userId: string | null, sessionTitlePrefix: string): PersistedChatState => {
  const firstSession = createSessionRecord(1);
  return {
    userId,
    activeSessionId: firstSession.id,
    sessions: [{ ...firstSession, title: `${sessionTitlePrefix} 1` }],
  };
};

const normalizeChatState = (raw: unknown): PersistedChatState => {
  if (!raw || typeof raw !== 'object') {
    return { userId: null, activeSessionId: null, sessions: [] };
  }

  const data = raw as Partial<PersistedChatState>;
  const sessions = Array.isArray(data.sessions)
    ? data.sessions.filter((session): session is ChatSessionRecord => Boolean(session?.id))
    : [];

  return {
    userId: typeof data.userId === 'string' && data.userId.trim() ? data.userId.trim() : null,
    activeSessionId: typeof data.activeSessionId === 'string' && data.activeSessionId.trim() ? data.activeSessionId.trim() : null,
    sessions,
  };
};

const loadPersistedChatState = (): PersistedChatState => {
  if (typeof window === 'undefined') {
    return createSessionState(null, '临时会话');
  }

  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return createSessionState(null, '临时会话');

    const normalized = normalizeChatState(JSON.parse(raw));
    if (!normalized.userId) {
      return createSessionState(null, '临时会话');
    }
    if (normalized.sessions.length === 0 || !normalized.activeSessionId) {
      return createSessionState(normalized.userId, '新会话');
    }
    return normalized;
  } catch {
    return createSessionState(null, '临时会话');
  }
};

const sanitizeUserId = (value: string) => value.trim().replace(/[^a-zA-Z0-9@._-]/g, '').slice(0, 128);

const deriveSessionTitle = (fallbackTitle: string, messages: ChatMessage[]): string => {
  const firstUserMessage = messages.find((message) => message.role === 'user' && message.text.trim());
  if (!firstUserMessage) return fallbackTitle;
  const compactText = firstUserMessage.text.replace(/\s+/g, ' ').trim();
  return compactText.length > 18 ? `${compactText.slice(0, 18)}...` : compactText;
};

const updateSessionRecord = (
  sessions: ChatSessionRecord[],
  sessionId: string,
  updater: (session: ChatSessionRecord) => ChatSessionRecord,
) => sessions.map((session) => (session.id === sessionId ? updater(session) : session));

export function useScholarChatFlow(options: UseScholarChatFlowOptions) {
  const { onPlanGraphChanged } = options;
  const [persistedState, setPersistedState] = useState<PersistedChatState>(() => loadPersistedChatState());
  const [prompt, setPrompt] = useState('');
  const [loading, setLoading] = useState(false);
  const [loginInput, setLoginInput] = useState(() => loadPersistedChatState().userId ?? '');
  const [guestUserId] = useState(() => createId('guest'));

  const activeSession = useMemo(
    () => persistedState.sessions.find((session) => session.id === persistedState.activeSessionId) ?? null,
    [persistedState.activeSessionId, persistedState.sessions],
  );

  const chatHistory = activeSession?.chatHistory ?? [];
  const activePlanId = activeSession?.activePlanId ?? null;
  const intentContext = activeSession?.intentContext ?? null;
  const sessionSummaries = useMemo<SessionSummary[]>(
    () =>
      [...persistedState.sessions]
        .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))
        .map((session) => ({
          id: session.id,
          title: session.title,
          createdAt: session.createdAt,
          updatedAt: session.updatedAt,
          messageCount: session.chatHistory.length,
        })),
    [persistedState.sessions],
  );

  useEffect(() => {
    if (typeof window === 'undefined') return;
    if (persistedState.userId) {
      window.localStorage.setItem(STORAGE_KEY, JSON.stringify(persistedState));
      return;
    }
    window.localStorage.removeItem(STORAGE_KEY);
  }, [persistedState]);

  useEffect(() => {
    onPlanGraphChanged(activeSession?.planGraph ?? null);
  }, [activeSession?.id, activeSession?.planGraph, onPlanGraphChanged]);

  const appendMessageToSession = useCallback((sessionId: string, message: ChatMessage) => {
    const now = new Date().toISOString();
    setPersistedState((prev) => ({
      ...prev,
      sessions: updateSessionRecord(prev.sessions, sessionId, (session) => {
        const nextChatHistory = [...session.chatHistory, message];
        return {
          ...session,
          chatHistory: nextChatHistory,
          updatedAt: now,
          title: deriveSessionTitle(session.title, nextChatHistory),
        };
      }),
    }));
  }, []);

  const appendChatMessage = useCallback(
    (message: ChatMessage) => {
      if (!persistedState.activeSessionId) return;
      appendMessageToSession(persistedState.activeSessionId, message);
    },
    [appendMessageToSession, persistedState.activeSessionId],
  );

  const handleLogin = useCallback(() => {
    const nextUserId = sanitizeUserId(loginInput);
    if (!nextUserId) return;

    setPersistedState((prev) => {
      if (prev.userId === nextUserId && prev.sessions.length > 0 && prev.activeSessionId) {
        return prev;
      }
      if (!prev.userId && prev.sessions.length > 0 && prev.activeSessionId) {
        return {
          ...prev,
          userId: nextUserId,
          sessions: prev.sessions.map((session, index) => ({
            ...session,
            title: session.title.startsWith('临时会话') ? `新会话 ${index + 1}` : session.title,
          })),
        };
      }
      return {
        ...createSessionState(nextUserId, '新会话'),
      };
    });
    setLoginInput(nextUserId);
    setPrompt('');
  }, [loginInput]);

  const handleCreateSession = useCallback(() => {
    const sessionTitlePrefix = persistedState.userId ? '新会话' : '临时会话';
    const nextSession = {
      ...createSessionRecord(persistedState.sessions.length + 1),
      title: `${sessionTitlePrefix} ${persistedState.sessions.length + 1}`,
    };
    setPersistedState((prev) => ({
      ...prev,
      activeSessionId: nextSession.id,
      sessions: [nextSession, ...prev.sessions],
    }));
    setPrompt('');
  }, [persistedState.sessions.length, persistedState.userId]);

  const handleSwitchSession = useCallback((sessionId: string) => {
    setPersistedState((prev) => ({
      ...prev,
      activeSessionId: sessionId,
    }));
    setPrompt('');
  }, []);

  const updateSessionPlanState = useCallback(
    (sessionId: string, payload: { activePlanId: string | null; intentContext: IntentContext | null; planGraph: PlanGraph | null }) => {
      const now = new Date().toISOString();
      setPersistedState((prev) => ({
        ...prev,
        sessions: updateSessionRecord(prev.sessions, sessionId, (session) => ({
          ...session,
          activePlanId: payload.activePlanId,
          intentContext: payload.intentContext,
          planGraph: payload.planGraph,
          updatedAt: now,
        })),
      }));
    },
    [],
  );

  const handleSendMessage = useCallback(async () => {
    if (!prompt.trim() || !persistedState.activeSessionId) return;

    const userPrompt = prompt.trim();
    const requestUserId = persistedState.userId ?? guestUserId;
    const requestSessionId = persistedState.activeSessionId;
    setLoading(true);
    appendMessageToSession(requestSessionId, { role: 'user', text: userPrompt });
    setPrompt('');

    const isTaskRequest = isTaskIntent(userPrompt);

    try {
      if (isTaskRequest) {
        const response = await createPlan(userPrompt, {
          userId: requestUserId,
          sessionId: requestSessionId,
        });
        const generatedPlanGraph = response.plan_graph;
        const returnedIntentContext = response.intent_context;
        if (!generatedPlanGraph) throw new Error('Backend did not return plan_graph');

        // 计划数据跟随 session 保存，切回历史 session 时可以还原右侧图谱。
        updateSessionPlanState(requestSessionId, {
          intentContext: returnedIntentContext ?? null,
          activePlanId: generatedPlanGraph.id,
          planGraph: generatedPlanGraph,
        });
        appendMessageToSession(requestSessionId, {
          role: 'system',
          text: uiText.planGenerated,
          actions: ['open_pdf', 'translate_full', 'close_pdf'],
        });
      } else {
        const response = await chat(userPrompt, {
          userId: requestUserId,
          sessionId: requestSessionId,
        });
        appendMessageToSession(requestSessionId, { role: 'system', text: response.response });
      }
    } catch (error) {
      console.error(error);
      appendMessageToSession(requestSessionId, { role: 'system', text: uiText.backendError });
    } finally {
      setLoading(false);
    }
  }, [appendMessageToSession, guestUserId, persistedState.activeSessionId, persistedState.userId, prompt, updateSessionPlanState]);

  return {
    prompt,
    setPrompt,
    loading,
    chatHistory,
    activePlanId,
    intentContext,
    appendChatMessage,
    isLoggedIn: Boolean(persistedState.userId),
    userId: persistedState.userId,
    loginInput,
    setLoginInput,
    activeSessionId: persistedState.activeSessionId,
    sessionSummaries,
    handleLogin,
    handleCreateSession,
    handleSwitchSession,
    handleSendMessage,
  };
}
