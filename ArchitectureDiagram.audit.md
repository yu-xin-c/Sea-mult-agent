# ArchitectureDiagram visual audit

Reference: the previous `ArchitectureDiagram.png`, updated against the current implementation described in `scholar-agent/docs/project_architecture.md`.

## Style tokens

- Export: 1828 x 1028 PNG on a clean white background; the Draw.io editor grid is intentionally not printed.
- Typography: Arial/Helvetica; title 34 px; section titles 18-20 px; card titles 15-17 px; body 12-14 px.
- Palette: navy `#123B78`, blue `#2F6BFF`, teal `#0F8B8D`, green `#1E8E5A`, amber `#E59B19`, purple `#6D5BD0`, ink `#18324A`, muted `#60758A`.
- Cards: white or lightly tinted fill, 1.5-2 px border, 8 px corner radius, minimal shadow.
- Connectors: solid teal for control, blue dashed for SSE, purple for Artifact/evidence, amber dashed for model suggestions.

## Visible-element inventory

| ID | Approximate region | Content / visual | Medium | Style notes | Status |
|---|---|---|---|---|---|
| canvas | 0,0,1828,1028 | Clean white canvas | native | no printed grid | accepted |
| brand | 28,24,300,56 | Sea-Mult-Agent wordmark and wave mark | native | navy, bold | accepted |
| title | 430,24,970,52 | ScholarAgent current system title | native | centered navy title | accepted |
| subtitle | 520,75,780,30 | Product scope subtitle | native | muted center text | accepted |
| input_panel | 24,126,220,700 | Researcher inputs and request types | native | blue border, three input cards | accepted |
| workbench_panel | 270,126,280,700 | React workbench, uploads, views and controls | native | blue tint, four cards | accepted |
| control_panel | 580,126,580,700 | Gin API, intent, Planner, PlanStore, Scheduler, Routed Executor | native | teal border and internal cards | accepted |
| agent_strip | 604,588,530,208 | Chat, Librarian, Coder, Research Coding, Data | native | five role cards; Research Coding highlighted | accepted |
| harness_panel | 1190,126,350,700 | Paper Debug, Benchmark and AutoResearch harnesses plus policy gate | native | green border; three harness cards | accepted |
| infra_panel | 1570,126,230,700 | LLM and repository sources, persistent state, events and observability | native | purple border, stacked cards | accepted |
| runtime_band | 270,852,1270,118 | docker-sandbox, Native Docker, optional OpenSandbox, workspace | native | light slate band | accepted |
| evidence_band | 1570,852,230,118 | TrialLedger, validation, evidence graph | native | purple tint | accepted |
| model_boundary | 1188,96,612,28 | Model proposes; deterministic code decides | native | amber callout | accepted |
| footer | 24,988,1776,22 | Current implementation boundary | native | muted small text | accepted |

## Arrow inventory

| ID | Source -> target | Type | Meaning | Status |
|---|---|---|---|---|
| flow_1 | input_panel -> workbench_panel | solid teal | user request and attachments | accepted |
| flow_2 | workbench_panel -> control_panel | solid teal | REST commands | accepted |
| flow_3 | control_panel -> workbench_panel | dashed blue | SSE events and state replay | accepted |
| flow_4 | API -> Intent Router -> Planner | solid teal | request interpretation and plan generation | accepted |
| flow_5 | Planner -> PlanGraph -> FilePlanStore <-> Scheduler | teal and purple | validated plan persistence and scheduler state | accepted |
| flow_6 | Scheduler -> Routed Task Executor | solid teal | leased typed task | accepted |
| flow_7 | Executor -> Artifact bus -> Research Coding | teal and purple | typed artifacts and specialist routing | accepted |
| flow_8 | Executor -> research harnesses | solid teal | typed task and artifact dispatch | accepted |
| flow_9 | Policy gate -> Sandbox Client -> docker-sandbox -> Native Docker | solid teal | bounded command execution | accepted |
| flow_10 | Container -> runtime output -> evidence | purple | stdout, metrics, files and auditable results | accepted |

## Content boundaries

- The production intent path is rule extraction plus Planner; BERT/Qwen is labelled optional rather than part of the default request path.
- `sandbox_agent` is shown as a logical role implemented by deterministic runtime methods, not as an independent model Agent.
- Native Docker is shown as the current default. OpenSandbox remains optional; CubeSandbox is not shown as implemented.
- AutoResearch uses a linear bounded TrialLedger, not tree search.
- Hidden holdout is described as model-invisible, not an independent zero-trust service.

## Final visual audit

- Full-size export inspected: accepted after two render passes.
- Text clipping / overlap: accepted; all card copy remains inside its container.
- Connector routing and arrowheads: accepted after removing nonessential cross-panel lines and routing the sandbox call below the main panels.
- README rendering at repository width: accepted; the overview remains scannable, with the full-size PNG available on click.
