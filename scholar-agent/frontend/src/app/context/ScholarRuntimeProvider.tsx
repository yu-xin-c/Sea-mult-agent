import type { PropsWithChildren } from 'react';
import { ScholarRuntimeContext, type ScholarRuntimeContextValue } from './ScholarRuntimeContext';

interface ScholarRuntimeProviderProps extends PropsWithChildren {
  value: ScholarRuntimeContextValue;
}

export function ScholarRuntimeProvider({ value, children }: ScholarRuntimeProviderProps) {
  return <ScholarRuntimeContext.Provider value={value}>{children}</ScholarRuntimeContext.Provider>;
}
