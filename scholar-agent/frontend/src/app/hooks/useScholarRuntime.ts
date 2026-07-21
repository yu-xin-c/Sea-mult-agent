import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type { Node } from '@xyflow/react';
import type { ChatMessage, ExecuteTaskResultEvent, NodeExecutionState, Task } from '../../contracts/api';
import { PLAN_EVENTS, isPlanTerminalEvent, pickImageBase64 } from '../../contracts/events';
import { approvePlan, cancelPlan, createPlanEventSource, executePlan, executeTaskStream, retryTask } from '../../services/api/scholarApi';
import type { RequestIdentity } from '../../services/api/scholarApi';
import { getTaskStyleByStatus } from '../../features/shared/agentVisuals';
import { createTaskNodeLabel } from '../../features/plan-graph/nodeLabelFactory';
import { uiText } from '../constants/uiText';

export type ExecutionDisplayMode =
  | 'logs'
  | 'report'
  | 'code'
  | 'plot'
  | 'evidence'
  | 'report-expanded'
  | 'plot-expanded'
  | 'evidence-expanded';

interface ExecutionState {
  selectedTask: Task | null;
  nodeStates: Record<string, NodeExecutionState>;
  displayMode: ExecutionDisplayMode;
  isExecuting: boolean;
	approvalResolved: boolean;
}

type ExecutionAction =
  | { type: 'select-task'; task: Task; state?: NodeExecutionState }
  | { type: 'close-task' }
  | { type: 'patch-task-state'; taskId: string; updater: (prev: NodeExecutionState) => NodeExecutionState }
  | { type: 'set-display-mode'; mode: ExecutionDisplayMode }
  | { type: 'set-executing'; value: boolean }
	| { type: 'set-approval-resolved'; value: boolean }
  | { type: 'reset' };

const initialNodeExecutionState: NodeExecutionState = { logs: '', result: '', code: '', structuredData: '', imageBase64: '' };
const reportAgents = new Set(['librarian_agent', 'data_agent', 'research_coding_agent']);
const isReportAgent = (assignedTo: string) => reportAgents.has(assignedTo);

const updateNodeStatus = (node: Node, status: string): Node => {
	const task = node.data.task as Task;
	if (!task) return node;
	const updatedTask = { ...task, Status: status };
	return {
		...node,
		data: {
			...node.data,
			status,
			task: updatedTask,
			label: createTaskNodeLabel({
				assignedTo: updatedTask.AssignedTo,
				taskName: updatedTask.Name,
				status,
				step: typeof node.data.step === 'number' ? node.data.step : undefined,
			}),
		},
		style: {
			...(node.style || {}),
			...getTaskStyleByStatus(status),
		},
	};
};

const detectBestDisplayMode = (task: Task, state: NodeExecutionState): ExecutionDisplayMode => {
  if (task.Type === 'claim_evidence_build' && state.structuredData) return 'evidence';
  if (state.imageBase64) return 'plot';
  if (state.code && !state.result) return 'code';
  if (state.result && isReportAgent(task.AssignedTo)) return 'report';
  return 'logs';
};

const executionStateFromTask = (task: Task): NodeExecutionState => ({
  logs: '',
  result: task.Result || '',
  code: task.Code || '',
  structuredData: task.StructuredData || '',
  imageBase64: task.ImageBase64 || '',
});

const looksLikePythonCode = (value: string): boolean => {
  const text = value.trim();
  if (!text) return false;
  return /\b(import|from|def|class|print|if __name__)\b/.test(text);
};

const PARAM_PREVIEW_LIMIT = 1200;

const previewParameterValue = (key: string, value: unknown): string => {
  if (value === undefined || value === null) return '';
  const keyLower = key.toLowerCase();
  if (typeof value === 'string' && (keyLower.includes('image') || keyLower.includes('base64'))) {
    return `<base64 image, ${value.length} chars>`;
  }

  const raw = typeof value === 'string' ? value : JSON.stringify(value, null, 2);
  if (!raw) return '';
  return raw.length > PARAM_PREVIEW_LIMIT ? `${raw.slice(0, PARAM_PREVIEW_LIMIT)}...[truncated]` : raw;
};

