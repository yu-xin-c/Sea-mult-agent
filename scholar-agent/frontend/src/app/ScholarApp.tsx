import { type ReactNode, type RefObject, useCallback, useEffect, useMemo, useRef } from 'react';
import { useEdgesState, useNodesState } from '@xyflow/react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import 'katex/dist/katex.min.css';
import '@react-pdf-viewer/core/lib/styles/index.css';
import '@react-pdf-viewer/default-layout/lib/styles/index.css';

import { LeftWorkspace } from './components/LeftWorkspace';
import { ScholarRuntimeProvider, useScholarRuntimeContext, type ScholarRuntimeContextValue } from './context/ScholarRuntimeContext';
import { usePdfAssistFlow } from './hooks/usePdfAssistFlow';
import { useScholarChatFlow } from './hooks/useScholarChatFlow';
import { useScholarLayoutState } from './hooks/useScholarLayoutState';
import { useScholarRuntime } from './hooks/useScholarRuntime';
import { useGraphExecutionViewModel } from './viewModels/useGraphExecutionViewModel';
import { ExecutionSidebar } from '../features/execution/ExecutionSidebar';
import { GraphPanel } from '../features/plan-graph/GraphPanel';
import { buildGraphLayout } from '../features/plan-graph/buildGraphLayout';
import type { IntentContext } from '../contracts/api';
import { getPdfProxyUrl } from '../services/api/scholarApi';

interface ScholarAppShellProps {
  layout: ReturnType<typeof useScholarLayoutState>;
  leftWorkspace: ReactNode;
  graphExecutionViewModel: ReturnType<typeof useGraphExecutionViewModel>;
}

function ScholarAppShell(props: ScholarAppShellProps) {
  const { layout, leftWorkspace, graphExecutionViewModel } = props;

  return (
    <div className="flex h-screen bg-gray-100 font-sans overflow-hidden">
      {leftWorkspace}

      <div
        className={`w-1.5 bg-gray-200 hover:bg-blue-400 cursor-col-resize z-20 transition-colors flex items-center justify-center ${layout.isResizing ? 'bg-blue-500' : ''}`}
        onMouseDown={layout.startResizingLeftPanel}
      >
        <div className="h-8 w-1 bg-gray-400 rounded-full" />
      </div>

      <div className="flex-1 relative flex overflow-hidden">
        <GraphPanel {...graphExecutionViewModel.graphPanelProps} />

        {graphExecutionViewModel.showExecutionResizeHandle && (
          <div
            className={`w-1 bg-gray-200 hover:bg-blue-400 cursor-col-resize z-20 transition-colors flex items-center justify-center ${layout.isResizingSidebar ? 'bg-blue-500' : ''}`}
            onMouseDown={layout.startResizingSidebar}
          >
            <div className="h-8 w-0.5 bg-gray-400 rounded-full" />
          </div>
        )}

        {graphExecutionViewModel.executionSidebarProps && <ExecutionSidebar {...graphExecutionViewModel.executionSidebarProps} />}
      </div>
    </div>
  );
}

interface ScholarWorkspaceContentProps {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: OnNodesChange<Node>;
  onEdgesChange: OnEdgesChange<Edge>;
  logsEndRef: RefObject<HTMLDivElement | null>;
  layout: ReturnType<typeof useScholarLayoutState>;
  chatFlow: {
    chatHistory: ReturnType<typeof useScholarChatFlow>['chatHistory'];
    loading: boolean;
    prompt: string;
    setPrompt: (value: string) => void;
    handleSendMessage: () => void;
    intentContext: IntentContext | null;
    activePlanId: string | null;
    isLoggedIn: boolean;
    userId: string | null;
    loginInput: string;
    setLoginInput: (value: string) => void;
    activeSessionId: string | null;
    sessionSummaries: ReturnType<typeof useScholarChatFlow>['sessionSummaries'];
    handleLogin: () => void;
    handleCreateSession: () => void;
    handleSwitchSession: (sessionId: string) => void;
  };
  pdfFlow: ReturnType<typeof usePdfAssistFlow>;
}

