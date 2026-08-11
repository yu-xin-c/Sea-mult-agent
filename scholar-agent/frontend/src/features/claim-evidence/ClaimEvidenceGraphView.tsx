import { useMemo, useState, type CSSProperties, type ReactNode } from 'react';
import { Background, Controls, MarkerType, Position, ReactFlow, type Edge, type Node } from '@xyflow/react';
import { AlertTriangle, CheckCircle2, CircleHelp, FileSearch, Link2, XCircle } from 'lucide-react';

type ClaimStatus =
  | 'verified'
  | 'partially_reproduced'
  | 'contradicted'
  | 'unverifiable'
  | 'blocked_by_missing_asset';

interface ClaimEvidenceNode {
  id: string;
  artifact_key: string;
  evidence_type: string;
  sha256?: string;
  available: boolean;
  summary?: string;
}

interface CriterionVerdict {
  criterion_id: string;
  description: string;
  status: ClaimStatus;
  confidence: number;
  observed_value?: string;
  evidence_ids: string[];
  reason: string;
}

interface ClaimResult {
  claim_id: string;
  title: string;
  statement: string;
  source_locator: string;
  claim_type: string;
  status: ClaimStatus;
  confidence: number;
  criteria: CriterionVerdict[];
}

interface ClaimEvidenceGraph {
  version: string;
  status: string;
  status_reason?: string;
  rubric_sha256: string;
  evidence: ClaimEvidenceNode[];
  claims: ClaimResult[];
  summary: {
    total_claims: number;
    total_criteria: number;
    verified: number;
    partially_reproduced: number;
    contradicted: number;
    unverifiable: number;
    blocked_by_missing_asset: number;
    criterion_evidence_coverage: number;
  };
}

interface VisualNodeDetail {
  kind: 'claim' | 'criterion' | 'evidence' | 'gap';
  title: string;
  status?: ClaimStatus;
  description: string;
  metadata: string[];
}

interface VisualNodeData extends Record<string, unknown> {
  label: ReactNode;
  detail: VisualNodeDetail;
}

interface ClaimEvidenceGraphViewProps {
  rawGraph: string;
  expanded?: boolean;
}

const statusConfig: Record<ClaimStatus, { label: string; color: string; background: string; border: string }> = {
  verified: { label: '已验证', color: '#166534', background: '#f0fdf4', border: '#86efac' },
  partially_reproduced: { label: '部分复现', color: '#92400e', background: '#fffbeb', border: '#fbbf24' },
  contradicted: { label: '与证据矛盾', color: '#991b1b', background: '#fef2f2', border: '#fca5a5' },
  unverifiable: { label: '不可验证', color: '#475569', background: '#f8fafc', border: '#cbd5e1' },
  blocked_by_missing_asset: { label: '缺少资产', color: '#075985', background: '#f0f9ff', border: '#7dd3fc' },
};

const nodeBaseStyle: CSSProperties = {
  borderRadius: 6,
  boxShadow: '0 3px 10px rgba(15, 23, 42, 0.08)',
  padding: 0,
  overflow: 'hidden',
  fontSize: 12,
};

const boundedPercent = (value: number) => `${Math.round(Math.max(0, Math.min(1, value || 0)) * 100)}%`;

const compactText = (value: string, limit: number) => {
  const text = String(value || '').trim();
  return text.length > limit ? `${text.slice(0, limit)}...` : text;
};

const isClaimEvidenceGraph = (value: unknown): value is ClaimEvidenceGraph => {
  if (!value || typeof value !== 'object') return false;
  const candidate = value as Partial<ClaimEvidenceGraph>;
  return (
    candidate.version === 'claim.evidence/v1' &&
    Array.isArray(candidate.claims) &&
    candidate.claims.every((claim) => Boolean(claim?.claim_id) && Array.isArray(claim?.criteria)) &&
    Array.isArray(candidate.evidence) &&
    candidate.evidence.every((evidence) => Boolean(evidence?.id) && Boolean(evidence?.artifact_key)) &&
    Boolean(candidate.summary) &&
    typeof candidate.summary?.total_claims === 'number' &&
    typeof candidate.summary?.total_criteria === 'number'
  );
};

