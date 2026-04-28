import type { Edge, Node } from '@xyflow/react';
import type { GraphTask, PlanGraph, Task } from '../../contracts/api';
import { getTaskStyleByStatus } from '../shared/agentVisuals';
import { createTaskNodeLabel } from './nodeLabelFactory';

export const graphTaskToTask = (task: GraphTask): Task => ({
  ID: task.id,
  Name: task.name,
  Type: task.type,
  Description: task.description,
  AssignedTo: task.assigned_to,
  Status: task.status,
  Dependencies: task.dependencies ?? [],
});

export const buildGraphLayout = (planGraph: PlanGraph): { nodes: Node[]; edges: Edge[] } => {
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

    const level = Math.max(
      ...task.dependencies.map((depId) => {
        const dep = tasksById[depId];
        return dep ? resolveLevel(dep) + 1 : 1;
      }),
    );
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
        x: 80 + level * 320,
        y: laneOffsets[laneKey] + stackIndex * 110,
      },
      data: {
        task: legacyTask,
        status: task.status,
        label: createTaskNodeLabel({
          assignedTo: task.assigned_to,
          taskName: task.name,
          status: task.status,
          level,
        }),
      },
      style: {
        borderRadius: '8px',
        backgroundColor: styleState.backgroundColor,
        border: '2px solid',
        borderColor: styleState.borderColor,
        boxShadow: '0 4px 6px -1px rgb(0 0 0 / 0.1)',
        cursor: 'pointer',
      },
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
        strokeDasharray: edge.type === 'data' ? '6 4' : undefined,
      },
      label: edge.type === 'data' ? 'data' : undefined,
    });
  });

  return { nodes: newNodes, edges: newEdges };
};
