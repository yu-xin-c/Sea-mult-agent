import { Bot, Code, Database, FileText, TerminalSquare } from 'lucide-react';

export const getAgentIcon = (agentName: string) => {
  switch (agentName) {
    case 'librarian_agent':
      return <FileText className="w-5 h-5 text-blue-500" />;
    case 'coder_agent':
      return <Code className="w-5 h-5 text-purple-500" />;
    case 'sandbox_agent':
      return <TerminalSquare className="w-5 h-5 text-orange-500" />;
    case 'data_agent':
      return <Database className="w-5 h-5 text-green-500" />;
    default:
      return <Bot className="w-5 h-5 text-gray-500" />;
  }
};

export const getTaskStyleByStatus = (status?: string) => {
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
