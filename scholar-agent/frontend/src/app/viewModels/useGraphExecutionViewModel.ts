import { useMemo } from 'react';
import type { RefObject } from 'react';
import type { Edge, Node, OnEdgesChange, OnNodesChange } from '@xyflow/react';
import type { IntentContext } from '../../contracts/api';
import { useScholarRuntimeContext } from '../context/ScholarRuntimeContext';
import { uiText } from '../constants/uiText';
import type { useScholarLayoutState } from '../hooks/useScholarLayoutState';

interface UseGraphExecutionViewModelOptions {
  nodes: Node[];
  edges: Edge[];
  onNodesChange: OnNodesChange<Node>;
  onEdgesChange: OnEdgesChange<Edge>;
  intentContext: IntentContext | null;
  activePlanId: string | null;
  layout: ReturnType<typeof useScholarLayoutState>;
  logsEndRef: RefObject<HTMLDivElement | null>;
}

export function useGraphExecutionViewModel(options: UseGraphExecutionViewModelOptions) {
  const { nodes, edges, onNodesChange, onEdgesChange, intentContext, activePlanId, layout, logsEndRef } = options;
  const runtime = useScholarRuntimeContext();

  return useMemo(() => {
    const graphPanelProps = {
      nodes,
      edges,
      onNodesChange,
      onEdgesChange,
      onNodeClick: runtime.actions.onNodeClick,
      intentContext,
      runAllText: uiText.runAll,
      graphTitle: uiText.graphTitle,
      graphHint: uiText.graphHint,
      isExecuting: runtime.state.executionState.isExecuting,
      onRunAll: () => void runtime.actions.handleRunAllTasks(activePlanId),
    };

    const isExpandedMode = runtime.state.executionState.displayMode.endsWith('-expanded');
    const showExecutionResizeHandle =
      Boolean(runtime.state.executionState.selectedTask) && !isExpandedMode;

    const executionSidebarProps = runtime.state.executionState.selectedTask
      ? {
          selectedTask: runtime.state.executionState.selectedTask,
          width: isExpandedMode ? '100%' : `${layout.sidebarWidth}px`,
          isExecuting: runtime.state.executionState.isExecuting,
          displayMode: runtime.state.executionState.displayMode,
          executionLogs: runtime.state.selectedTaskState.logs,
          executionResult: runtime.state.selectedTaskState.result,
          executionCode: runtime.state.selectedTaskState.code,
          executionImage: runtime.state.selectedTaskState.imageBase64 || '',
          logsEndRef,
          onClose: runtime.actions.closeTaskPanel,
          onExecute: () =>
            void runtime.actions.handleExecuteTask(
              runtime.state.executionState.selectedTask as NonNullable<typeof runtime.state.executionState.selectedTask>,
            ),
          onChangeDisplayMode: runtime.actions.setDisplayMode,
        }
      : null;

    return {
      graphPanelProps,
      showExecutionResizeHandle,
      executionSidebarProps,
    };
  }, [activePlanId, edges, intentContext, layout.sidebarWidth, logsEndRef, nodes, onEdgesChange, onNodesChange, runtime]);
}