const formatParameterObject = (title: string, values: Record<string, unknown>): string => {
  const entries = Object.entries(values).filter(([, value]) => value !== undefined && value !== null && String(value).trim() !== '');
  if (entries.length === 0) return `${title}: 无`;
  return [
    `${title}:`,
    ...entries.map(([key, value]) => `- ${key}: ${previewParameterValue(key, value)}`),
  ].join('\n');
};

const formatArtifactSummaries = (title: string, rawArtifacts: unknown): string => {
  if (!Array.isArray(rawArtifacts) || rawArtifacts.length === 0) return `${title}: 无`;
  const lines = rawArtifacts.map((item) => {
    const artifact = item as Record<string, unknown>;
    const key = String(artifact.key || 'unknown');
    const type = artifact.type ? ` (${String(artifact.type)})` : '';
    const producer = artifact.producer_task_id ? ` <- ${String(artifact.producer_task_id)}` : '';
    return `- ${key}${type}${producer}: ${previewParameterValue(key, artifact.value_preview || '')}`;
  });
  return [`${title}:`, ...lines].join('\n');
};

const buildDirectExecutionOutputs = (result: string, code: string, structuredData: string, imageBase64: string): Record<string, unknown> => {
  const outputs: Record<string, unknown> = {};
  if (code) outputs.generated_code = code;
  if (result) outputs.result = result;
  if (structuredData) outputs.structured_data = structuredData;
  if (imageBase64) outputs.image_base64 = imageBase64;
  return outputs;
};

const executionReducer = (state: ExecutionState, action: ExecutionAction): ExecutionState => {
  switch (action.type) {
    case 'select-task': {
      const taskState = action.state ?? executionStateFromTask(action.task);
      return {
        ...state,
        selectedTask: action.task,
        displayMode: detectBestDisplayMode(action.task, taskState),
      };
    }
    case 'close-task':
      return {
        ...state,
        selectedTask: null,
        displayMode: 'logs',
      };
    case 'patch-task-state': {
      const prevTaskState = state.nodeStates[action.taskId] ?? initialNodeExecutionState;
      return {
        ...state,
        nodeStates: {
          ...state.nodeStates,
          [action.taskId]: action.updater(prevTaskState),
        },
      };
    }
    case 'set-display-mode':
      return { ...state, displayMode: action.mode };
    case 'set-executing':
      return { ...state, isExecuting: action.value };
	case 'set-approval-resolved':
	  return { ...state, approvalResolved: action.value };
    case 'reset':
      return {
        selectedTask: null,
        nodeStates: {},
        displayMode: 'logs',
        isExecuting: false,
		approvalResolved: false,
      };
    default:
      return state;
  }
};

interface UseScholarRuntimeOptions {
  nodes: Node[];
  setNodes: Dispatch<SetStateAction<Node[]>>;
  appendChatMessage: (message: ChatMessage) => void;
	identity: RequestIdentity;
}

