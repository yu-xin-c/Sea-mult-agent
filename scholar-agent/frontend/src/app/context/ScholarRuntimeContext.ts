import { createContext, useContext } from 'react';
import type { useScholarRuntime } from '../hooks/useScholarRuntime';

type ScholarRuntimeApi = ReturnType<typeof useScholarRuntime>;

export interface ScholarRuntimeContextValue {
  state: {
    executionState: ScholarRuntimeApi['executionState'];
    selectedTaskState: ScholarRuntimeApi['selectedTaskState'];
  };
  actions: {
    onNodeClick: ScholarRuntimeApi['onNodeClick'];
    handleOpenTaskView: ScholarRuntimeApi['handleOpenTaskView'];
    handleExecuteTask: ScholarRuntimeApi['handleExecuteTask'];
    handleRunAllTasks: ScholarRuntimeApi['handleRunAllTasks'];
	handleApproveAndRun: ScholarRuntimeApi['handleApproveAndRun'];
	handleCancelPlan: ScholarRuntimeApi['handleCancelPlan'];
	handleRetryFailedPlan: ScholarRuntimeApi['handleRetryFailedPlan'];
    setDisplayMode: ScholarRuntimeApi['setDisplayMode'];
    closeTaskPanel: ScholarRuntimeApi['closeTaskPanel'];
    resetRuntimeState: ScholarRuntimeApi['resetRuntimeState'];
  };
  meta: {
    appendSelectedTaskLog: ScholarRuntimeApi['appendSelectedTaskLog'];
  };
}

export const ScholarRuntimeContext = createContext<ScholarRuntimeContextValue | null>(null);

export function useScholarRuntimeContext() {
  const context = useContext(ScholarRuntimeContext);
  if (!context) {
    throw new Error('useScholarRuntimeContext must be used within ScholarRuntimeProvider');
  }
  return context;
}
