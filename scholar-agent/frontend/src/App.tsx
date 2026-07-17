import { useState, useRef, useEffect, useCallback, type MouseEvent as ReactMouseEvent } from 'react';
import { Background, Controls, ReactFlow, useNodesState, useEdgesState, Panel } from '@xyflow/react';
import type { Node, Edge } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { Activity, AlertTriangle, CheckCircle2, Send, Bot, FileText, Code, Database, TerminalSquare, Play, X, Eye, FileUp, Maximize2, Languages, Loader2, Sparkles, ChevronDown, ChevronUp } from 'lucide-react';
import axios from 'axios';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import 'katex/dist/katex.min.css';
import { API_BASE_URL } from './config/env';

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
  Description: string;
  AssignedTo: string;
  Status: string;
  Dependencies: string[];
}

interface Plan {
  ID: string;
  UserIntent: string;
  Tasks: Record<string, Task>;
  Status: string;
}

interface ReproductionResourceProbe {
  cpu_count?: number;
  memory_gb?: number;
  disk_free_gb?: number;
  gpu_count?: number;
}

interface ReproductionModeDecision {
  effective_mode?: string;
  full_eligible?: boolean;
  reasons?: string[];
}

interface PlanClarification {
  required?: boolean;
  type?: string;
  recommended_mode?: string;
  question?: string;
  options?: Array<{
    id: string;
    label: string;
    description: string;
  }>;
  mode_decision?: ReproductionModeDecision;
  resource_probe?: ReproductionResourceProbe;
}

interface ServiceHealth {
  ok?: boolean;
  backend?: {
    ok?: boolean;
    message?: string;
  };
  sandbox?: {
    ok?: boolean;
    url?: string;
    message?: string;
    effective_strategy?: string;
    native_docker?: {
      available?: boolean;
      command?: string;
      server_version?: string;
      error?: string;
    };
  };
}

interface ExecuteResultEvent {
  result?: string;
  code?: string;
  image_base_64?: string;
}

const getErrorMessage = (error: unknown) => {
  if (error instanceof Error) return error.message;
  return String(error);
};

const formatPlanClarification = (clarification?: PlanClarification) => {
  if (!clarification?.required || clarification.type !== 'paper_reproduction_mode') return '';

  const probe = clarification.resource_probe;
  const decision = clarification.mode_decision;
  const lines = [
    '### 需要确认复现模式',
    '',
    clarification.question || '检测到论文复现可能涉及全量实验，请确认运行模式。',
  ];

  if (clarification.recommended_mode) {
    lines.push('', `**当前建议模式：** \`${clarification.recommended_mode}\``);
  }

  if (probe) {
    lines.push(
      '',
      '**本机资源探测：**',
      `- CPU：${probe.cpu_count ?? '未知'}`,
      `- 内存：${typeof probe.memory_gb === 'number' ? `${probe.memory_gb.toFixed(1)}GB` : '未知'}`,
      `- 可用磁盘：${typeof probe.disk_free_gb === 'number' ? `${probe.disk_free_gb.toFixed(1)}GB` : '未知'}`,
      `- CUDA GPU：${probe.gpu_count ?? 0}`,
    );
  }

  if (decision?.reasons?.length) {
    lines.push('', '**判断依据：**', ...decision.reasons.map((reason) => `- ${reason}`));
  }

  if (clarification.options?.length) {
    lines.push(
      '',
      '**可选运行方式：**',
      ...clarification.options.map((option) => `- \`${option.id}\` ${option.label}：${option.description}`),
    );
  }

  lines.push('', '当前会先生成任务拓扑；如果你确认要全量复现，下一步可以把运行模式切到 `full`。');
  return lines.join('\n');
};

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

const getAgentLabel = (agentName: string) => {
  switch (agentName) {
    case 'librarian_agent': return 'Librarian';
    case 'coder_agent': return 'Coder';
    case 'sandbox_agent': return 'Sandbox';
    case 'data_agent': return 'Data';
    default: return 'Agent';
  }
};