export function useScholarRuntime(options: UseScholarRuntimeOptions) {
	const { nodes, setNodes, appendChatMessage, identity } = options;
  const planEventSourceRef = useRef<EventSource | null>(null);
  const [executionState, dispatchExecution] = useReducer(executionReducer, {
    selectedTask: null,
    nodeStates: {},
    displayMode: 'logs',
    isExecuting: false,
	approvalResolved: false,
  });

  const selectedTaskState = useMemo(() => {
    if (!executionState.selectedTask) return initialNodeExecutionState;
    return executionState.nodeStates[executionState.selectedTask.ID] ?? executionStateFromTask(executionState.selectedTask);
  }, [executionState.selectedTask, executionState.nodeStates]);

  const patchNodeState = useCallback((taskId: string, updater: (prev: NodeExecutionState) => NodeExecutionState) => {
    dispatchExecution({ type: 'patch-task-state', taskId, updater });
  }, []);

  const buildDirectExecutionInputs = useCallback(
    (task: Task): Record<string, unknown> => {
      const inputs: Record<string, unknown> = { ...(task.Inputs ?? {}) };
      for (const dependencyId of task.Dependencies || []) {
        const upstream = executionState.nodeStates[dependencyId];
        if (!upstream) continue;

        if (upstream.code && !inputs.generated_code) inputs.generated_code = upstream.code;
        if (upstream.result) {
          inputs[`dependency_${dependencyId}_result`] = upstream.result;
          if (!inputs.generated_code && looksLikePythonCode(upstream.result)) inputs.generated_code = upstream.result;
        }
        if (upstream.imageBase64) inputs[`dependency_${dependencyId}_image_base64`] = upstream.imageBase64;
        if (upstream.structuredData) {
          inputs[`dependency_${dependencyId}_structured_data`] = upstream.structuredData;
          const upstreamTask = nodes.find((node) => node.id === dependencyId)?.data.task as Task | undefined;
          for (const artifactKey of upstreamTask?.OutputArtifacts || []) {
            if (artifactKey === 'claim_rubric' || artifactKey === 'claim_evidence_graph') {
              inputs[artifactKey] = upstream.structuredData;
            }
          }
        }
      }
      return inputs;
    },
    [executionState.nodeStates, nodes],
  );

  const appendNodeLog = useCallback(
    (taskId: string, line: string) => {
      patchNodeState(taskId, (prev) => ({
        ...prev,
        logs: prev.logs ? `${prev.logs}\n${line}` : line,
      }));
    },
    [patchNodeState],
  );

  const updateNodeVisualState = useCallback(
    (taskId: string, status: string) => {
      setNodes((nds) =>
		nds.map((node) => (node.id === taskId ? updateNodeStatus(node, status) : node)),
      );
    },
    [setNodes],
  );

  const connectPlanStream = useCallback(
    (planId: string) => {
      if (planEventSourceRef.current) {
        planEventSourceRef.current.close();
      }

      const source = createPlanEventSource(planId, {
        onPlanEvent: (event) => {
          if (event.task_id && event.task_status) {
            updateNodeVisualState(event.task_id, event.task_status);
          }

          if (event.event_type === PLAN_EVENTS.TASK_READY && event.task_id) {
            appendNodeLog(event.task_id, `[Plan] ready\n${formatArtifactSummaries('[Plan Params] 本次上游传入参数', event.payload?.inputs)}`);
          }
          if (event.event_type === PLAN_EVENTS.TASK_STARTED && event.task_id) {
            appendNodeLog(event.task_id, '[Plan] started');
          }
          if (event.event_type === PLAN_EVENTS.TASK_LOG && event.task_id) {
            appendNodeLog(event.task_id, String(event.payload?.message || ''));
          }
          if (event.event_type === PLAN_EVENTS.ARTIFACT_CREATED && event.task_id) {
            const keys = Array.isArray(event.payload?.artifact_keys) ? event.payload?.artifact_keys?.join(', ') : '';
            appendNodeLog(
              event.task_id,
              `[Plan] artifacts created${keys ? `: ${keys}` : ''}\n${formatArtifactSummaries('[Plan Params] 将传递给下游的参数', event.payload?.artifacts)}`,
            );
          }
          if (event.event_type === PLAN_EVENTS.TASK_BLOCKED && event.task_id) {
            const upstream = String(event.payload?.upstream_task_id || '');
            appendNodeLog(event.task_id, `[Plan] blocked${upstream ? ` by ${upstream}` : ''}`);
          }
          if (event.event_type === PLAN_EVENTS.TASK_COMPLETED && event.task_id) {
            patchNodeState(event.task_id, (prev) => ({
              ...prev,
              logs: prev.logs ? `${prev.logs}\n[Plan] task_completed` : '[Plan] task_completed',
              result: String(event.payload?.result || event.payload?.result_summary || prev.result || ''),
              code: String(event.payload?.code || prev.code || ''),
              structuredData: String(event.payload?.structured_data || prev.structuredData || ''),
              imageBase64: pickImageBase64(event.payload) || prev.imageBase64 || '',
            }));
          }
          if (event.event_type === PLAN_EVENTS.TASK_FAILED && event.task_id) {
            const errorText = String(event.payload?.error || 'Task failed');
            patchNodeState(event.task_id, (prev) => ({
              ...prev,
              logs: prev.logs ? `${prev.logs}\n[Plan Error] ${errorText}` : `[Plan Error] ${errorText}`,
            }));
          }

          if (isPlanTerminalEvent(event)) {
			if (event.event_type === PLAN_EVENTS.PLAN_CANCELED) {
				setNodes((current) => current.map((node) => updateNodeStatus(node, 'canceled')));
			}
            source.close();
            planEventSourceRef.current = null;
            dispatchExecution({ type: 'set-executing', value: false });
            appendChatMessage({
              role: 'system',
              text:
                event.event_type === PLAN_EVENTS.PLAN_COMPLETED
                  ? '整张拓扑图已经执行完成。'
                  : `计划执行失败：${String(event.payload?.error || event.payload?.reason || '未知错误')}`,
            });
          }
        },
        onError: () => {
          source.close();
          if (planEventSourceRef.current === source) {
            planEventSourceRef.current = null;
          }
        },
      });

      planEventSourceRef.current = source;
    },
	[appendChatMessage, appendNodeLog, patchNodeState, setNodes, updateNodeVisualState],
  );

  const handleExecuteTask = useCallback(
    async (task: Task) => {
      dispatchExecution({ type: 'set-executing', value: true });
      dispatchExecution({
        type: 'select-task',
        task,
        state: executionState.nodeStates[task.ID],
      });
      dispatchExecution({ type: 'set-display-mode', mode: 'logs' });

      const directInputs = buildDirectExecutionInputs(task);
      const initLog = `[System] 正在唤醒 ${task.AssignedTo}...\n[System] 正在通过 Eino 框架调用 DeepSeek 模型${
        isReportAgent(task.AssignedTo) ? '生成结构化结果' : '生成代码'
      }...\n\n${formatParameterObject('[Params] 本次传入参数', directInputs)}\n`;
      patchNodeState(task.ID, () => ({ logs: initLog, result: '', code: '', structuredData: '', imageBase64: '' }));

      try {
        await executeTaskStream(
          {
            task_id: task.ID,
            task_name: task.Name,
            task_type: task.Type,
            assigned_to: task.AssignedTo,
            inputs: directInputs,
            task_description:
              task.Description +
              (task.AssignedTo === 'coder_agent' || task.AssignedTo === 'sandbox_agent'
                ? '\n\n(提示: 请务必输出一段可执行的完整 Python 代码，完成上述任务目标)'
                : ''),
          },
          {
            onLog: (line) => {
              patchNodeState(task.ID, (prev) => ({
                ...prev,
                logs: prev.logs ? `${prev.logs}\n${line}` : line,
              }));
            },
            onResult: (rawData) => {
              let finalResult = rawData;
              let generatedCode = '';
              let structuredData = '';
              let imageBase64 = '';

              try {
                const parsed = JSON.parse(rawData) as ExecuteTaskResultEvent;
                if (parsed?.result) finalResult = parsed.result;
                if (parsed?.code) generatedCode = parsed.code;
                if (parsed?.structured_data) structuredData = parsed.structured_data;
                imageBase64 = pickImageBase64(parsed);
              } catch {
                // keep raw fallback
              }

              const directOutputs = buildDirectExecutionOutputs(finalResult, generatedCode, structuredData, imageBase64);

              patchNodeState(task.ID, (prev) => ({
                logs: `${prev.logs || ''}\n\n${formatParameterObject('[Params] 将传递给下游的参数', directOutputs)}\n\n[🎉 Agent 思考与执行完毕]`,
                result: finalResult,
                code: generatedCode,
                structuredData,
                imageBase64,
              }));

              const taskActions: ('view_plot' | 'view_report')[] = [];
              if (imageBase64) taskActions.push('view_plot');
              if (finalResult && isReportAgent(task.AssignedTo)) {
                taskActions.push('view_report');
              }

              appendChatMessage({
                role: 'system',
                taskId: task.ID,
                text:
                  `✅ 节点 **[${task.Name}]** 执行完成！\n\n您可以点击下方的快捷按钮或右侧节点查看完整结果。` +
                  (generatedCode
                    ? `\n\n**生成的代码片段:**\n\`\`\`python\n${generatedCode.substring(0, 300)}${
                        generatedCode.length > 300 ? '\n... (代码较长，请在右侧面板查看完整代码)' : ''
                      }\n\`\`\``
                    : ''),
                actions: taskActions.length > 0 ? taskActions : undefined,
              });

              if (task.Type === 'claim_evidence_build' && structuredData) {
                dispatchExecution({ type: 'set-display-mode', mode: 'evidence' });
              } else if (imageBase64) dispatchExecution({ type: 'set-display-mode', mode: 'plot' });
              else if (isReportAgent(task.AssignedTo)) {
                dispatchExecution({ type: 'set-display-mode', mode: 'report' });
              }

              updateNodeVisualState(task.ID, 'completed');
            },
            onError: (errorMessage) => {
              throw new Error(errorMessage);
            },
          },
        );
      } catch (error: unknown) {
        console.error(error);
        const message = error instanceof Error ? error.message : String(error);
        const errorMsg =
          message === 'Failed to fetch'
            ? '哎呀，与后端失联了 📡！可能是大模型思考太久导致连接超时，或者您的本地端口被占用了，请重试一下～'
            : message;

        patchNodeState(task.ID, (prev) => ({
          ...prev,
          logs: `${prev.logs}\n\n[❌ 执行中断] ${errorMsg}`,
        }));
        appendChatMessage({
          role: 'system',
          text: `❌ 节点 **[${task.Name}]** 执行失败。\n\n**错误信息:**\n${errorMsg}`,
        });
        updateNodeVisualState(task.ID, 'failed');
        throw error;
      } finally {
        dispatchExecution({ type: 'set-executing', value: false });
      }
    },
    [appendChatMessage, buildDirectExecutionInputs, executionState.nodeStates, patchNodeState, updateNodeVisualState],
  );

  const handleRunAllTasks = useCallback(
    async (activePlanId: string | null) => {
      if (executionState.isExecuting) return;
      if (!activePlanId) {
        appendChatMessage({ role: 'system', text: uiText.noPlanMessage });
        return;
      }

      dispatchExecution({ type: 'set-executing', value: true });
      appendChatMessage({ role: 'system', text: uiText.planStartMessage });

      try {
		await executePlan(activePlanId, identity);
        connectPlanStream(activePlanId);
      } catch (error) {
        console.error(error);
        dispatchExecution({ type: 'set-executing', value: false });
        appendChatMessage({ role: 'system', text: uiText.planStartFailedMessage });
      }
    },
	[appendChatMessage, connectPlanStream, executionState.isExecuting, identity],
  );

	const handleApproveAndRun = useCallback(
		async (activePlanId: string | null) => {
			if (!activePlanId || executionState.isExecuting) return;
			dispatchExecution({ type: 'set-executing', value: true });
			try {
				await approvePlan(activePlanId, identity);
				dispatchExecution({ type: 'set-approval-resolved', value: true });
				await executePlan(activePlanId, identity);
				appendChatMessage({ role: 'system', text: '计划已审批，开始执行。' });
				connectPlanStream(activePlanId);
			} catch (error) {
				console.error(error);
				dispatchExecution({ type: 'set-executing', value: false });
				appendChatMessage({ role: 'system', text: '计划审批或启动失败，请检查运行状态。' });
			}
		},
		[appendChatMessage, connectPlanStream, executionState.isExecuting, identity],
	);

	const handleCancelPlan = useCallback(
		async (activePlanId: string | null) => {
			if (!activePlanId) return;
			try {
				await cancelPlan(activePlanId, identity);
				planEventSourceRef.current?.close();
				planEventSourceRef.current = null;
				setNodes((current) => current.map((node) => updateNodeStatus(node, 'canceled')));
				dispatchExecution({ type: 'set-executing', value: false });
				appendChatMessage({ role: 'system', text: '计划已取消，未完成任务已停止。' });
			} catch (error) {
				console.error(error);
				appendChatMessage({ role: 'system', text: '取消计划失败，请刷新计划状态后重试。' });
			}
		},
		[appendChatMessage, identity, setNodes],
	);

	const handleRetryFailedPlan = useCallback(
		async (activePlanId: string | null) => {
			if (!activePlanId || executionState.isExecuting) return;
			const failedNode = nodes.find((node) => node.data.status === 'failed');
			if (!failedNode) return;
			dispatchExecution({ type: 'set-executing', value: true });
			try {
				await retryTask(activePlanId, failedNode.id, identity);
				setNodes((current) =>
					current.map((node) =>
						['failed', 'blocked', 'canceled'].includes(String(node.data.status))
							? updateNodeStatus(node, 'pending')
							: node,
					),
				);
				await executePlan(activePlanId, identity);
				appendChatMessage({ role: 'system', text: '失败节点已重置，计划开始重试。' });
				connectPlanStream(activePlanId);
			} catch (error) {
				console.error(error);
				dispatchExecution({ type: 'set-executing', value: false });
				appendChatMessage({ role: 'system', text: '重试计划失败，请检查节点状态。' });
			}
		},
		[appendChatMessage, connectPlanStream, executionState.isExecuting, identity, nodes, setNodes],
	);

  const onNodeClick = useCallback(
    (_: unknown, node: Node) => {
      const taskData = node.data.task as Task;
      if (!taskData) return;
      dispatchExecution({
        type: 'select-task',
        task: taskData,
        state: executionState.nodeStates[taskData.ID],
      });
    },
    [executionState.nodeStates],
  );

  const handleOpenTaskView = useCallback(
    (taskId: string, mode: 'plot' | 'report') => {
      const targetNode = nodes.find((n) => n.id === taskId);
      if (!targetNode) return;
      onNodeClick(null, targetNode);
      dispatchExecution({ type: 'set-display-mode', mode });
    },
    [nodes, onNodeClick],
  );

  const appendSelectedTaskLog = useCallback(
    (line: string) => {
      if (!executionState.selectedTask) return;
      appendNodeLog(executionState.selectedTask.ID, line);
    },
    [appendNodeLog, executionState.selectedTask],
  );

  useEffect(
    () => () => {
      if (planEventSourceRef.current) {
        planEventSourceRef.current.close();
      }
    },
    [],
  );

  const setDisplayMode = useCallback(
    (mode: ExecutionDisplayMode) => {
      dispatchExecution({ type: 'set-display-mode', mode });
    },
    [],
  );

  const closeTaskPanel = useCallback(() => {
    dispatchExecution({ type: 'close-task' });
  }, []);

  const resetRuntimeState = useCallback(() => {
    dispatchExecution({ type: 'reset' });
  }, []);

  return {
    executionState,
    selectedTaskState,
    onNodeClick,
    handleOpenTaskView,
    handleExecuteTask,
    handleRunAllTasks,
	handleApproveAndRun,
	handleCancelPlan,
	handleRetryFailedPlan,
    appendSelectedTaskLog,
    setDisplayMode,
    closeTaskPanel,
    resetRuntimeState,
  };
}