function ScholarWorkspaceContent(props: ScholarWorkspaceContentProps) {
  const { nodes, edges, onNodesChange, onEdgesChange, logsEndRef, layout, chatFlow, pdfFlow } = props;
  const runtime = useScholarRuntimeContext();
  const graphExecutionViewModel = useGraphExecutionViewModel({
    nodes,
    edges,
    onNodesChange,
    onEdgesChange,
    intentContext: chatFlow.intentContext,
    activePlanId: chatFlow.activePlanId,
    layout,
    logsEndRef,
  });

  const leftWorkspace = pdfFlow.pdfUrl ? (
    <LeftWorkspace.Pdf
      widthPercent={layout.leftPanelWidth}
      pdfUrl={pdfFlow.pdfUrl}
      isFullTranslating={pdfFlow.isFullTranslating}
      onClosePdf={() => pdfFlow.setPdfUrl(null)}
      onFullTranslation={pdfFlow.handleFullTranslation}
      defaultLayoutPluginInstance={pdfFlow.defaultLayoutPluginInstance}
      aiTranslationPluginInstance={pdfFlow.aiTranslationPluginInstance}
    />
  ) : (
    <LeftWorkspace.Chat
      widthPercent={layout.leftPanelWidth}
      state={{
        chatHistory: chatFlow.chatHistory,
        loading: chatFlow.loading,
        prompt: chatFlow.prompt,
        showSuggestions: pdfFlow.showSuggestions,
        isLoggedIn: chatFlow.isLoggedIn,
        userId: chatFlow.userId,
        loginInput: chatFlow.loginInput,
        activeSessionId: chatFlow.activeSessionId,
        sessions: chatFlow.sessionSummaries,
      }}
      chatActions={{
        setPrompt: chatFlow.setPrompt,
        setShowSuggestions: pdfFlow.setShowSuggestions,
        onSendMessage: chatFlow.handleSendMessage,
        setLoginInput: chatFlow.setLoginInput,
        onLogin: chatFlow.handleLogin,
        onCreateSession: chatFlow.handleCreateSession,
        onSwitchSession: chatFlow.handleSwitchSession,
      }}
      pdfActions={{
        onOpenPdf: () => pdfFlow.setPdfUrl(getPdfProxyUrl('https://arxiv.org/pdf/1706.03762.pdf')),
        onClosePdf: () => pdfFlow.setPdfUrl(null),
        onFullTranslation: pdfFlow.handleFullTranslation,
      }}
      taskActions={{
        onOpenTaskView: runtime.actions.handleOpenTaskView,
      }}
    />
  );

  return <ScholarAppShell layout={layout} leftWorkspace={leftWorkspace} graphExecutionViewModel={graphExecutionViewModel} />;
}

export default function ScholarApp() {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const logsEndRef = useRef<HTMLDivElement>(null);
  const layout = useScholarLayoutState();

  const handlePlanGraphChanged = useCallback(
    (planGraph: ReturnType<typeof useScholarChatFlow>['intentContext'] extends never ? never : Parameters<Parameters<typeof useScholarChatFlow>[0]['onPlanGraphChanged']>[0]) => {
      if (!planGraph) {
        setNodes([]);
        setEdges([]);
        return;
      }

      const graphLayout = buildGraphLayout(planGraph);
      setNodes(graphLayout.nodes);
      setEdges(graphLayout.edges);
    },
    [setEdges, setNodes],
  );

  const chatFlow = useScholarChatFlow({
    onPlanGraphChanged: handlePlanGraphChanged,
  });

  const runtime = useScholarRuntime({
    nodes,
    setNodes,
    appendChatMessage: chatFlow.appendChatMessage,
  });

  const pdfFlow = usePdfAssistFlow({
    setPrompt: chatFlow.setPrompt,
    appendChatMessage: chatFlow.appendChatMessage,
    appendSelectedTaskLog: runtime.appendSelectedTaskLog,
  });

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [runtime.selectedTaskState.logs]);

  useEffect(() => {
    runtime.resetRuntimeState();
  }, [chatFlow.activeSessionId, runtime.resetRuntimeState]);

  const runtimeContextValue = useMemo<ScholarRuntimeContextValue>(
    () => ({
      state: {
        executionState: runtime.executionState,
        selectedTaskState: runtime.selectedTaskState,
      },
      actions: {
        onNodeClick: runtime.onNodeClick,
        handleOpenTaskView: runtime.handleOpenTaskView,
        handleExecuteTask: runtime.handleExecuteTask,
        handleRunAllTasks: runtime.handleRunAllTasks,
        setDisplayMode: runtime.setDisplayMode,
        closeTaskPanel: runtime.closeTaskPanel,
        resetRuntimeState: runtime.resetRuntimeState,
      },
      meta: {
        appendSelectedTaskLog: runtime.appendSelectedTaskLog,
      },
    }),
    [runtime],
  );

  return (
    <ScholarRuntimeProvider value={runtimeContextValue}>
      <ScholarWorkspaceContent
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        logsEndRef={logsEndRef}
        layout={layout}
        chatFlow={{
          chatHistory: chatFlow.chatHistory,
          loading: chatFlow.loading,
          prompt: chatFlow.prompt,
          setPrompt: chatFlow.setPrompt,
          handleSendMessage: chatFlow.handleSendMessage,
          intentContext: chatFlow.intentContext,
          activePlanId: chatFlow.activePlanId,
          isLoggedIn: chatFlow.isLoggedIn,
          userId: chatFlow.userId,
          loginInput: chatFlow.loginInput,
          setLoginInput: chatFlow.setLoginInput,
          activeSessionId: chatFlow.activeSessionId,
          sessionSummaries: chatFlow.sessionSummaries,
          handleLogin: chatFlow.handleLogin,
          handleCreateSession: chatFlow.handleCreateSession,
          handleSwitchSession: chatFlow.handleSwitchSession,
        }}
        pdfFlow={pdfFlow}
      />
    </ScholarRuntimeProvider>
  );
}
