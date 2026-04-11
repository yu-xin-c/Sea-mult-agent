import { useState, useRef, useEffect, useCallback } from 'react';
import { Background, Controls, ReactFlow, useNodesState, useEdgesState, Panel } from '@xyflow/react';
import type { Node, Edge } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { Send, Bot, FileText, Code, Database, TerminalSquare, Play, X, Eye, FileUp, Maximize2, Languages, Loader2, Sparkles, ChevronDown, ChevronUp } from 'lucide-react';
import axios from 'axios';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import 'katex/dist/katex.min.css';

// PDF Viewer Imports
import { Viewer, Worker } from '@react-pdf-viewer/core';
import { defaultLayoutPlugin } from '@react-pdf-viewer/default-layout';
import '@react-pdf-viewer/core/lib/styles/index.css';
import '@react-pdf-viewer/default-layout/lib/styles/index.css';

// Custom Hooks
import { useAITranslationPlugin } from './hooks/useAITranslationPlugin';

// --- 类型定义 ---
interface ChatMessage {
  role: string;
  text: string;
  actions?: ('open_pdf' | 'close_pdf' | 'translate_full' | 'view_plot' | 'view_report')[];
  taskId?: string;
}

interface Task {
  ID: string;
  Name: string;
  Type?: string;
  Description: string;
  AssignedTo: string;
  Status: string;
  Dependencies: string[];
}

interface GraphTask {
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
  error?: string;
}

interface GraphEdge {
  id: string;
  from: string;
  to: string;
  type: string;
}

interface PlanGraph {
  id: string;
  user_intent: string;
  intent_type: string;
  status: string;
  nodes: GraphTask[];
  edges: GraphEdge[];
}

interface IntentContext {
  raw_intent: string;
  intent_type: string;
  entities: Record<string, unknown>;
  constraints: Record<string, unknown>;
  metadata: Record<string, unknown>;
}

interface PlanEvent {
  plan_id: string;
  event_type: string;
  task_id?: string;
  task_status?: string;
  payload?: Record<string, unknown>;
  timestamp: string;
}

interface NodeExecutionState {
  logs: string;
  result: string;
  code: string;
  imageBase64?: string;
}

// --- Agent 图标映射 ---
const getAgentIcon = (agentName: string) => {
  switch (agentName) {
    case 'librarian_agent': return <FileText className="w-5 h-5 text-blue-500" />;
    case 'coder_agent': return <Code className="w-5 h-5 text-purple-500" />;
    case 'sandbox_agent': return <TerminalSquare className="w-5 h-5 text-orange-500" />;
    case 'data_agent': return <Database className="w-5 h-5 text-green-500" />;
    default: return <Bot className="w-5 h-5 text-gray-500" />;
  }
};

// --- 主应用组件 ---
const isTaskIntent = (input: string) => {
  const normalized = input.toLowerCase();
  return [
    /对比|比较|评估|选型|复现|执行|运行|画图|绘图|代码|论文|报告|总结|分析/,
    /rag|benchmark|plot|matplotlib|python|reproduce|summary|report/,
  ].some((pattern) => pattern.test(normalized));
};

const stringifyEntity = (value: unknown) => {
  if (Array.isArray(value)) return value.join(', ');
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (value == null || value === '') return '-';
  return String(value);
};

const uiText = {
  appWelcome: '你好，我是 ScholarAgent 科研助手。你可以直接让我做规划、复现实验、代码执行、论文总结或框架对比。',
  planGenerated: '我已经生成新的规划拓扑图，右侧也会展示 intent_context，方便你核对 planner 是否真的理解了问题。',
  backendError: '后端请求失败，请确认 Go 服务正在 :8080 端口运行。',
  graphTitle: '多智能体执行计划 (DAG)',
  graphHint: '点击节点可查看详情并触发真实执行',
  runAll: '一键运行所有节点',
  suggestionsTitle: '试试这些任务',
  inputPlaceholder: '例如：对比 LangChain 和 LlamaIndex，做一个 RAG 框架选型。',
};

export default function App() {
  const [prompt, setPrompt] = useState('');
  const [loading, setLoading] = useState(false);
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [activePlanId, setActivePlanId] = useState<string | null>(null);
  const [intentContext, setIntentContext] = useState<IntentContext | null>(null);
  const [chatHistory, setChatHistory] = useState<ChatMessage[]>([
    { role: 'system', text: uiText.appWelcome }
  ]);
  
  // Resizable Panels State
  const [leftPanelWidth, setLeftPanelWidth] = useState(35); // 默认 35%
  const [isResizing, setIsResizing] = useState(false);
  
  const [sidebarWidth, setSidebarWidth] = useState(450); // 默认 450px
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);
  
  // 全文翻译状态
  const [isFullTranslating, setIsFullTranslating] = useState(false);

  // 新增状态：保存各个节点的执行状态（日志、结果、代码、图表），避免关闭侧边栏后丢失
  const [nodeStates, setNodeStates] = useState<Record<string, NodeExecutionState>>({});

  const [selectedTask, setSelectedTask] = useState<Task | null>(null);
  const [executionLogs, setExecutionLogs] = useState<string>('');
  const [executionResult, setExecutionResult] = useState<string>('');
  const [executionCode, setExecutionCode] = useState<string>('');
  const [executionImage, setExecutionImage] = useState<string>('');
  const [viewMode, setViewMode] = useState<'logs' | 'report' | 'code' | 'plot'>('logs');
  const [isExecuting, setIsExecuting] = useState(false);
  const logsEndRef = useRef<HTMLDivElement>(null);
  
  // PDF 相关状态
  const [pdfUrl, setPdfUrl] = useState<string | null>(null);
  const defaultLayoutPluginInstance = defaultLayoutPlugin();
  
  // 报告放大状态
  const [isReportExpanded, setIsReportExpanded] = useState(false);
  const [isPlotExpanded, setIsPlotExpanded] = useState(false);
  const [showSuggestions, setShowSuggestions] = useState(true);
  const planEventSourceRef = useRef<EventSource | null>(null);

  // 实例化 AI 翻译插件
  const handleAskAI = useCallback((selectedText: string) => {
    setPrompt(`请帮我详细解释这篇文献中的这段内容：\n"${selectedText}"`);
    if (pdfUrl) {
      setExecutionLogs(prev => prev + `\n\n[System] 已获取划词内容，准备向 ScholarAgent 发起追问...`);
    }
  }, [pdfUrl]);

  const aiTranslationPluginInstance = useAITranslationPlugin(handleAskAI);