const getAgentTone = (agentName: string) => {
  switch (agentName) {
    case 'librarian_agent':
      return { border: '#60a5fa', soft: '#eff6ff', text: '#1d4ed8' };
    case 'coder_agent':
      return { border: '#a78bfa', soft: '#f5f3ff', text: '#6d28d9' };
    case 'sandbox_agent':
      return { border: '#fb923c', soft: '#fff7ed', text: '#c2410c' };
    case 'data_agent':
      return { border: '#34d399', soft: '#ecfdf5', text: '#047857' };
    default:
      return { border: '#94a3b8', soft: '#f8fafc', text: '#475569' };
  }
};

const getStatusLabel = (status: string) => {
  switch (status.toLowerCase()) {
    case 'completed': return 'Completed';
    case 'failed': return 'Failed';
    case 'in_progress': return 'Running';
    case 'running': return 'Running';
    default: return 'Pending';
  }
};

const suggestedPrompts = [
  { category: '可视化', text: '帮我画一个正弦函数和余弦函数的对比图' },
  { category: '复现', text: '复现一下 Transformer 论文的核心架构并跑通测试' },
  { category: '评测', text: '对比一下 LangChain 和 LlamaIndex 的 RAG 性能' },
  { category: '论文', text: '分析一下这篇论文的主要创新点和局限性' },
  { category: '代码', text: '帮我复现 Attention Is All You Need 论文的代码' },
];

