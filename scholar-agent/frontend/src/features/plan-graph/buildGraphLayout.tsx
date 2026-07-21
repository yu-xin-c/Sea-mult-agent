import { MarkerType, Position, type Edge, type Node } from '@xyflow/react';
import type { GraphTask, PlanGraph, Task } from '../../contracts/api';
import { getTaskStyleByStatus } from '../shared/agentVisuals';
import { createTaskNodeLabel } from './nodeLabelFactory';

const NODE_WIDTH = 216;
const NODE_HEIGHT = 92;

const getCompactColumnCount = () => (typeof window !== 'undefined' && window.innerWidth < 640 ? 2 : 3);

const pairKey = (from: string, to: string) => `${from}->${to}`;

const selectVisibleEdges = (planGraph: PlanGraph) => {
  const controlEdges = planGraph.edges.filter((edge) => edge.type === 'control');
  const controlPairs = new Set(controlEdges.map((edge) => pairKey(edge.from, edge.to)));
  const adjacency = controlEdges.reduce<Record<string, string[]>>((result, edge) => {
    result[edge.from] = [...(result[edge.from] ?? []), edge.to];
    return result;
  }, {});

  const hasControlPath = (from: string, to: string) => {
    const queue = [...(adjacency[from] ?? [])];
    const visited = new Set<string>();
    while (queue.length > 0) {
      const current = queue.shift();
      if (!current || visited.has(current)) continue;
      if (current === to) return true;
      visited.add(current);
      queue.push(...(adjacency[current] ?? []));
    }
    return false;
  };

  const dataEdges = planGraph.edges.filter(
    (edge) =>
      edge.type === 'data' &&
      !controlPairs.has(pairKey(edge.from, edge.to)) &&
      !hasControlPath(edge.from, edge.to),
  );

  return [...controlEdges, ...dataEdges].filter(
    (edge, index, edges) =>
      edges.findIndex((candidate) => pairKey(candidate.from, candidate.to) === pairKey(edge.from, edge.to)) === index,
  );
};

export const graphTaskToTask = (task: GraphTask): Task => ({
  ID: task.id,
  Name: task.name,
  Type: task.type,
  Description: task.description,
  AssignedTo: task.assigned_to,
  Status: task.status,
  Dependencies: task.dependencies ?? [],
  Inputs: task.inputs,
  RequiredArtifacts: task.required_artifacts,
  OutputArtifacts: task.output_artifacts,
  Result: task.result,
  Code: task.code,
  StructuredData: task.structured_data,
  ImageBase64: task.image_base64 || task.image_base_64,
});

export const buildGraphLayout = (planGraph: PlanGraph): { nodes: Node[]; edges: Edge[] } => {
  const newNodes: Node[] = [];
  const newEdges: Edge[] = [];

  const levelMap: Record<string, number> = {};
  const laneOrder = ['librarian_agent', 'coder_agent', 'research_coding_agent', 'sandbox_agent', 'data_agent', 'general_agent'];
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

  const maxLevel = sortedTasks.reduce((max, task) => Math.max(max, resolveLevel(task)), 0);
  const tasksPerLevel = sortedTasks.reduce<Record<number, number>>((counts, task) => {
    const level = resolveLevel(task);
    counts[level] = (counts[level] || 0) + 1;
    return counts;
  }, {});
  const maxTasksInLevel = Math.max(...Object.values(tasksPerLevel), 1);
  const useCompactLongChain = maxLevel >= 5 && maxTasksInLevel <= 2;
  const compactColumns = getCompactColumnCount();
  const compactStackGap = 112;
  const compactRowHeight = maxTasksInLevel > 1 ? 260 : 148;

  const levelCounts: Record<number, number> = {};
  sortedTasks.forEach((task, taskIndex) => {
    const level = resolveLevel(task);
    const stackIndex = levelCounts[level] || 0;
    levelCounts[level] = stackIndex + 1;
    const legacyTask = graphTaskToTask(task);
    const styleState = getTaskStyleByStatus(task.status);

    let position = {
      x: 48 + level * 276,
      y: 48 + (stackIndex + (maxTasksInLevel - tasksPerLevel[level]) / 2) * 132,
    };
    let sourcePosition = Position.Right;
    let targetPosition = Position.Left;

    if (useCompactLongChain) {
      const row = Math.floor(level / compactColumns);
      const naturalColumn = level % compactColumns;
      const column = row % 2 === 0 ? naturalColumn : compactColumns - naturalColumn - 1;
      const startsNewRow = naturalColumn === 0 && row > 0;
      const endsRow = naturalColumn === compactColumns - 1 && level < maxLevel;
      const levelStackOffset = ((maxTasksInLevel - tasksPerLevel[level]) * compactStackGap) / 2;

      position = {
        x: 48 + column * 268,
        y: 48 + row * compactRowHeight + levelStackOffset + stackIndex * compactStackGap,
      };
      targetPosition = startsNewRow ? Position.Top : row % 2 === 0 ? Position.Left : Position.Right;
      sourcePosition = endsRow ? Position.Bottom : row % 2 === 0 ? Position.Right : Position.Left;
    }

    newNodes.push({
      id: task.id,
      position,
      sourcePosition,
      targetPosition,
      className: 'scholar-task-node',
      data: {
        task: legacyTask,
        status: task.status,
        step: taskIndex + 1,
        label: createTaskNodeLabel({
          assignedTo: task.assigned_to,
          taskName: task.name,
          status: task.status,
          step: taskIndex + 1,
        }),
      },
      style: {
        borderRadius: '8px',
        backgroundColor: styleState.backgroundColor,
        border: '1px solid',
        borderColor: styleState.borderColor,
        boxShadow: '0 4px 14px rgb(15 23 42 / 0.07)',
        cursor: 'pointer',
        overflow: 'hidden',
        padding: 0,
        width: NODE_WIDTH,
        minHeight: NODE_HEIGHT,
      },
    });
  });

  selectVisibleEdges(planGraph).forEach((edge) => {
    const targetStatus = tasksById[edge.to]?.status;
    const isDataEdge = edge.type === 'data';
    newEdges.push({
      id: edge.id,
      source: edge.from,
      target: edge.to,
      type: 'smoothstep',
      animated: targetStatus === 'in_progress',
      style: {
        stroke: isDataEdge ? '#8b5cf6' : '#94a3b8',
        strokeWidth: isDataEdge ? 1.5 : 2,
        strokeDasharray: isDataEdge ? '5 5' : undefined,
      },
      markerEnd: {
        type: MarkerType.ArrowClosed,
        color: isDataEdge ? '#8b5cf6' : '#94a3b8',
        width: 16,
        height: 16,
      },
    });
  });

  return { nodes: newNodes, edges: newEdges };
};