const graphTaskToTask = (task: GraphTask): Task => ({
  ID: task.id,
  Name: task.name,
  Type: task.type,
  Description: task.description,
  AssignedTo: task.assigned_to,
  Status: task.status,
    Dependencies: task.dependencies ?? [],
  });

  const getTaskStyleByStatus = (status?: string) => {
    switch (status) {
      case 'ready':
        return { borderColor: '#3b82f6', backgroundColor: '#eff6ff' };
      case 'in_progress':
        return { borderColor: '#f59e0b', backgroundColor: '#fffbeb' };
      case 'completed':
        return { borderColor: '#22c55e', backgroundColor: '#f0fdf4' };
      case 'failed':
        return { borderColor: '#ef4444', backgroundColor: '#fef2f2' };
      case 'blocked':
        return { borderColor: '#6b7280', backgroundColor: '#f3f4f6' };
      default:
        return { borderColor: '#e5e7eb', backgroundColor: '#ffffff' };
    }
  };

  const updateNodeVisualState = (taskId: string, status: string) => {
    setNodes(nds => nds.map(n => {
      if (n.id !== taskId) return n;
      const task = n.data.task as Task;
      const updatedTask = { ...task, Status: status };
      const styleState = getTaskStyleByStatus(status);
      return {
        ...n,
        data: {
          ...n.data,
          status,
          task: updatedTask,
          label: (
            <div className="flex flex-col gap-2 p-2 w-56">
              <div className="flex items-center justify-between border-b pb-2">
                <div className="flex items-center gap-2">
                  {getAgentIcon(updatedTask.AssignedTo)}
                  <span className="font-semibold text-xs text-gray-700">{updatedTask.AssignedTo}</span>
                </div>
              </div>
              <div className="text-sm text-gray-800 text-left font-medium">{updatedTask.Name}</div>
              <div className="text-xs text-gray-400 capitalize text-left">状态: {status}</div>
            </div>
          )
        },
        style: {
          ...(n.style || {}),
          ...styleState,
        }
      };
    }));

    setSelectedTask(prev => prev && prev.ID === taskId ? { ...prev, Status: status } : prev);
  };

  const patchNodeState = (taskId: string, updater: (prev: NodeExecutionState) => NodeExecutionState) => {
    setNodeStates(prev => {
      const current = prev[taskId] || { logs: '', result: '', code: '', imageBase64: '' };
      const next = updater(current);

      if (selectedTask?.ID === taskId) {
        setExecutionLogs(next.logs);
        setExecutionResult(next.result);
        setExecutionCode(next.code);
        setExecutionImage(next.imageBase64 || '');
      }

      return {
        ...prev,
        [taskId]: next,
      };
    });
  };

  const appendNodeLog = (taskId: string, line: string) => {
    patchNodeState(taskId, (prev) => ({
      ...prev,
      logs: prev.logs ? `${prev.logs}\n${line}` : line,
    }));
  };

  const connectPlanStream = (planId: string) => {
    if (planEventSourceRef.current) {
      planEventSourceRef.current.close();
    }

    const source = new EventSource(`http://localhost:8080/api/plans/${planId}/stream`);
    planEventSourceRef.current = source;

    source.addEventListener('plan_event', (evt) => {
      const event = JSON.parse((evt as MessageEvent).data) as PlanEvent;

      if (event.task_id && event.task_status) {
        updateNodeVisualState(event.task_id, event.task_status);
      }

      if (event.event_type === 'task_ready' && event.task_id) {
        appendNodeLog(event.task_id, `[Plan] ready`);
      }

      if (event.event_type === 'task_started' && event.task_id) {
        appendNodeLog(event.task_id, `[Plan] started`);
      }

      if (event.event_type === 'task_log' && event.task_id) {
        appendNodeLog(event.task_id, String(event.payload?.message || ''));
      }

      if (event.event_type === 'artifact_created' && event.task_id) {
        const keys = Array.isArray(event.payload?.artifact_keys) ? event.payload?.artifact_keys?.join(', ') : '';
        appendNodeLog(event.task_id, `[Plan] artifacts created${keys ? `: ${keys}` : ''}`);
      }

      if (event.event_type === 'task_blocked' && event.task_id) {
        const upstream = String(event.payload?.upstream_task_id || '');
        appendNodeLog(event.task_id, `[Plan] blocked${upstream ? ` by ${upstream}` : ''}`);
      }

      if (event.event_type === 'task_completed' && event.task_id) {
        patchNodeState(event.task_id, (prev) => ({
          ...prev,
          logs: prev.logs ? `${prev.logs}\n[Plan] task_completed` : '[Plan] task_completed',
          result: String(event.payload?.result || event.payload?.result_summary || prev.result || ''),
          code: String(event.payload?.code || prev.code || ''),
          imageBase64: String(event.payload?.image_base64 || prev.imageBase64 || ''),
        }));
      }

      if (event.event_type === 'task_failed' && event.task_id) {
        const errorText = String(event.payload?.error || 'Task failed');
        patchNodeState(event.task_id, (prev) => ({
          ...prev,
          logs: prev.logs ? `${prev.logs}\n[Plan Error] ${errorText}` : `[Plan Error] ${errorText}`,
        }));
      }

      if (event.event_type === 'plan_completed' || event.event_type === 'plan_failed') {
        source.close();
        planEventSourceRef.current = null;
        setIsExecuting(false);
        setChatHistory(prev => [...prev, {
          role: 'system',
          text: event.event_type === 'plan_completed'
            ? '整张拓扑图已经执行完成。'
            : `计划执行失败：${String(event.payload?.error || event.payload?.reason || '未知错误')}`
        }]);
      }
    });

    source.onerror = () => {
      source.close();
      if (planEventSourceRef.current === source) {
        planEventSourceRef.current = null;
      }
    };
  };

  // 自动滚动日志到底部
  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [executionLogs]);

  useEffect(() => {
    return () => {
      if (planEventSourceRef.current) {
        planEventSourceRef.current.close();
      }
    };
  }, []);

  // Handle Resize Events
  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      if (isResizing) {
        const newWidth = (e.clientX / window.innerWidth) * 100;
        if (newWidth > 20 && newWidth < 80) {
          setLeftPanelWidth(newWidth);
        }
      } else if (isResizingSidebar) {
        const newSidebarWidth = window.innerWidth - e.clientX;
        if (newSidebarWidth > 300 && newSidebarWidth < window.innerWidth * 0.6) {
          setSidebarWidth(newSidebarWidth);
        }
      }
    };

    const handleMouseUp = () => {
      setIsResizing(false);
      setIsResizingSidebar(false);
      document.body.style.cursor = 'default';
    };

    if (isResizing || isResizingSidebar) {
      window.addEventListener('mousemove', handleMouseMove);
      window.addEventListener('mouseup', handleMouseUp);
      document.body.style.cursor = 'col-resize';
    }

    return () => {
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isResizing, isResizingSidebar]);

  // 处理全文翻译
  const handleFullTranslation = async () => {
    setIsFullTranslating(true);
    setChatHistory(prev => [...prev, { role: 'system', text: '正在调起 ScholarAgent 进行全文翻译，请稍候...' }]);
    
    // 模拟翻译过程
    setTimeout(() => {
      setIsFullTranslating(false);
      setChatHistory(prev => [...prev, { 
        role: 'system', 
        text: '全文翻译已完成！您可以直接在 PDF 阅读器中看到翻译后的中文文本。',
        actions: ['open_pdf']
      }]);
    }, 2000);
  };

  // 处理发送请求 (智能识别：问答 vs 任务编排)
  const handleSendMessage = async () => {
    if (!prompt.trim()) return;
    
    const userPrompt = prompt.trim();
    setLoading(true);
    setChatHistory(prev => [...prev, { role: 'user', text: userPrompt }]);
    setPrompt(''); // 清空输入框
    
    // 智能判断意图：是否包含任务触发关键词
    const isTaskRequest = isTaskIntent(userPrompt);
    
    try {
      if (isTaskRequest) {
        // 1. 任务编排逻辑 (Plan)
        const response = await axios.post('http://localhost:8080/api/plan', {
          intent: userPrompt
        });

        const generatedPlanGraph = response.data.plan_graph as PlanGraph | undefined;
        const returnedIntentContext = response.data.intent_context as IntentContext | undefined;
        if (!generatedPlanGraph) {
          throw new Error('Backend did not return plan_graph');
        }
        setIntentContext(returnedIntentContext ?? null);
        setActivePlanId(generatedPlanGraph.id);
        renderGraphDAG(generatedPlanGraph);
        
        setChatHistory(prev => [...prev, { 
          role: 'system', 
          text: uiText.planGenerated,
          actions: ['open_pdf', 'translate_full', 'close_pdf']
        }]);
      } else {
        // 2. 简单问答逻辑 (Chat)
        setIntentContext(null);
        const response = await axios.post('http://localhost:8080/api/chat', {
          message: userPrompt
        });

        setChatHistory(prev => [...prev, { 
          role: 'system', 
          text: response.data.response
        }]);
      }
      
    } catch (error) {
      console.error(error);
      setChatHistory(prev => [...prev, { role: 'system', text: uiText.backendError }]);
    } finally {
      setLoading(false);
    }
  };

  // 触发真实的 Agent 执行 (调用 DeepSeek + 沙箱)
  const handleExecuteTask = async (task: Task) => {
    return new Promise<void>(async (resolve, reject) => {
      setIsExecuting(true);
      setSelectedTask(task); // 自动选中当前正在执行的任务，方便查看日志
      setViewMode('logs');
      setExecutionResult('');
      setExecutionCode('');
      setExecutionImage('');
      
      const initLog = `[System] 正在唤醒 ${task.AssignedTo}...\n[System] 正在通过 Eino 框架调用 DeepSeek 模型${task.AssignedTo === 'librarian_agent' || task.AssignedTo === 'data_agent' ? '生成报告' : '生成代码'}...\n`;
      setExecutionLogs(initLog);
      setNodeStates(prev => ({
        ...prev,
        [task.ID]: { logs: initLog, result: '', code: '', imageBase64: '' }
      }));
      
      try {
        const response = await fetch('http://localhost:8080/api/execute', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({
            task_id: task.ID,
            task_name: task.Name,
            task_type: task.Type,
            assigned_to: task.AssignedTo,
            task_description: task.Description + (task.AssignedTo === 'coder_agent' || task.AssignedTo === 'sandbox_agent' ? "\n\n(提示: 请务必输出一段可执行的完整 Python 代码，完成上述任务目标)" : "")
          })
        });

        if (!response.ok) {
          throw new Error(`[HTTP ${response.status}] 哎呀，服务器好像开小差了，请稍后再试一次吧～`);
        }
        
        if (!response.body) throw new Error('您的浏览器版本可能有点老，不支持流式传输呢 😅');

        const reader = response.body.getReader();
        const decoder = new TextDecoder('utf-8');
        let buffer = '';

        while (true) {
          let readResult;
          try {
            readResult = await reader.read();
          } catch (e) {
            throw new Error('网络连接突然断开了 🔌... 可能是大模型正在深度思考，导致连接超时了，建议刷新页面重试～');
          }
          
          const { value, done } = readResult;
          if (done) break;
          
          buffer += decoder.decode(value, { stream: true });
          const events = buffer.split('\n\n');
          buffer = events.pop() || ''; // keep the last incomplete event in buffer

          for (const event of events) {
            const lines = event.split('\n');
            let eventType = 'message';
            let eventData = '';

            for (const line of lines) {
              if (line.startsWith('event:')) {
                eventType = line.substring(6).trim();
              } else if (line.startsWith('data:')) {
                eventData += line.substring(5).trim();
              }
            }

            if (eventType === 'log') {
              setExecutionLogs(prev => prev + '\n' + eventData);
              setNodeStates(prev => ({
                ...prev,
                [task.ID]: { ...prev[task.ID], logs: (prev[task.ID]?.logs || '') + '\n' + eventData }
              }));
            } else if (eventType === 'heartbeat') {
              // 默默收到心跳，保持连接
              console.log('💓 Heartbeat received');
            } else if (eventType === 'result') {
              let finalResult = eventData;
              let generatedCode = '';
              let imageBase64 = '';
              try { 
                const parsed = JSON.parse(eventData); 
                if (parsed && parsed.result) finalResult = parsed.result;
                if (parsed && parsed.code) generatedCode = parsed.code;
                if (parsed && parsed.image_base_64) imageBase64 = parsed.image_base_64;
              } catch (e) {} 
              
              const completionMsg = `\n\n[🎉 Agent 思考与执行完毕]`;
              setExecutionLogs(prev => prev + completionMsg);
              setExecutionResult(finalResult);
              setExecutionCode(generatedCode);
              setExecutionImage(imageBase64);
              
              setNodeStates(prev => ({
                ...prev,
                [task.ID]: { 
                  logs: (prev[task.ID]?.logs || '') + completionMsg, 
                  result: finalResult, 
                  code: generatedCode,
                  imageBase64: imageBase64
                }
              }));
              
              // 将执行结果推送到左侧对话框
              const taskActions: ('view_plot' | 'view_report')[] = [];
              if (imageBase64) taskActions.push('view_plot');
              if (finalResult && (task.AssignedTo === 'librarian_agent' || task.AssignedTo === 'data_agent')) {
                taskActions.push('view_report');
              }

              setChatHistory(prev => [...prev, {
                role: 'system',
                taskId: task.ID,
                text: `✅ 节点 **[${task.Name}]** 执行完成！\n\n您可以点击下方的快捷按钮或右侧节点查看完整结果。` + (generatedCode ? '\n\n**生成的代码片段:**\n```python\n' + generatedCode.substring(0, 300) + (generatedCode.length > 300 ? '\n... (代码较长，请在右侧面板查看完整代码)' : '') + '\n```' : ''),
                actions: taskActions.length > 0 ? taskActions : undefined
              }]);
              
              // 如果生成了图表，自动切换到图表视图
              if (imageBase64) {
                setViewMode('plot');
              } else if (task.AssignedTo === 'librarian_agent' || task.AssignedTo === 'data_agent') {
                setViewMode('report');
              }
              
              // 更新节点状态为已完成
              setNodes(nds => nds.map(n => {
                if (n.id === task.ID) {
                  return {
                    ...n,
                    style: { ...n.style, borderColor: '#22c55e', backgroundColor: '#f0fdf4' }, // 绿色边框
                    data: { ...n.data, status: 'completed' }
                  };
                }
                return n;
              }));
            } else if (eventType === 'error') {
              throw new Error(eventData);
            }
          }
        }
        resolve(); // 完成

      } catch (error: any) {
        console.error(error);
        const errorMsg = error.message === 'Failed to fetch' 
          ? '哎呀，与后端失联了 📡！可能是大模型思考太久导致连接超时，或者您的本地端口被占用了，请重试一下～' 
          : error.message;
        setExecutionLogs(prev => prev + `\n\n[❌ 执行中断] ${errorMsg}`);
        
        setNodeStates(prev => ({
          ...prev,
          [task.ID]: { ...prev[task.ID], logs: (prev[task.ID]?.logs || '') + `\n\n[❌ 执行中断] ${errorMsg}` }
        }));
        
        // 推送错误信息到左侧
        setChatHistory(prev => [...prev, {
          role: 'system',
          text: `❌ 节点 **[${task.Name}]** 执行失败。\n\n**错误信息:**\n${errorMsg}`
        }]);
        
        // 更新节点状态为失败
        setNodes(nds => nds.map(n => {
          if (n.id === task.ID) {
            return {
              ...n,
              style: { ...n.style, borderColor: '#ef4444', backgroundColor: '#fef2f2' }, // 红色边框
            };
          }
          return n;
        }));
        reject(error);
      } finally {
        setIsExecuting(false);
      }
    });
  };

  // 一键运行所有任务
  const handleRunAllTasks = async () => {
    if (isExecuting) return;
    if (activePlanId) {
      setIsExecuting(true);
      setChatHistory(prev => [...prev, {
        role: 'system',
        text: '正在启动整张拓扑图执行，并订阅计划级状态流。'
      }]);

      try {
        await axios.post(`http://localhost:8080/api/plans/${activePlanId}/execute`, {});
        connectPlanStream(activePlanId);
      } catch (error) {
        console.error(error);
        setIsExecuting(false);
        setChatHistory(prev => [...prev, {
          role: 'system',
          text: '启动整图执行失败，请确认后端计划接口已经启动。'
        }]);
      }
      return;
    }
    setChatHistory(prev => [...prev, {
      role: 'system',
      text: '当前前端已经切换到 plan_graph 主流程，无法回退旧任务列表执行链路。请重新生成计划后再执行。'
    }]);
    return;
    
    // 找出所有未完成的任务，按节点在数组中的顺序依次执行 (Planner 生成的顺序通常是合理的)
    const taskNodes = nodes.filter(n => n.data.task && n.data.status !== 'completed');
    
    setChatHistory(prev => [...prev, { 
      role: 'system', 
      text: `🚀 开始全自动流水线任务！共需执行 ${taskNodes.length} 个节点，请耐心等待。` 
    }]);

    for (const node of taskNodes) {
      const task = node.data.task as Task;
      try {
        await handleExecuteTask(task);
      } catch (e) {
        console.error(`Task ${task.Name} failed:`, e);
        // 如果中间有一个失败了，可以选择停止
        setChatHistory(prev => [...prev, { 
          role: 'system', 
          text: `⚠️ 流水线在节点 **[${task.Name}]** 处中断。后续节点已取消自动执行。` 
        }]);
        break;
      }
    }
    
    setChatHistory(prev => [...prev, { 
      role: 'system', 
      text: `🏁 全自动流水线执行完毕！` 
    }]);
  };

  // 节点点击事件
  const onNodeClick = (_: any, node: Node) => {
    const taskData = node.data.task as Task;
    if (taskData) {
      setSelectedTask(taskData);
      
      // 恢复之前的执行状态
      const savedState = nodeStates[taskData.ID] || { logs: '', result: '', code: '', imageBase64: '' };
      setExecutionLogs(savedState.logs);
      setExecutionResult(savedState.result);
      setExecutionCode(savedState.code);
      setExecutionImage(savedState.imageBase64 || '');
      
      // 根据情况智能切换默认视图
      if (savedState.imageBase64) {
        setViewMode('plot');
      } else if (savedState.code && !savedState.result) {
        setViewMode('code');
      } else if (savedState.result && (taskData.AssignedTo === 'librarian_agent' || taskData.AssignedTo === 'data_agent')) {
        setViewMode('report');
      } else {
        setViewMode('logs');
      }
    }
  };

  // 渲染有向无环图 (DAG)
  const renderDAG = (_legacyPlan: unknown) => {
    return;
    /*
    const newNodes: Node[] = [];
    const newEdges: Edge[] = [];
    
    let yOffset = 50;
    const taskArray = Object.values(plan.Tasks);
    
    const sortedTasks = [...taskArray].sort((a, b) => {
      if (a.Dependencies.includes(b.ID)) return 1;
      if (b.Dependencies.includes(a.ID)) return -1;
      return 0;
    });

    sortedTasks.forEach((task, index) => {
      const xOffset = 280;
      const x = 50 + (task.Dependencies.length * xOffset);
      const y = yOffset + (index * 120);

      newNodes.push({
        id: task.ID,
        position: { x, y: y - (task.Dependencies.length * 100) },
        data: { 
          task: task, // 存储完整 task 数据供点击使用
          label: (
            <div className="flex flex-col gap-2 p-2 w-56">
              <div className="flex items-center justify-between border-b pb-2">
                <div className="flex items-center gap-2">
                  {getAgentIcon(task.AssignedTo)}
                  <span className="font-semibold text-xs text-gray-700">{task.AssignedTo}</span>
                </div>
              </div>
              <div className="text-sm text-gray-800 text-left font-medium">{task.Name}</div>
              <div className="text-xs text-gray-400 capitalize text-left">状态: {task.Status}</div>
            </div>
          )
        },
        style: {
          borderRadius: '8px',
          backgroundColor: 'white',
          border: '2px solid',
          borderColor: task.Status === 'pending' ? '#e5e7eb' : '#3b82f6',
          boxShadow: '0 4px 6px -1px rgb(0 0 0 / 0.1)',
          cursor: 'pointer',
        }
      });

      task.Dependencies.forEach(depId => {
        newEdges.push({
          id: `e-${depId}-${task.ID}`,
          source: depId,
          target: task.ID,
          animated: true,
          style: { stroke: '#94a3b8', strokeWidth: 2 }
        });
      });
    });

    setNodes(newNodes);
    setEdges(newEdges);
    */
  };
  void renderDAG;

  const renderGraphDAG = (planGraph: PlanGraph) => {
    const newNodes: Node[] = [];
    const newEdges: Edge[] = [];

    const levelMap: Record<string, number> = {};
    const laneOrder = ['librarian_agent', 'coder_agent', 'sandbox_agent', 'data_agent', 'general_agent'];
    const laneOffsets: Record<string, number> = {
      librarian_agent: 40,
      coder_agent: 180,
      sandbox_agent: 320,
      data_agent: 460,
      general_agent: 600,
    };
    const tasksById = Object.fromEntries(planGraph.nodes.map((task) => [task.id, task]));

    const resolveLevel = (task: GraphTask): number => {
      if (typeof levelMap[task.id] === 'number') return levelMap[task.id];
      if (!task.dependencies.length) {
        levelMap[task.id] = 0;
        return 0;
      }

      const level = Math.max(...task.dependencies.map((depId) => {
        const dep = tasksById[depId];
        return dep ? resolveLevel(dep) + 1 : 1;
      }));
      levelMap[task.id] = level;
      return level;
    };

    const sortedTasks = [...planGraph.nodes].sort((a, b) => {
      const levelDiff = resolveLevel(a) - resolveLevel(b);
      if (levelDiff !== 0) return levelDiff;
      return laneOrder.indexOf(a.assigned_to) - laneOrder.indexOf(b.assigned_to);
    });

    const levelCounts: Record<string, number> = {};
    sortedTasks.forEach((task) => {
      const level = resolveLevel(task);
      const laneKey = task.assigned_to in laneOffsets ? task.assigned_to : 'general_agent';
      const bucketKey = `${laneKey}-${level}`;
      const stackIndex = levelCounts[bucketKey] || 0;
      levelCounts[bucketKey] = stackIndex + 1;
      const legacyTask = graphTaskToTask(task);
      const styleState = getTaskStyleByStatus(task.status);

      newNodes.push({
        id: task.id,
        position: {
          x: 80 + (level * 320),
          y: laneOffsets[laneKey] + (stackIndex * 110),
        },
        data: {
          task: legacyTask,
          status: task.status,
          label: (
            <div className="flex flex-col gap-2 p-2 w-56">
              <div className="flex items-center justify-between border-b pb-2">
                <div className="flex items-center gap-2">
                  {getAgentIcon(task.assigned_to)}
                  <span className="font-semibold text-xs text-gray-700">{task.assigned_to}</span>
                </div>
                <span className="text-[10px] uppercase tracking-wide text-gray-400">L{level + 1}</span>
              </div>
              <div className="text-sm text-gray-800 text-left font-medium">{task.name}</div>
              <div className="text-xs text-gray-400 capitalize text-left">状态: {task.status}</div>
            </div>
          )
        },
        style: {
          borderRadius: '8px',
          backgroundColor: styleState.backgroundColor,
          border: '2px solid',
          borderColor: styleState.borderColor,
          boxShadow: '0 4px 6px -1px rgb(0 0 0 / 0.1)',
          cursor: 'pointer',
        }
      });
    });

    planGraph.edges.forEach((edge) => {
      newEdges.push({
        id: edge.id,
        source: edge.from,
        target: edge.to,
        animated: edge.type === 'control',
        style: {
          stroke: edge.type === 'data' ? '#c084fc' : '#94a3b8',
          strokeWidth: edge.type === 'data' ? 1.5 : 2.5,
          strokeDasharray: edge.type === 'data' ? '6 4' : undefined
        },
        label: edge.type === 'data' ? 'data' : undefined
      });
    });

    setNodes(newNodes);
    setEdges(newEdges);
  };

  return (
    <div className="flex h-screen bg-gray-100 font-sans overflow-hidden">
      {/* 左侧面板: 聊天交互与 PDF 阅读区 */}
      <div 
        style={{ width: `${leftPanelWidth}%` }}
        className="flex flex-col bg-white border-r border-gray-200 shadow-xl z-10 flex-shrink-0 transition-all duration-300 relative"
      >
        <div className="p-4 border-b border-gray-200 bg-blue-600 text-white flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Bot className="w-6 h-6" />
            <h1 className="text-xl font-bold tracking-wide">ScholarAgent</h1>
          </div>
          {pdfUrl && (
            <button onClick={() => setPdfUrl(null)} className="text-blue-100 hover:text-white p-1 hover:bg-blue-500 rounded-full transition-colors">
              <X className="w-5 h-5" />
            </button>
          )}
        </div>
        
        {pdfUrl ? (
          // PDF 阅读器视图
          <div className="flex-1 overflow-hidden flex flex-col">
            <div className="bg-gray-100 p-2 text-xs text-gray-500 text-center border-b border-gray-200 flex justify-between items-center px-4">
              <span className="font-medium">正在阅读: Attention Is All You Need.pdf</span>
              <div className="flex items-center gap-2">
                <button 
                  onClick={handleFullTranslation}
                  disabled={isFullTranslating}
                  className="flex items-center gap-1 text-blue-600 hover:text-blue-700 bg-white px-2 py-1 rounded border border-blue-200 shadow-sm transition-all active:scale-95"
                >
                  <Languages className={`w-3 h-3 ${isFullTranslating ? 'animate-spin' : ''}`} />
                  {isFullTranslating ? '翻译中...' : '全文翻译'}
                </button>
                <span className="flex items-center gap-1 text-gray-400"><FileUp className="w-3 h-3"/> 切换文档</span>
              </div>
            </div>
            <div className="flex-1 overflow-y-auto relative">
              <Worker workerUrl="https://unpkg.com/pdfjs-dist@3.11.174/build/pdf.worker.min.js">
                <div style={{ height: '100%', width: '100%' }}>
                  <Viewer
                    fileUrl={pdfUrl}
                    plugins={[
                      defaultLayoutPluginInstance,
                      aiTranslationPluginInstance
                    ]}
                  />
                </div>
              </Worker>
            </div>
          </div>
        ) : (
          // 聊天视图
          <>
            <div className="flex-1 overflow-y-auto p-4 space-y-4">
              {chatHistory.map((msg, i) => (
                <div key={i} className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                  <div className={`max-w-[85%] flex flex-col gap-2 ${msg.role === 'user' ? 'items-end' : 'items-start'}`}>
                    <div className={`rounded-2xl px-4 py-3 shadow-sm ${
                      msg.role === 'user' 
                        ? 'bg-blue-600 text-white rounded-br-none' 
                        : 'bg-gray-100 text-gray-800 rounded-bl-none'
                    }`}>
                      {msg.role === 'user' ? (
                        msg.text
                      ) : (
                        <div className="prose prose-sm prose-slate max-w-none prose-p:leading-snug prose-pre:my-1 prose-pre:bg-gray-800 prose-pre:text-gray-100 prose-code:text-blue-600 prose-code:bg-blue-50 prose-code:px-1 prose-code:rounded">
                          <ReactMarkdown remarkPlugins={[remarkGfm]}>
                            {msg.text}
                          </ReactMarkdown>
                        </div>
                      )}
                    </div>
                    
                    {/* 快捷操作按钮 */}
                    {msg.actions && msg.actions.length > 0 && (
                      <div className="flex gap-2 mt-1 animate-in fade-in slide-in-from-top-1 duration-300">
                        {msg.actions.includes('open_pdf') && (
                          <button 
                            onClick={() => setPdfUrl('http://localhost:8080/api/pdf-proxy?url=https%3A%2F%2Farxiv.org%2Fpdf%2F1706.03762.pdf')}
                            className="flex items-center gap-1 text-xs bg-white text-blue-600 border border-blue-200 px-3 py-1.5 rounded-full hover:bg-blue-50 shadow-sm transition-all active:scale-95"
                          >
                            <FileText className="w-3 h-3" />
                            打开论文原文
                          </button>
                        )}
                        {msg.actions.includes('translate_full') && (
                          <button 
                            onClick={handleFullTranslation}
                            className="flex items-center gap-1 text-xs bg-white text-purple-600 border border-purple-200 px-3 py-1.5 rounded-full hover:bg-purple-50 shadow-sm transition-all active:scale-95"
                          >
                            <Languages className="w-3 h-3" />
                            全文翻译
                          </button>
                        )}
                        {msg.actions.includes('close_pdf') && pdfUrl && (
                          <button 
                            onClick={() => setPdfUrl(null)}
                            className="flex items-center gap-1 text-xs bg-white text-gray-600 border border-gray-200 px-3 py-1.5 rounded-full hover:bg-gray-50 shadow-sm transition-all active:scale-95"
                          >
                            <X className="w-3 h-3" />
                            关闭阅读器
                          </button>
                        )}
                        {msg.actions.includes('view_plot') && msg.taskId && (
                          <button 
                            onClick={() => {
                              const targetNode = nodes.find(n => n.id === msg.taskId);
                              if (targetNode) onNodeClick(null, targetNode);
                              setViewMode('plot');
                            }}
                            className="flex items-center gap-1 text-xs bg-white text-orange-600 border border-orange-200 px-3 py-1.5 rounded-full hover:bg-orange-50 shadow-sm transition-all active:scale-95"
                          >
                            <Maximize2 className="w-3 h-3" />
                            查看生成的图表
                          </button>
                        )}
                        {msg.actions.includes('view_report') && msg.taskId && (
                          <button 
                            onClick={() => {
                              const targetNode = nodes.find(n => n.id === msg.taskId);
                              if (targetNode) onNodeClick(null, targetNode);
                              setViewMode('report');
                            }}
                            className="flex items-center gap-1 text-xs bg-white text-green-600 border border-green-200 px-3 py-1.5 rounded-full hover:bg-green-50 shadow-sm transition-all active:scale-95"
                          >
                            <FileText className="w-3 h-3" />
                            查看分析报告
                          </button>
                        )}
                      </div>
                    )}
                  </div>
                </div>
              ))}
              {loading && (
                <div className="flex justify-start">
                  <div className="bg-gray-100 rounded-2xl rounded-bl-none px-4 py-3 text-gray-500 animate-pulse flex items-center gap-2">
                    <Bot className="w-4 h-4" />
                    正在使用 Planner 编排多智能体任务拓扑图...
                  </div>
                </div>
              )}
            </div>

            <div className="p-4 bg-white border-t border-gray-200">
              {/* 试试：提示词推荐标题与切换开关 */}
              <div className="flex items-center justify-between mb-2 px-1">
                <button 
                  onClick={() => setShowSuggestions(!showSuggestions)}
                  className="flex items-center gap-1.5 text-[10px] font-bold text-gray-400 uppercase tracking-widest hover:text-blue-500 transition-colors group"
                >
                  <Sparkles className={`w-3 h-3 ${showSuggestions ? 'text-blue-500' : 'text-gray-400'} group-hover:animate-pulse`} />
                  试试推荐指令
                  {showSuggestions ? <ChevronDown className="w-3 h-3" /> : <ChevronUp className="w-3 h-3" />}
                </button>
              </div>

              {/* 推荐列表内容 */}
              {showSuggestions && (
                <div className="flex flex-wrap gap-2 mb-4 animate-in slide-in-from-bottom-2 duration-300">
                  {[
                    "帮我画一个正弦函数和余弦函数的对比图",
                    "复现一下 Transformer 论文的核心架构并跑通测试",
                    "对比一下 LangChain 和 LlamaIndex 的 RAG 性能",
                    "分析一下这篇论文的主要创新点和局限性",
                    "帮我复现 Attention Is All You Need 论文的代码"
                  ].map((text, idx) => (
                    <button
                      key={idx}
                      onClick={() => setPrompt(text)}
                      className="text-[11px] bg-blue-50 text-blue-600 border border-blue-100 px-3 py-1.5 rounded-full hover:bg-blue-100 transition-all active:scale-95 shadow-sm hover:shadow-md"
                    >
                      {text}
                    </button>
                  ))}
                </div>
              )}

              <div className="flex gap-2 relative">
                <textarea
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault();
                      handleSendMessage();
                    }
                  }}
                  placeholder="例如：帮我用 LangChain 和 LlamaIndex 做一个 RAG 框架的对比评测..."
                  className="flex-1 resize-none rounded-xl border border-gray-300 p-3 pr-12 focus:outline-none focus:ring-2 focus:ring-blue-500 shadow-sm bg-gray-50 focus:bg-white transition-all"
                  rows={3}
                />
                <button
                  onClick={handleSendMessage}
                  disabled={loading || !prompt.trim()}
                  className="absolute right-2 bottom-2 p-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors shadow-lg"
                >
                  <Send className="w-5 h-5" />
                </button>
              </div>
            </div>
          </>
        )}
      </div>

      {/* 垂直拖拽条 */}
      <div 
        className={`w-1.5 bg-gray-200 hover:bg-blue-400 cursor-col-resize z-20 transition-colors flex items-center justify-center ${isResizing ? 'bg-blue-500' : ''}`}
        onMouseDown={() => setIsResizing(true)}
      >
        <div className="h-8 w-1 bg-gray-400 rounded-full"></div>
      </div>

      {/* 右侧面板: DAG 可视化区 */}
      <div className="flex-1 relative flex overflow-hidden">
        <div className="flex-1 h-full relative">
          <div className="absolute top-4 left-4 z-10 bg-white px-4 py-2 rounded-lg shadow-sm border border-gray-200 max-w-md">
            <h2 className="font-semibold text-gray-700 flex items-center gap-2">
              <TerminalSquare className="w-4 h-4" />
              {uiText.graphTitle}
            </h2>
            <p className="text-xs text-gray-500 mt-1">{uiText.graphHint}</p>
            {intentContext && (
              <div className="mt-3 space-y-2 border-t border-gray-100 pt-3 text-xs text-gray-600">
                <div className="flex flex-wrap gap-2">
                  <span className="rounded-full bg-blue-50 px-2 py-1 text-blue-700">
                    intent_type: {intentContext.intent_type}
                  </span>
                  <span className="rounded-full bg-slate-100 px-2 py-1 text-slate-700">
                    entities: {Object.keys(intentContext.entities || {}).length}
                  </span>
                </div>
                <div className="grid grid-cols-2 gap-2">
                  <div className="rounded-md bg-gray-50 px-2 py-1">frameworks: {stringifyEntity(intentContext.entities?.frameworks)}</div>
                  <div className="rounded-md bg-gray-50 px-2 py-1">paper_title: {stringifyEntity(intentContext.entities?.paper_title)}</div>
                  <div className="rounded-md bg-gray-50 px-2 py-1">needs_plot: {stringifyEntity(intentContext.entities?.needs_plot)}</div>
                  <div className="rounded-md bg-gray-50 px-2 py-1">needs_fix: {stringifyEntity(intentContext.entities?.needs_fix)}</div>
                  <div className="rounded-md bg-gray-50 px-2 py-1">needs_benchmark: {stringifyEntity(intentContext.entities?.needs_benchmark)}</div>
                  <div className="rounded-md bg-gray-50 px-2 py-1">output_mode: {stringifyEntity(intentContext.entities?.output_mode)}</div>
                </div>
              </div>
            )}
          </div>
          
          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeClick={onNodeClick}
            fitView
            className="bg-gray-50"
          >
            <Background color="#ccc" gap={16} />
            <Controls />
            <Panel position="top-right">
              <button
                onClick={handleRunAllTasks}
                disabled={isExecuting || nodes.filter(n => n.data.task && n.data.status !== 'completed').length === 0}
                className="bg-blue-600 hover:bg-blue-700 text-white font-bold py-2 px-4 rounded-xl shadow-lg flex items-center gap-2 transition-all active:scale-95 disabled:opacity-50 disabled:grayscale"
              >
                <Play className="w-4 h-4 fill-current" />
                {uiText.runAll}
              </button>
            </Panel>
          </ReactFlow>
        </div>

        {/* 侧边栏拖拽条 */}
        {selectedTask && !isReportExpanded && !isPlotExpanded && (
          <div 
            className={`w-1 bg-gray-200 hover:bg-blue-400 cursor-col-resize z-20 transition-colors flex items-center justify-center ${isResizingSidebar ? 'bg-blue-500' : ''}`}
            onMouseDown={() => setIsResizingSidebar(true)}
          >
            <div className="h-8 w-0.5 bg-gray-400 rounded-full"></div>
          </div>
        )}

        {/* 侧边栏: 节点详情与真实执行日志 */}
        {selectedTask && (
          <div 
            style={{ width: (isReportExpanded || isPlotExpanded) ? '100%' : `${sidebarWidth}px` }}
            className={`bg-white border-l border-gray-200 shadow-2xl flex flex-col z-20 transition-all duration-300 ${(isReportExpanded || isPlotExpanded) ? 'absolute inset-0' : 'relative'}`}
          >
            {isPlotExpanded ? (
              // 全屏图表视图
              <div className="flex-1 flex flex-col p-10 bg-white animate-in zoom-in-95 duration-300 overflow-hidden">
                <div className="flex-shrink-0 flex items-center justify-between mb-8 pb-6 border-b border-gray-100">
                  <div className="flex items-center gap-5">
                    <div className="p-4 bg-purple-600 rounded-3xl text-white shadow-xl rotate-3">
                      <Maximize2 className="w-8 h-8" />
                    </div>
                    <div>
                      <h2 className="text-3xl font-black text-gray-900 tracking-tight">生成的图表可视化</h2>
                      <div className="flex items-center gap-2 mt-1">
                        <span className="text-xs font-bold bg-purple-100 text-purple-700 px-2 py-0.5 rounded-full uppercase tracking-wider">Visual Result</span>
                        <span className="text-sm text-gray-400 font-medium">Rendered by Matplotlib in Sandbox</span>
                      </div>
                    </div>
                  </div>
                  <button 
                    onClick={() => setIsPlotExpanded(false)}
                    className="p-4 hover:bg-red-50 hover:text-red-500 rounded-3xl transition-all text-gray-400 active:scale-90 shadow-sm hover:shadow-md"
                  >
                    <X className="w-8 h-8" />
                  </button>
                </div>
                <div className="flex-1 flex items-center justify-center overflow-hidden bg-gray-50 rounded-3xl p-8 border border-gray-100 shadow-inner">
                  <img 
                    src={`data:image/png;base64,${executionImage}`} 
                    alt="Full Resolution Plot" 
                    className="max-w-full max-h-full object-contain rounded-xl shadow-2xl transition-transform hover:scale-105 duration-500"
                  />
                </div>
              </div>
            ) : isReportExpanded ? (
              // 全屏报告视图 (真正占满右侧空间)
              <div className="flex-1 flex flex-col p-10 bg-white animate-in zoom-in-95 duration-300 overflow-hidden">
                <div className="flex-shrink-0 flex items-center justify-between mb-8 pb-6 border-b border-gray-100">
                  <div className="flex items-center gap-5">
                    <div className="p-4 bg-blue-600 rounded-3xl text-white shadow-xl rotate-3">
                      <FileText className="w-8 h-8" />
                    </div>
                    <div>
                      <h2 className="text-3xl font-black text-gray-900 tracking-tight">{selectedTask.Name}</h2>
                      <div className="flex items-center gap-2 mt-1">
                        <span className="text-xs font-bold bg-blue-100 text-blue-700 px-2 py-0.5 rounded-full uppercase tracking-wider">Analysis Report</span>
                        <span className="text-sm text-gray-400 font-medium">Powered by ScholarAgent Insight Engine</span>
                      </div>
                    </div>
                  </div>
                  <button 
                    onClick={() => setIsReportExpanded(false)}
                    className="p-4 hover:bg-red-50 hover:text-red-500 rounded-3xl transition-all text-gray-400 active:scale-90 shadow-sm hover:shadow-md"
                  >
                    <X className="w-8 h-8" />
                  </button>
                </div>
                
                <div className="flex-1 overflow-y-auto px-4 min-h-0 scrollbar-thin scrollbar-thumb-gray-200">
                  <div className="max-w-4xl mx-auto prose prose-slate prose-lg lg:prose-xl text-gray-800 prose-headings:text-blue-900 prose-strong:text-blue-700 prose-code:bg-blue-50 prose-code:text-blue-600 prose-code:px-2 prose-code:py-0.5 prose-code:rounded-lg prose-img:rounded-3xl prose-img:shadow-2xl pb-10">
                    <ReactMarkdown 
                      remarkPlugins={[remarkGfm, remarkMath]}
                      rehypePlugins={[rehypeKatex]}
                    >
                      {executionResult}
                    </ReactMarkdown>
                  </div>
                </div>
              </div>
            ) : (
              // 普通侧边栏视图
              <>
                <div className="p-4 border-b border-gray-200 flex justify-between items-center bg-gray-50">
                  <h3 className="font-bold text-gray-800 flex items-center gap-2 text-base">
                    {getAgentIcon(selectedTask.AssignedTo)}
                    节点执行面板
                  </h3>
                  <button onClick={() => setSelectedTask(null)} className="text-gray-500 hover:text-gray-700 p-1.5 hover:bg-gray-200 rounded-full transition-all">
                    <X className="w-5 h-5" />
                  </button>
                </div>
                
                <div className="p-5 flex-1 overflow-y-auto flex flex-col gap-5">
                  <div className="bg-white p-4 rounded-2xl border border-gray-100 shadow-sm">
                    <label className="text-[10px] font-bold text-gray-400 uppercase tracking-wider block mb-1">任务名称</label>
                    <div className="text-base font-bold text-gray-800 leading-tight">{selectedTask.Name}</div>
                  </div>
                  
                  <div className="flex items-center justify-between px-1">
                    <label className="text-xs font-bold text-gray-500 uppercase tracking-tight">负责 Agent</label>
                    <div className="text-xs font-black text-blue-700 bg-blue-50 px-3 py-1.5 rounded-full border border-blue-100 shadow-sm font-mono">
                      {selectedTask.AssignedTo}
                    </div>
                  </div>

                  <button 
                    onClick={() => handleExecuteTask(selectedTask)}
                    disabled={isExecuting}
                    className="w-full bg-blue-600 hover:bg-blue-700 text-white font-black py-4 px-6 rounded-2xl flex items-center justify-center gap-3 disabled:opacity-50 disabled:cursor-not-allowed transition-all shadow-[0_10px_20px_-10px_rgba(37,99,235,0.5)] active:scale-[0.98] active:shadow-inner"
                  >
                    {isExecuting ? (
                      <span className="animate-pulse flex items-center gap-2">
                        <Loader2 className="w-5 h-5 animate-spin" />
                        正在深度解析...
                      </span>
                    ) : (
                      <>
                        <Play className="w-5 h-5 fill-current" />
                        启动 Agent 任务
                      </>
                    )}
                  </button>

                  {/* 视图切换 Tabs */}
                  {(executionResult || executionCode) && (
                    <div className="flex border-b border-gray-100 mt-2 items-center justify-between">
                      <div className="flex flex-1">
                        <button
                          onClick={() => setViewMode('logs')}
                          className={`flex-1 py-3 text-xs font-black text-center border-b-2 transition-all ${viewMode === 'logs' ? 'border-blue-500 text-blue-600' : 'border-transparent text-gray-400 hover:text-gray-600'}`}
                        >
                          实时日志
                        </button>
                        {executionCode && (
                          <button
                            onClick={() => setViewMode('code')}
                            className={`flex-1 py-3 text-xs font-black text-center border-b-2 flex items-center justify-center gap-1 transition-all ${viewMode === 'code' ? 'border-blue-500 text-blue-600' : 'border-transparent text-gray-400 hover:text-gray-600'}`}
                          >
                            <Code className="w-4 h-4" />
                            沙箱代码
                          </button>
                        )}
                        {executionImage && (
                          <button
                            onClick={() => setViewMode('plot')}
                            className={`flex-1 py-3 text-xs font-black text-center border-b-2 flex items-center justify-center gap-1 transition-all ${viewMode === 'plot' ? 'border-blue-500 text-blue-600' : 'border-transparent text-gray-400 hover:text-gray-600'}`}
                          >
                            <Maximize2 className="w-4 h-4" />
                            生成图表
                          </button>
                        )}
                        {(selectedTask.AssignedTo === 'librarian_agent' || selectedTask.AssignedTo === 'data_agent') && executionResult && (
                          <button
                            onClick={() => setViewMode('report')}
                            className={`flex-1 py-3 text-xs font-black text-center border-b-2 flex items-center justify-center gap-1 transition-all ${viewMode === 'report' ? 'border-blue-500 text-blue-600' : 'border-transparent text-gray-400 hover:text-gray-600'}`}
                          >
                            <Eye className="w-4 h-4" />
                            分析报告
                          </button>
                        )}
                      </div>
                      {viewMode === 'report' && (
                        <button 
                          onClick={() => setIsReportExpanded(true)}
                          className="ml-3 p-2.5 text-blue-500 hover:bg-blue-50 rounded-xl transition-all active:scale-90 border border-blue-50 shadow-sm"
                          title="全屏阅读报告"
                        >
                          <Maximize2 className="w-4 h-4" />
                        </button>
                      )}
                      {viewMode === 'plot' && (
                        <button 
                          onClick={() => setIsPlotExpanded(true)}
                          className="ml-3 p-2.5 text-blue-500 hover:bg-blue-50 rounded-xl transition-all active:scale-90 border border-blue-50 shadow-sm"
                          title="全屏查看图表"
                        >
                          <Maximize2 className="w-4 h-4" />
                        </button>
                      )}
                    </div>
                  )}

                  <div className="mt-1 flex-1 flex flex-col min-h-0">
                    {viewMode === 'logs' ? (
                      <>
                        <label className="text-[10px] font-bold text-gray-400 uppercase mb-2 flex items-center gap-1 tracking-wider">
                          <TerminalSquare className="w-3 h-3" />
                          Pipeline Output
                        </label>
                        <div className="bg-gray-900 rounded-2xl p-5 flex-1 overflow-y-auto font-mono text-[11px] text-green-400 leading-relaxed shadow-2xl border border-gray-800 whitespace-pre-wrap selection:bg-green-800 selection:text-white scrollbar-thin scrollbar-thumb-gray-700">
                          {executionLogs || '>>> 准备就绪，等待响应...'}
                          {executionResult && !['librarian_agent', 'data_agent'].includes(selectedTask.AssignedTo) && (
                            <div className="mt-5 pt-5 border-t border-gray-800 text-blue-400 font-bold">
                              [Output]:<br/>{executionResult}
                            </div>
                          )}
                          <div ref={logsEndRef} />
                        </div>
                      </>
                    ) : viewMode === 'code' ? (
                      <div className="bg-gray-50 rounded-2xl border border-gray-200 p-6 flex-1 overflow-y-auto shadow-inner prose prose-slate prose-sm max-w-none text-gray-800 h-64">
                        <ReactMarkdown 
                          remarkPlugins={[remarkGfm, remarkMath]}
                          rehypePlugins={[rehypeKatex]}
                        >
                          {`\`\`\`python\n${executionCode}\n\`\`\``}
                        </ReactMarkdown>
                      </div>
                    ) : viewMode === 'plot' ? (
                      <div className="bg-white rounded-2xl border border-gray-100 p-2 flex-1 flex flex-col items-center justify-center overflow-hidden shadow-inner h-64">
                        <img 
                          src={`data:image/png;base64,${executionImage}`} 
                          alt="Generated Plot" 
                          className="max-w-full max-h-full object-contain rounded-lg shadow-md"
                        />
                        <div className="mt-2 text-[10px] text-gray-400">点击下方按钮可全屏查看</div>
                      </div>
                    ) : (
                      <div className="bg-white rounded-2xl border border-gray-100 p-6 flex-1 overflow-y-auto shadow-inner prose prose-slate prose-sm max-w-none text-gray-800 prose-headings:text-blue-900 prose-strong:text-blue-700 prose-code:bg-blue-50 prose-code:text-blue-600 prose-code:px-1 prose-code:rounded h-64">
                        <ReactMarkdown 
                          remarkPlugins={[remarkGfm, remarkMath]}
                          rehypePlugins={[rehypeKatex]}
                        >
                          {executionResult}
                        </ReactMarkdown>
                      </div>
                    )}
                  </div>
                </div>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