const parseGraph = (rawGraph: string): ClaimEvidenceGraph | null => {
  try {
    const parsed = JSON.parse(rawGraph) as unknown;
    return isClaimEvidenceGraph(parsed) ? parsed : null;
  } catch {
    return null;
  }
};

const statusNodeLabel = (eyebrow: string, title: string, status: ClaimStatus, detail?: string) => {
  const config = statusConfig[status];
  return (
    <div className="w-full text-left" style={{ color: config.color }}>
      <div className="flex items-center justify-between gap-2 border-b px-3 py-2" style={{ borderColor: config.border }}>
        <span className="font-mono text-[10px] font-semibold">{eyebrow}</span>
        <span className="text-[10px] font-semibold">{config.label}</span>
      </div>
      <div className="px-3 py-2">
        <div className="break-words font-semibold leading-4">{compactText(title, 92)}</div>
        {detail && <div className="mt-1 text-[10px] leading-4 opacity-75">{detail}</div>}
      </div>
    </div>
  );
};

const evidenceNodeLabel = (evidence: ClaimEvidenceNode) => (
  <div className="w-full text-left text-cyan-950">
    <div className="flex items-center gap-2 border-b border-cyan-200 bg-cyan-50 px-3 py-2">
      <FileSearch className="h-3.5 w-3.5" />
      <span className="font-mono text-[10px] font-semibold">{evidence.evidence_type}</span>
    </div>
    <div className="px-3 py-2">
      <div className="break-all font-semibold leading-4">{evidence.artifact_key}</div>
      <div className="mt-1 font-mono text-[9px] text-cyan-700">{evidence.sha256 ? evidence.sha256.slice(0, 12) : 'no hash'}</div>
    </div>
  </div>
);

const gapNodeLabel = () => (
  <div className="w-full text-left text-slate-700">
    <div className="flex items-center gap-2 border-b border-dashed border-slate-300 bg-slate-50 px-3 py-2">
      <CircleHelp className="h-3.5 w-3.5" />
      <span className="text-[10px] font-semibold">证据缺口</span>
    </div>
    <div className="px-3 py-2 font-semibold">未引用可用证据</div>
  </div>
);

