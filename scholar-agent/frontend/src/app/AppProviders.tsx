import type { PropsWithChildren } from 'react';

// 全局上下文/依赖注入层 留着以后拓展用
export function AppProviders({ children }: PropsWithChildren) {
  return <>{children}</>;
}
