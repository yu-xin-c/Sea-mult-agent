import { API_BASE_URL } from '../../config/env';
import type {
  ChatResponse,
  ExecutePlanResponse,
  ExecuteTaskPayload,
  PlanEvent,
  PlanResponse,
} from '../../contracts/api';
import { DIRECT_EXECUTION_EVENTS, PLAN_STREAM_EVENT_NAME } from '../../contracts/events';
import { httpClient } from './httpClient';

export interface RequestIdentity {
  userId: string;
  sessionId: string;
}

const buildIdentityHeaders = (identity: RequestIdentity) => ({
  'X-User-Id': identity.userId,
  'X-Session-Id': identity.sessionId,
});

export const createPlan = async (intent: string, identity: RequestIdentity): Promise<PlanResponse> => {
  const response = await httpClient.post<PlanResponse>('/api/plan', { intent }, { headers: buildIdentityHeaders(identity) });
  return response.data;
};

export const chat = async (message: string, identity: RequestIdentity): Promise<ChatResponse> => {
  const response = await httpClient.post<ChatResponse>('/api/chat', { message }, { headers: buildIdentityHeaders(identity) });
  return response.data;
};

export const executePlan = async (planId: string): Promise<ExecutePlanResponse> => {
  const response = await httpClient.post<ExecutePlanResponse>(`/api/plans/${planId}/execute`, {});
  return response.data;
};

export const getPdfProxyUrl = (url: string): string =>
  `${API_BASE_URL}/api/pdf-proxy?url=${encodeURIComponent(url)}`;

export const createPlanEventSource = (
  planId: string,
  handlers: {
    onPlanEvent: (event: PlanEvent) => void;
    onError?: () => void;
  },
): EventSource => {
  const source = new EventSource(`${API_BASE_URL}/api/plans/${planId}/stream`);
  source.addEventListener(PLAN_STREAM_EVENT_NAME, (evt) => {
    const parsed = JSON.parse((evt as MessageEvent).data) as PlanEvent;
    handlers.onPlanEvent(parsed);
  });
  if (handlers.onError) {
    source.onerror = handlers.onError;
  }
  return source;
};

const parseSseChunk = (chunk: string): { type: string; data: string }[] => {
  const events = chunk.split('\n\n').filter(Boolean);
  return events.map((event) => {
    const lines = event.split('\n');
    let type = 'message';
    let data = '';
    for (const line of lines) {
      if (line.startsWith('event:')) type = line.substring(6).trim();
      if (line.startsWith('data:')) data += line.substring(5).trim();
    }
    return { type, data };
  });
};

export const executeTaskStream = async (
  payload: ExecuteTaskPayload,
  handlers: {
    onLog: (line: string) => void;
    onResult: (rawData: string) => void;
    onError: (errorMessage: string) => void;
  },
): Promise<void> => {
  const response = await fetch(`${API_BASE_URL}/api/execute`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });

  if (!response.ok) {
    throw new Error(`[HTTP ${response.status}] 哎呀，服务器好像开小差了，请稍后再试一次吧～`);
  }

  if (!response.body) {
    throw new Error('您的浏览器版本可能有点老，不支持流式传输呢 😅');
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder('utf-8');
  let buffer = '';

  while (true) {
    let readResult: ReadableStreamReadResult<Uint8Array>;
    try {
      readResult = await reader.read();
    } catch {
      throw new Error('网络连接突然断开了 🔌... 可能是大模型正在深度思考，导致连接超时了，建议刷新页面重试～');
    }

    const { value, done } = readResult;
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const splitIndex = buffer.lastIndexOf('\n\n');
    if (splitIndex < 0) continue;

    const completeChunk = buffer.slice(0, splitIndex);
    buffer = buffer.slice(splitIndex + 2);
    const parsedEvents = parseSseChunk(completeChunk);

    for (const event of parsedEvents) {
      if (event.type === DIRECT_EXECUTION_EVENTS.LOG) {
        handlers.onLog(event.data);
      } else if (event.type === DIRECT_EXECUTION_EVENTS.RESULT) {
        handlers.onResult(event.data);
      } else if (event.type === DIRECT_EXECUTION_EVENTS.ERROR) {
        handlers.onError(event.data);
      }
    }
  }
};