const buildVisualGraph = (graph: ClaimEvidenceGraph): { nodes: Node<VisualNodeData>[]; edges: Edge[] } => {
  const nodes: Node<VisualNodeData>[] = [];
  const edges: Edge[] = [];
  const referencedEvidence = new Map<string, { evidence: ClaimEvidenceNode; positions: number[] }>();
  const evidenceByID = new Map(graph.evidence.map((item) => [item.id, item]));
  let criterionRow = 0;

  for (const claim of graph.claims) {
    const claimCriterionPositions: number[] = [];
    for (const criterion of claim.criteria) {
      const y = 50 + criterionRow * 142;
      criterionRow += 1;
      claimCriterionPositions.push(y);
      const config = statusConfig[criterion.status] || statusConfig.unverifiable;
      nodes.push({
        id: criterion.criterion_id,
        position: { x: 360, y },
        sourcePosition: Position.Right,
        targetPosition: Position.Left,
        style: { ...nodeBaseStyle, width: 310, border: `1px solid ${config.border}`, background: config.background },
        data: {
          label: statusNodeLabel(criterion.criterion_id, criterion.description, criterion.status, `置信度 ${boundedPercent(criterion.confidence)}`),
          detail: {
            kind: 'criterion',
            title: criterion.criterion_id,
            status: criterion.status,
            description: criterion.reason || criterion.description,
            metadata: [
              `准则：${criterion.description}`,
              `置信度：${boundedPercent(criterion.confidence)}`,
              criterion.observed_value ? `观测值：${criterion.observed_value}` : '观测值：未提供',
            ],
          },
        },
      });
      edges.push({
        id: `${claim.claim_id}-${criterion.criterion_id}`,
        source: claim.claim_id,
        target: criterion.criterion_id,
        markerEnd: { type: MarkerType.ArrowClosed, color: '#94a3b8' },
        style: { stroke: '#94a3b8', strokeWidth: 1.5 },
      });

      if (criterion.evidence_ids.length === 0) {
        const gapID = `gap-${criterion.criterion_id}`;
        referencedEvidence.set(gapID, {
          evidence: { id: gapID, artifact_key: 'missing_evidence', evidence_type: 'gap', available: false },
          positions: [y],
        });
        edges.push({
          id: `${criterion.criterion_id}-${gapID}`,
          source: criterion.criterion_id,
          target: gapID,
          animated: true,
          markerEnd: { type: MarkerType.ArrowClosed, color: config.color },
          style: { stroke: config.color, strokeDasharray: '5 4', strokeWidth: 1.5 },
        });
      } else {
        for (const evidenceID of criterion.evidence_ids) {
          const evidence = evidenceByID.get(evidenceID);
          if (!evidence) continue;
          const existing = referencedEvidence.get(evidenceID);
          if (existing) existing.positions.push(y);
          else referencedEvidence.set(evidenceID, { evidence, positions: [y] });
          edges.push({
            id: `${criterion.criterion_id}-${evidenceID}`,
            source: criterion.criterion_id,
            target: evidenceID,
            markerEnd: { type: MarkerType.ArrowClosed, color: config.color },
            style: { stroke: config.color, strokeWidth: 1.8 },
          });
        }
      }
    }

    const claimY = claimCriterionPositions.length > 0
      ? claimCriterionPositions.reduce((sum, position) => sum + position, 0) / claimCriterionPositions.length
      : 50;
    const claimConfig = statusConfig[claim.status] || statusConfig.unverifiable;
    nodes.push({
      id: claim.claim_id,
      type: 'input',
      position: { x: 20, y: claimY },
      sourcePosition: Position.Right,
      style: { ...nodeBaseStyle, width: 230, border: `1px solid ${claimConfig.border}`, background: claimConfig.background },
      data: {
        label: statusNodeLabel(claim.claim_id, claim.title, claim.status, claim.claim_type),
        detail: {
          kind: 'claim',
          title: claim.title,
          status: claim.status,
          description: claim.statement,
          metadata: [
            `来源：${claim.source_locator || '未标注'}`,
            `类型：${claim.claim_type}`,
            `置信度：${boundedPercent(claim.confidence)}`,
          ],
        },
      },
    });
  }

  const evidencePlacements = [...referencedEvidence.entries()]
    .map(([id, value]) => ({
      id,
      ...value,
      desiredY: value.positions.reduce((sum, position) => sum + position, 0) / value.positions.length,
    }))
    .sort((left, right) => left.desiredY - right.desiredY);
  let previousY = -90;
  for (const placement of evidencePlacements) {
    const y = Math.max(placement.desiredY, previousY + 112);
    previousY = y;
    const isGap = placement.evidence.evidence_type === 'gap';
    nodes.push({
      id: placement.id,
      type: 'output',
      position: { x: 800, y },
      targetPosition: Position.Left,
      style: {
        ...nodeBaseStyle,
        width: 225,
        border: isGap ? '1px dashed #94a3b8' : '1px solid #67e8f9',
        background: isGap ? '#f8fafc' : '#ecfeff',
      },
      data: {
        label: isGap ? gapNodeLabel() : evidenceNodeLabel(placement.evidence),
        detail: isGap
          ? {
              kind: 'gap',
              title: '未引用可用证据',
              description: '该准则没有引用可用的运行、指标、对照或图表证据，因此不能被判定为已验证。',
              metadata: [],
            }
          : {
              kind: 'evidence',
              title: placement.evidence.artifact_key,
              description: placement.evidence.summary || '已记录产物，但没有摘要。',
              metadata: [
                `类型：${placement.evidence.evidence_type}`,
                `SHA-256：${placement.evidence.sha256 || '未记录'}`,
              ],
            },
      },
    });
  }

  return { nodes, edges };
};

const DetailIcon = ({ detail }: { detail: VisualNodeDetail }) => {
  if (detail.kind === 'evidence') return <Link2 className="h-4 w-4 text-cyan-700" />;
  if (detail.kind === 'gap') return <CircleHelp className="h-4 w-4 text-slate-500" />;
  if (detail.status === 'verified') return <CheckCircle2 className="h-4 w-4 text-green-700" />;
  if (detail.status === 'contradicted') return <XCircle className="h-4 w-4 text-red-700" />;
  return <AlertTriangle className="h-4 w-4 text-amber-700" />;
};

