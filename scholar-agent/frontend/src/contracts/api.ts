export type ChatAction =
  | 'open_pdf'
  | 'close_pdf'
  | 'translate_full'
  | 'view_plot'
  | 'view_report';

export interface ChatMessage {
  role: string;
  text: string;
  actions?: ChatAction[];
  taskId?: string;
}

export interface Task {
  ID: string;
  Name: string;
  Type?: string;
  Description: string;
  AssignedTo: string;
  Status: string;
  Dependencies: string[];
}

export interface GraphTask {
  id: string;
  name: string;
  type: string;
  description: string;
  assigned_to: string;
  status: string;
  dependencies: string[];
  required_artifacts: string[];
  output_artifacts: string[];
  parallelizable: boolean;
  result?: string;
  code?: string;
  image_base64?: string;
  image_base_64?: string;
  error?: string;
}

export interface GraphEdge {
  id: string;
  from: string;
  to: string;
  type: string;
}

export interface PlanGraph {
  id: string;
  user_intent: string;
  intent_type: string;
  status: string;
  nodes: GraphTask[];
  edges: GraphEdge[];
}

export interface IntentContext {
  raw_intent: string;
  intent_type: string;
  entities: Record<string, unknown>;
  constraints: Record<string, unknown>;
  metadata: Record<string, unknown>;
}

export interface PlanEvent {
  plan_id: string;
  event_type: string;
  task_id?: string;
  task_status?: string;
  payload?: Record<string, unknown>;
  timestamp: string;
}

export interface NodeExecutionState {
  logs: string;
  result: string;
  code: string;
  imageBase64?: string;
}

export interface ReproductionResourceProbe {
  cpu_count?: number;
  memory_gb?: number;
  disk_free_gb?: number;
  gpu_count?: number;
  gpu_names?: string[];
  thresholds?: Record<string, unknown>;
}

export interface ReproductionModeDecision {
  requested_mode?: string;
  effective_mode?: string;
  full_eligible?: boolean;
  reasons?: string[];
  probe?: ReproductionResourceProbe;
}

export interface PlanClarificationOption {
  id: string;
  label: string;
  description: string;
}

export interface PlanClarification {
  required?: boolean;
  type?: string;
  recommended_mode?: string;
  question: string;
  options?: PlanClarificationOption[];
  mode_decision?: ReproductionModeDecision;
  resource_probe?: ReproductionResourceProbe;
}

export interface PlanResponse {
  message: string;
  plan_graph: PlanGraph;
  clarification?: PlanClarification;
  intent_context?: IntentContext;
  session_id?: string;
  anon_user_id?: string;
  user_id?: string;
}

export interface ChatResponse {
  response: string;
  session_id?: string;
  anon_user_id?: string;
  user_id?: string;
}

export interface ExecutePlanResponse {
  message: string;
  plan_id: string;
}

export interface ExecuteTaskPayload {
  task_id: string;
  task_name: string;
  task_type?: string;
  task_description: string;
  assigned_to: string;
  inputs?: Record<string, unknown>;
}

export interface ExecuteTaskResultEvent {
  result?: string;
  code?: string;
  image_base64?: string;
  image_base_64?: string;
}
