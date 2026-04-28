import { createContext, type PropsWithChildren, useContext } from 'react';
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
    setDisplayMode: ScholarRuntimeApi['setDisplayMode'];
    closeTaskPanel: ScholarRuntimeApi['closeTaskPanel'];
    resetRuntimeState: ScholarRuntimeApi['resetRuntimeState'];
  };
  meta: {
    appendSelectedTaskLog: ScholarRuntimeApi['appendSelectedTaskLog'];
  };
}

const ScholarRuntimeContext = createContext<ScholarRuntimeContextValue | null>(null);

interface ScholarRuntimeProviderProps extends PropsWithChildren {
  value: ScholarRuntimeContextValue;
}

export function ScholarRuntimeProvider({ value, children }: ScholarRuntimeProviderProps) {
  return <ScholarRuntimeContext.Provider value={value}>{children}</ScholarRuntimeContext.Provider>;
}

export function useScholarRuntimeContext() {
  const context = useContext(ScholarRuntimeContext);
  if (!context) {
    throw new Error('useScholarRuntimeContext must be used within ScholarRuntimeProvider');
  }
  return context;
}