// --- 主应用组件 ---
export default function App() {
  const [prompt, setPrompt] = useState('');
  const [loading, setLoading] = useState(false);
  const [serviceHealth, setServiceHealth] = useState<ServiceHealth | null>(null);
  const [healthLoading, setHealthLoading] = useState(false);
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [currentPlan, setCurrentPlan] = useState<Plan | null>(null);
  const [chatHistory, setChatHistory] = useState<ChatMessage[]>([
    { role: 'system', text: '你好！我是 ScholarAgent 智能科研助理。请问今天有什么我可以帮你的？' }
  ]);

  // Resizable Panels State
  const [leftPanelWidth, setLeftPanelWidth] = useState(35); // 默认 35%
  const [isResizing, setIsResizing] = useState(false);

  const [sidebarWidth, setSidebarWidth] = useState(450); // 默认 450px
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);

  // 全文翻译状态
  const [isFullTranslating, setIsFullTranslating] = useState(false);

  // 新增状态：保存各个节点的执行状态（日志、结果、代码、图表），避免关闭侧边栏后丢失
  const [nodeStates, setNodeStates] = useState<Record<string, { logs: string, result: string, code: string, imageBase64?: string }>>({});

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

  // 实例化 AI 翻译插件
  const handleAskAI = useCallback((selectedText: string) => {
    setPrompt(`请帮我详细解释这篇文献中的这段内容：\n"${selectedText}"`);
    if (pdfUrl) {
      setExecutionLogs(prev => prev + `\n\n[System] 已获取划词内容，准备向 ScholarAgent 发起追问...`);
    }
  }, [pdfUrl]);

  const aiTranslationPluginInstance = useAITranslationPlugin(handleAskAI);

  const refreshServiceHealth = useCallback(async () => {
    setHealthLoading(true);
    try {
      const response = await fetch(`${API_BASE_URL}/api/health`);
      if (!response.ok) throw new Error(`health check failed with HTTP ${response.status}`);
      const health = await response.json() as ServiceHealth;
      setServiceHealth(health);
    } catch (error) {
      setServiceHealth({
        ok: false,
        backend: {
          ok: false,
          message: getErrorMessage(error),
        },
      });
    } finally {
      setHealthLoading(false);
    }
  }, []);

  // 自动滚动日志到底部
  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [executionLogs]);

  useEffect(() => {
    void refreshServiceHealth();
    const timer = window.setInterval(() => {
      void refreshServiceHealth();
    }, 30000);
    return () => window.clearInterval(timer);
  }, [refreshServiceHealth]);

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

    // 智能判断意图：是否包含任务触发关键词（扩展版）
    const isTaskRequest = /对比|比较|评估|选型|rag|复现|跑一下|执行|画|绘图|plot|matplotlib|langchain|llamaindex|llama.index|haystack|框架|配环境|安装依赖|vs\b/.test(userPrompt.toLowerCase());
    const isPaperTaskRequest = /复现|论文|paper|arxiv|attention is all you need|transformer/.test(userPrompt.toLowerCase());

    try {
      if (isTaskRequest) {
        // 1. 任务编排逻辑 (Plan)
        const response = await axios.post(`${API_BASE_URL}/api/plan`, {
          intent: userPrompt
        });

        const generatedPlan = response.data.plan;
        if (!generatedPlan) throw new Error('Backend did not return plan');
        setCurrentPlan(generatedPlan);
        setSelectedTask(null);
        renderDAG(generatedPlan, false); // 渲染 DAG 图

        const clarificationText = formatPlanClarification(response.data.clarification);
        const systemMessages: ChatMessage[] = [];
        if (clarificationText) {
          systemMessages.push({ role: 'system', text: clarificationText });
        }
        systemMessages.push({
          role: 'system',
          text: `我已分析您的需求，并为您生成了专属的多智能体协作工作流。您可以点击右侧的 DAG 节点查看详情或执行任务。`,
          actions: isPaperTaskRequest ? ['open_pdf', 'translate_full', 'close_pdf'] : undefined
        });
        setChatHistory(prev => [...prev, ...systemMessages]);
      } else {
        // 2. 简单问答逻辑 (Chat)
        const response = await axios.post(`${API_BASE_URL}/api/chat`, {
          message: userPrompt
        });

        setChatHistory(prev => [...prev, {
          role: 'system',
          text: response.data.response
        }]);
      }

    } catch (error) {
      console.error(error);
      setChatHistory(prev => [...prev, { role: 'system', text: '抱歉，连接后端服务失败。请确保 Go 后端运行在 :8080 端口。' }]);
    } finally {
      setLoading(false);
    }
  };

  // 触发真实的 Agent 执行 (调用 DeepSeek + 沙箱)
  const handleExecuteTask = async (task: Task): Promise<string> => {
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

    let latestResult = '';
    let receivedResult = false;
    try {
        const response = await fetch(`${API_BASE_URL}/api/execute`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({
            task_id: task.ID,
            task_name: task.Name,
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
          } catch {
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
              receivedResult = true;
              let finalResult = eventData;
              let generatedCode = '';
              let imageBase64 = '';
              try {
                const parsed = JSON.parse(eventData) as ExecuteResultEvent;
                if (parsed && parsed.result) finalResult = parsed.result;
                if (parsed && parsed.code) generatedCode = parsed.code;
                if (parsed && parsed.image_base_64) imageBase64 = parsed.image_base_64;
              } catch {
                finalResult = eventData;
              }

              const completionMsg = `\n\n[🎉 Agent 思考与执行完毕]`;
              setExecutionLogs(prev => prev + completionMsg);
              setExecutionResult(finalResult);
              latestResult = finalResult;
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
        if (!receivedResult) {
          throw new Error('后端未返回 result 事件，任务结果不完整');
        }
        return latestResult; // 完成并返回结果，避免读取异步 state 旧值

      } catch (error: unknown) {
        console.error(error);
        const rawErrorMessage = getErrorMessage(error);
        const errorMsg = rawErrorMessage === 'Failed to fetch'
          ? '哎呀，与后端失联了 📡！可能是大模型思考太久导致连接超时，或者您的本地端口被占用了，请重试一下～'
          : rawErrorMessage;
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
        throw error;
      } finally {
        setIsExecuting(false);
      }
  };

  // 一键运行所有任务
  const handleRunAllTasks = async () => {
    if (isExecuting) return;

    // 找出所有未完成的任务节点
    const pendingNodes = nodes.filter(n => n.data.task && n.data.status !== 'completed');

    // 拓扑排序：基于 Dependencies 字段确定执行顺序
    const taskList = pendingNodes.map(n => n.data.task as Task);
    const taskMap: Record<string, Task> = {};
    taskList.forEach(t => { taskMap[t.ID] = t; });

    // Kahn's algorithm
    const inDegree: Record<string, number> = {};
    const adjList: Record<string, string[]> = {};
    taskList.forEach(t => {
      inDegree[t.ID] = 0;
      adjList[t.ID] = [];
    });
    taskList.forEach(t => {
      (t.Dependencies || []).forEach(depId => {
        if (adjList[depId]) {
          adjList[depId].push(t.ID);
          inDegree[t.ID] = (inDegree[t.ID] || 0) + 1;
        }
      });
    });
    const queue = taskList.filter(t => inDegree[t.ID] === 0).map(t => t.ID);
    const sortedTaskIds: string[] = [];
    const sortedSet = new Set<string>();
    while (queue.length > 0) {
      const id = queue.shift()!;
      sortedTaskIds.push(id);
      sortedSet.add(id);
      (adjList[id] || []).forEach(nextId => {
        inDegree[nextId]--;
        if (inDegree[nextId] === 0) queue.push(nextId);
      });
    }
    if (sortedTaskIds.length !== taskList.length) {
      setChatHistory(prev => [...prev, { role: 'system', text: '❌ 检测到循环依赖或非法任务图（DAG），已中止自动流水线执行。' }]);
      return;
    }
    const taskNodes = sortedTaskIds.map(id => taskMap[id]).filter((t): t is Task => t != null);

    // 构建任务完成结果的共享上下文，用于将上游结果传递给下游任务
    const taskResults: Record<string, string> = {};

    setChatHistory(prev => [...prev, {
      role: 'system',
      text: `🚀 开始全自动流水线任务！共需执行 ${taskNodes.length} 个节点，请耐心等待。`
    }]);

    let aborted = false;

    for (const task of taskNodes) {
      // 将所有依赖任务的结果拼接到当前任务的描述中
      const depResults: string[] = [];
      for (const depId of (task.Dependencies || [])) {
        if (taskResults[depId]) {
          depResults.push(`\n\n===上游任务 [${depId.substring(0, 8)}] 的执行结果===\n${taskResults[depId].substring(0, 3000)}`);
        }
      }

      const enrichedTask: Task = depResults.length > 0
        ? { ...task, Description: task.Description + depResults.join('') }
        : task;

      try {
        const result = await handleExecuteTask(enrichedTask);
        // 保存当前任务结果，供下游使用
        taskResults[task.ID] = result || '';
      } catch (e) {
        console.error(`Task ${task.Name} failed:`, e);
        setChatHistory(prev => [...prev, {
          role: 'system',
          text: `⚠️ 流水线在节点 **[${task.Name}]** 处中断。后续节点已取消自动执行。`
        }]);
        aborted = true;
        break;
      }
    }

    setChatHistory(prev => [...prev, {
      role: 'system',
      text: aborted ? `⛔ 全自动流水线已中断。` : `🏁 全自动流水线执行完毕！`
    }]);
  };

  // 节点点击事件
  const onNodeClick = (_event: ReactMouseEvent | null, node: Node) => {
    const taskData = node.data.task as Task;
    if (taskData) {
      setSelectedTask(taskData);
      if (currentPlan) renderDAG(currentPlan, true);

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

  const closeTaskPanel = () => {
    setSelectedTask(null);
    if (currentPlan) renderDAG(currentPlan, false);
  };

  // 渲染有向无环图 (DAG)
  const renderDAG = (plan: Plan, detailPanelOpen = Boolean(selectedTask && !isReportExpanded && !isPlotExpanded)) => {
    const newNodes: Node[] = [];
    const newEdges: Edge[] = [];
    const existingTaskById = new Map<string, Task>();
    nodes.forEach((node) => {
      const existingTask = node.data.task as Task | undefined;
      if (existingTask?.ID) existingTaskById.set(existingTask.ID, existingTask);
    });

    const taskArray = Object.values(plan.Tasks).map((task) => {
      const existingTask = existingTaskById.get(task.ID);
      return existingTask ? { ...task, Status: existingTask.Status || task.Status } : task;
    });
    const taskMap = new Map(taskArray.map((task) => [task.ID, task]));
    const levelCache = new Map<string, number>();

    const getTaskLevel = (task: Task): number => {
      if (levelCache.has(task.ID)) return levelCache.get(task.ID)!;
      const dependencies = (task.Dependencies || [])
        .map((depId) => taskMap.get(depId))
        .filter((dep): dep is Task => Boolean(dep));
      const level = dependencies.length === 0
        ? 0
        : Math.max(...dependencies.map((dependency) => getTaskLevel(dependency))) + 1;
      levelCache.set(task.ID, level);
      return level;
    };

    const levelGroups = new Map<number, Task[]>();
    taskArray.forEach((task) => {
      const level = getTaskLevel(task);
      levelGroups.set(level, [...(levelGroups.get(level) || []), task]);
    });

    const sortedTasks = [...levelGroups.keys()]
      .sort((a, b) => a - b)
      .flatMap((level) => levelGroups.get(level) || []);
    const useCompactBoardLayout = sortedTasks.length > 5 || levelGroups.size > 4 || detailPanelOpen;
    const compactColumns = Math.min(detailPanelOpen ? 2 : 3, Math.max(1, sortedTasks.length));

    sortedTasks.forEach((task, index) => {
      const level = getTaskLevel(task);
      const laneIndex = (levelGroups.get(level) || []).findIndex((item) => item.ID === task.ID);
      const compactColumn = index % compactColumns;
      const compactRow = Math.floor(index / compactColumns);
      const nodeWidth = detailPanelOpen ? 200 : 232;
      const x = useCompactBoardLayout
        ? (detailPanelOpen ? 16 + (compactColumn * 212) : 64 + (compactColumn * 292))
        : 44 + (level * 276);
      const y = useCompactBoardLayout
        ? 132 + (compactRow * (detailPanelOpen ? 164 : 178))
        : 132 + (Math.max(laneIndex, 0) * 168);
      const agentTone = getAgentTone(task.AssignedTo);

      newNodes.push({
        id: task.ID,
        position: { x, y },
        data: {
          task: task, // 存储完整 task 数据供点击使用
          label: (
            <div className={`flex ${detailPanelOpen ? 'w-48' : 'w-56'} flex-col gap-2.5 p-2.5`}>
              <div className="flex items-center justify-between border-b border-slate-100 pb-2">
                <div className="flex items-center gap-2">
                  <span className="flex h-7 w-7 items-center justify-center rounded-lg" style={{ backgroundColor: agentTone.soft }}>
                    {getAgentIcon(task.AssignedTo)}
                  </span>
                  <div className="text-left">
                    <div className="text-xs font-semibold text-slate-700">{getAgentLabel(task.AssignedTo)}</div>
                    <div className="font-mono text-[10px] text-slate-400">{task.AssignedTo}</div>
                  </div>
                </div>
                <span className="rounded-md border border-slate-200 bg-slate-50 px-2 py-1 text-[10px] font-semibold text-slate-500">
                  {getStatusLabel(task.Status)}
                </span>
              </div>
              <div className="text-left text-[13px] font-semibold leading-snug text-slate-800">{task.Name}</div>
              <div className="flex items-center justify-between text-[11px] text-slate-400">
                <span>依赖 {task.Dependencies.length}</span>
                <span>优先级 {index + 1}</span>
              </div>
            </div>
          )
        },
        style: {
          borderRadius: '8px',
          backgroundColor: 'white',
          border: '1px solid',
          borderColor: task.Status === 'pending' ? '#e2e8f0' : agentTone.border,
          boxShadow: '0 12px 28px -22px rgb(15 23 42 / 0.55)',
          cursor: 'pointer',
          overflow: 'hidden',
          width: nodeWidth,
        }
      });

      task.Dependencies.forEach(depId => {
        newEdges.push({
          id: `e-${depId}-${task.ID}`,
          source: depId,
          target: task.ID,
          animated: true,
          type: 'smoothstep',
          style: { stroke: '#9aa9bd', strokeWidth: 1.7, strokeDasharray: '6 5' }
        });
      });
    });

    setNodes(newNodes);
    setEdges(newEdges);
  };

  const backendOk = serviceHealth?.backend?.ok ?? false;
  const sandboxOk = serviceHealth?.sandbox?.ok ?? false;
  const dockerError = serviceHealth?.sandbox?.native_docker?.error || serviceHealth?.sandbox?.message;
  const statusTone = backendOk && sandboxOk ? 'bg-emerald-50 text-emerald-800 border-emerald-200' : backendOk ? 'bg-amber-50 text-amber-900 border-amber-200' : 'bg-rose-50 text-rose-800 border-rose-200';
  const statusTitle = backendOk && sandboxOk ? '完整服务已就绪' : backendOk ? '后端可用，沙箱待配置' : '后端服务未连接';
  const pendingTaskCount = nodes.filter(n => n.data.task && n.data.status !== 'completed').length;
  const canExecuteTasks = sandboxOk;

  return (
    <div className="flex h-screen bg-slate-100 font-sans text-slate-900 overflow-hidden">
      {/* 左侧面板: 聊天交互与 PDF 阅读区 */}
      <div
        style={{ width: `${leftPanelWidth}%` }}
        className="relative z-10 flex flex-col bg-white/95 border-r border-slate-200 shadow-xl flex-shrink-0 transition-all duration-300"
      >
        <div className="border-b border-slate-800 bg-slate-950 px-4 py-4 text-white flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-blue-500">
              <Bot className="w-5 h-5" />
            </div>
            <div>
              <h1 className="text-lg font-bold">ScholarAgent</h1>
              <div className="text-xs text-slate-300">科研规划 · 沙箱执行 · 结果复盘</div>
            </div>
          </div>
          {pdfUrl && (
            <button onClick={() => setPdfUrl(null)} className="text-slate-300 hover:text-white p-1 hover:bg-white/10 rounded-md transition-colors">
              <X className="w-5 h-5" />
            </button>
          )}
        </div>
        <div className={`border-b px-4 py-3 text-xs ${statusTone}`}>
          <div className="flex items-start justify-between gap-3">
            <div className="flex min-w-0 gap-2">
              <div className="pt-0.5">
                {backendOk && sandboxOk ? <CheckCircle2 className="h-4 w-4" /> : <AlertTriangle className="h-4 w-4" />}
              </div>
              <div className="min-w-0">
                <div className="font-semibold">{healthLoading && !serviceHealth ? '正在检测服务状态...' : statusTitle}</div>
                <div className="mt-1 truncate">
                  后端：{backendOk ? '正常' : serviceHealth?.backend?.message || '未检测'} · 沙箱：{sandboxOk ? `正常${serviceHealth?.sandbox?.native_docker?.server_version ? ` (Docker ${serviceHealth.sandbox.native_docker.server_version})` : ''}` : dockerError || '未检测'}
                </div>
              </div>
            </div>
            <button
              onClick={() => void refreshServiceHealth()}
              disabled={healthLoading}
              className="shrink-0 rounded-md border border-current/20 bg-white/45 px-2 py-1 font-medium hover:bg-white/70 disabled:opacity-60"
            >
              {healthLoading ? '检测中' : '重新检测'}
            </button>
          </div>
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
            <div className="flex-1 overflow-y-auto bg-slate-50/70 p-4 space-y-4">
              {chatHistory.map((msg, i) => (
                <div key={i} className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                  <div className={`max-w-[85%] flex flex-col gap-2 ${msg.role === 'user' ? 'items-end' : 'items-start'}`}>
                    <div className={`rounded-lg px-4 py-3 shadow-sm ${
                      msg.role === 'user'
                        ? 'bg-blue-600 text-white'
                        : 'border border-slate-200 bg-white text-slate-800'
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
                            onClick={() => setPdfUrl(`${API_BASE_URL}/api/pdf-proxy?url=https%3A%2F%2Farxiv.org%2Fpdf%2F1706.03762.pdf`)}
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
                  <div className="border border-slate-200 bg-white rounded-lg px-4 py-3 text-slate-500 animate-pulse flex items-center gap-2">
                    <Bot className="w-4 h-4" />
                    正在使用 Planner 编排多智能体任务拓扑图...
                  </div>
                </div>
              )}
            </div>

            <div className="border-t border-slate-200 bg-white p-4">
              {/* 试试：提示词推荐标题与切换开关 */}
              <div className="flex items-center justify-between mb-2 px-1">
                <button
                  onClick={() => setShowSuggestions(!showSuggestions)}
                  className="group flex items-center gap-1.5 text-xs font-semibold text-slate-500 hover:text-blue-600 transition-colors"
                >
                  <Sparkles className={`w-3 h-3 ${showSuggestions ? 'text-blue-500' : 'text-gray-400'} group-hover:animate-pulse`} />
                  任务模板
                  {showSuggestions ? <ChevronDown className="w-3 h-3" /> : <ChevronUp className="w-3 h-3" />}
                </button>
              </div>

              {/* 推荐列表内容 */}
              {showSuggestions && (
                <div className="mb-4 grid grid-cols-1 gap-2 animate-in slide-in-from-bottom-2 duration-300">
                  {suggestedPrompts.map((item, idx) => (
                    <button
                      key={idx}
                      onClick={() => setPrompt(item.text)}
                      className="group flex items-center justify-between rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 text-left text-xs text-slate-700 shadow-sm transition-all hover:border-blue-200 hover:bg-blue-50 active:scale-[0.99]"
                    >
                      <span className="min-w-0 truncate">{item.text}</span>
                      <span className="ml-3 shrink-0 rounded-md bg-white px-2 py-0.5 text-[10px] font-semibold text-slate-500 group-hover:text-blue-600">
                        {item.category}
                      </span>
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
                  className="flex-1 resize-none rounded-lg border border-slate-300 bg-slate-50 p-3 pr-12 shadow-sm transition-all focus:bg-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  rows={3}
                />
                <button
                  onClick={handleSendMessage}
                  disabled={loading || !prompt.trim()}
                  className="absolute right-2 bottom-2 rounded-lg bg-blue-600 p-2 text-white shadow-lg transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
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
        className={`w-1.5 bg-slate-200 hover:bg-blue-400 cursor-col-resize z-20 transition-colors flex items-center justify-center ${isResizing ? 'bg-blue-500' : ''}`}
        onMouseDown={() => setIsResizing(true)}
      >
        <div className="h-8 w-1 rounded-full bg-slate-400"></div>
      </div>

      {/* 右侧面板: DAG 可视化区 */}
      <div className="flex-1 relative flex overflow-hidden">
        <div className="flex-1 h-full relative bg-slate-50">
          <div className="absolute top-4 left-4 z-10 rounded-lg border border-slate-200 bg-white/95 px-4 py-3 shadow-sm">
            <div className="flex items-center gap-2">
              <Activity className="h-4 w-4 text-blue-600" />
              <h2 className="font-semibold text-slate-800">多智能体执行计划</h2>
            </div>
            <p className="mt-1 text-xs text-slate-500">点击节点查看详情；环境就绪后可启动真实执行</p>
            <div className="mt-2 flex gap-2 text-[11px] text-slate-500">
              <span className="rounded-md bg-slate-100 px-2 py-1">节点 {nodes.length}</span>
              <span className="rounded-md bg-slate-100 px-2 py-1">待执行 {pendingTaskCount}</span>
            </div>
          </div>
          {nodes.length === 0 && (
            <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center">
              <div className="max-w-sm rounded-lg border border-slate-200 bg-white/90 p-5 text-center shadow-sm">
                <div className="mx-auto mb-3 flex h-10 w-10 items-center justify-center rounded-lg bg-blue-50 text-blue-600">
                  <TerminalSquare className="h-5 w-5" />
                </div>
                <div className="font-semibold text-slate-800">等待生成执行图</div>
                <p className="mt-2 text-sm text-slate-500">从左侧输入研究任务，系统会把需求拆成可检查、可执行的 Agent 节点。</p>
              </div>
            </div>
          )}

          <ReactFlow
            nodes={nodes}
            edges={edges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onNodeClick={onNodeClick}
            fitView
            fitViewOptions={{ padding: 0.04, maxZoom: 1 }}
            className="bg-slate-50"
          >
            <Background color="#d4dae6" gap={18} />
            <Controls />
            <Panel position="top-right">
              <button
                onClick={handleRunAllTasks}
                disabled={isExecuting || pendingTaskCount === 0 || !canExecuteTasks}
                title={!canExecuteTasks ? '沙箱未就绪，暂不能执行节点' : undefined}
                className="flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 font-bold text-white shadow-lg transition-all hover:bg-blue-700 active:scale-95 disabled:cursor-not-allowed disabled:bg-slate-500 disabled:opacity-70"
              >
                <Play className="w-4 h-4 fill-current" />
                {canExecuteTasks ? '一键运行所有节点' : '沙箱未就绪'}
              </button>
            </Panel>
          </ReactFlow>
        </div>

        {/* 侧边栏拖拽条 */}
        {selectedTask && !isReportExpanded && !isPlotExpanded && (
          <div
            className={`w-1 bg-slate-200 hover:bg-blue-400 cursor-col-resize z-20 transition-colors flex items-center justify-center ${isResizingSidebar ? 'bg-blue-500' : ''}`}
            onMouseDown={() => setIsResizingSidebar(true)}
          >
            <div className="h-8 w-0.5 rounded-full bg-slate-400"></div>
          </div>
        )}

        {/* 侧边栏: 节点详情与真实执行日志 */}
        {selectedTask && (
          <div
            style={{ width: (isReportExpanded || isPlotExpanded) ? '100%' : `${sidebarWidth}px` }}
            className={`bg-white border-l border-slate-200 shadow-2xl flex flex-col z-20 transition-all duration-300 ${(isReportExpanded || isPlotExpanded) ? 'absolute inset-0' : 'relative'}`}
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
                <div className="flex items-center justify-between border-b border-slate-200 bg-slate-50 p-4">
                  <h3 className="flex items-center gap-2 text-base font-bold text-slate-800">
                    {getAgentIcon(selectedTask.AssignedTo)}
                    节点执行面板
                  </h3>
                  <button onClick={closeTaskPanel} className="rounded-md p-1.5 text-slate-500 transition-all hover:bg-slate-200 hover:text-slate-700">
                    <X className="w-5 h-5" />
                  </button>
                </div>

                <div className="p-5 flex-1 overflow-y-auto flex flex-col gap-5">
                  <div className="rounded-lg border border-slate-200 bg-white p-4 shadow-sm">
                    <label className="mb-1 block text-[11px] font-bold text-slate-400">任务名称</label>
                    <div className="text-base font-bold leading-tight text-slate-800">{selectedTask.Name}</div>
                  </div>

                  <div className="flex items-center justify-between px-1">
                    <label className="text-xs font-bold text-slate-500">负责 Agent</label>
                    <div className="rounded-md border border-blue-100 bg-blue-50 px-3 py-1.5 font-mono text-xs font-black text-blue-700 shadow-sm">
                      {getAgentLabel(selectedTask.AssignedTo)}
                    </div>
                  </div>

                  <button
                    onClick={() => handleExecuteTask(selectedTask)}
                    disabled={isExecuting || !canExecuteTasks}
                    title={!canExecuteTasks ? '沙箱未就绪，暂不能执行节点' : undefined}
                    className="flex w-full items-center justify-center gap-3 rounded-lg bg-blue-600 px-6 py-4 font-black text-white shadow-[0_10px_20px_-10px_rgba(37,99,235,0.5)] transition-all hover:bg-blue-700 active:scale-[0.98] active:shadow-inner disabled:cursor-not-allowed disabled:bg-slate-500 disabled:opacity-70"
                  >
                    {isExecuting ? (
                      <span className="animate-pulse flex items-center gap-2">
                        <Loader2 className="w-5 h-5 animate-spin" />
                        正在深度解析...
                      </span>
                    ) : !canExecuteTasks ? (
                      <>
                        <TerminalSquare className="w-5 h-5" />
                        沙箱未就绪
                      </>
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
