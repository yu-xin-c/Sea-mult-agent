import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { AppErrorBoundary } from './app/AppErrorBoundary.tsx'
import { AppProviders } from './app/AppProviders.tsx'
import ScholarApp from './app/ScholarApp.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppErrorBoundary>
      <AppProviders>
        <ScholarApp />
      </AppProviders>
    </AppErrorBoundary>
  </StrictMode>,
)
