import type { ExecuteTaskResultEvent, PlanEvent } from './api';

export const PLAN_EVENTS = {
  TASK_READY: 'task_ready',
  TASK_STARTED: 'task_started',
  TASK_LOG: 'task_log',
  ARTIFACT_CREATED: 'artifact_created',
  TASK_BLOCKED: 'task_blocked',
  TASK_COMPLETED: 'task_completed',
  TASK_FAILED: 'task_failed',
  PLAN_COMPLETED: 'plan_completed',
  PLAN_FAILED: 'plan_failed',
} as const;

export const DIRECT_EXECUTION_EVENTS = {
  LOG: 'log',
  HEARTBEAT: 'heartbeat',
  RESULT: 'result',
  ERROR: 'error',
} as const;

export const PLAN_STREAM_EVENT_NAME = 'plan_event';

export const isPlanTerminalEvent = (event: PlanEvent): boolean =>
  event.event_type === PLAN_EVENTS.PLAN_COMPLETED || event.event_type === PLAN_EVENTS.PLAN_FAILED;

export const pickImageBase64 = (payload: ExecuteTaskResultEvent | Record<string, unknown> | undefined): string => {
  if (!payload) return '';
  const snake = payload.image_base64;
  const legacy = payload.image_base_64;
  return String(snake || legacy || '');
};