export function ClaimEvidenceGraphView({ rawGraph, expanded = false }: ClaimEvidenceGraphViewProps) {
  const graph = useMemo(() => parseGraph(rawGraph), [rawGraph]);
  const visualGraph = useMemo(() => (graph ? buildVisualGraph(graph) : { nodes: [], edges: [] }), [graph]);
  const [selectedNodeID, setSelectedNodeID] = useState<string>('');

  if (!graph) {
    return (
      <div className="flex h-full items-center justify-center border border-red-200 bg-red-50 px-6 text-center text-sm text-red-700">
        证据图数据无法解析，请查看分析报告或重新运行该节点。
      </div>
    );
  }

  const selectedDetail = visualGraph.nodes.find((node) => node.id === selectedNodeID)?.data.detail;
  const summaryItems: Array<{ label: string; value: number; status: ClaimStatus }> = [
    { label: '已验证', value: graph.summary.verified, status: 'verified' },
    { label: '部分复现', value: graph.summary.partially_reproduced, status: 'partially_reproduced' },
    { label: '矛盾', value: graph.summary.contradicted, status: 'contradicted' },
    { label: '不可验证', value: graph.summary.unverifiable, status: 'unverifiable' },
    { label: '缺资产', value: graph.summary.blocked_by_missing_asset, status: 'blocked_by_missing_asset' },
  ];

  return (
    <div className="flex h-full min-h-0 flex-col bg-white">
      <div className="grid grid-cols-5 border-b border-slate-200 bg-slate-50">
        {summaryItems.map((item) => (
          <div key={item.status} className="border-r border-slate-200 px-2 py-2 text-center last:border-r-0">
            <div className="text-sm font-semibold" style={{ color: statusConfig[item.status].color }}>{item.value}</div>
            <div className="text-[10px] text-slate-500">{item.label}</div>
          </div>
        ))}
      </div>
      <div className="flex items-center justify-between border-b border-slate-200 px-3 py-2 text-[11px] text-slate-600">
        <span>{graph.summary.total_claims} 个主张 · {graph.summary.total_criteria} 个准则</span>
        <span>Rubric {(graph.rubric_sha256 || 'unknown').slice(0, 8)} · 证据覆盖 {boundedPercent(graph.summary.criterion_evidence_coverage)}</span>
      </div>
      {graph.status !== 'assessed' && (
        <div className="border-b border-amber-200 bg-amber-50 px-3 py-2 text-[11px] leading-4 text-amber-900">
          判定降级：{graph.status_reason || '部分准则没有获得完整证据判定。'}
        </div>
      )}
      <div className={`relative min-h-0 flex-1 ${expanded ? 'min-h-[520px]' : 'min-h-[320px]'}`}>
        <div className="pointer-events-none absolute inset-x-0 top-0 z-10 grid grid-cols-3 border-b border-slate-200 bg-white/90 px-4 py-2 text-center text-[10px] font-semibold text-slate-500 backdrop-blur-sm">
          <span>论文主张</span>
          <span>独立判定准则</span>
          <span>运行证据</span>
        </div>
        <ReactFlow
          key={`${graph.rubric_sha256}-${expanded ? 'expanded' : 'compact'}`}
          nodes={visualGraph.nodes}
          edges={visualGraph.edges}
          fitView
          fitViewOptions={{ padding: 0.16, minZoom: expanded ? 0.45 : 0.25, maxZoom: 1 }}
          minZoom={0.2}
          maxZoom={1.5}
          nodesDraggable={false}
          nodesConnectable={false}
          onNodeClick={(_, node) => setSelectedNodeID(node.id)}
          proOptions={{ hideAttribution: true }}
        >
          <Background color="#cbd5e1" gap={20} size={1} />
          <Controls showInteractive={false} position="bottom-right" />
        </ReactFlow>
      </div>
      {selectedDetail && (
        <div className="max-h-40 overflow-y-auto border-t border-slate-200 bg-white px-4 py-3 text-xs text-slate-700">
          <div className="flex items-center gap-2 font-semibold text-slate-900">
            <DetailIcon detail={selectedDetail} />
            <span className="break-all">{selectedDetail.title}</span>
          </div>
          <p className="mt-2 break-words leading-5">{selectedDetail.description}</p>
          {selectedDetail.metadata.map((item) => <div key={item} className="mt-1 break-all text-[11px] text-slate-500">{item}</div>)}
        </div>
      )}
    </div>
  );
}
