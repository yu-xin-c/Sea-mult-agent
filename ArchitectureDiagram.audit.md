# ScholarAgent overview visual audit

Output: `ScholarAgentOverview.png`. Editable source: `ArchitectureDiagram.drawio`.

## Design goal

The README figure is a beginner-facing overview, not a complete component map. It answers four questions in reading order: what the user provides, how the system plans, where code really runs, and how results are accepted. Detailed interfaces remain in `scholar-agent/docs/project_architecture.md`.

## Style tokens

- Canvas: 1440 x 900, light gray background, no printed grid.
- Typography: Helvetica with Chinese fallback; title 31 px; step titles 18 px; body 14 px; supporting labels 12-13.5 px.
- Palette: navy text with blue input, purple AI, amber runtime, green verification, and neutral gray infrastructure.
- Cards: white, 8 px radius, restrained border and shadow.
- Connectors: four short left-to-right workflow arrows plus four internal DAG edges.

## Visible-element inventory

| ID | Region | Content | Medium | Status |
|---|---|---|---|---|
| header | top | Brand, title, subtitle, model/code boundary badge | native | accepted |
| zone_labels | y=143 | Input, AI collaboration and deterministic execution zones | native | accepted |
| step_1 | left flow card | Paper, repository, data, budget and acceptance inputs | native | accepted |
| step_2 | second flow card | Intent, frozen contract and validated DAG | native | accepted |
| step_2_dag | second card visual | Four-node DAG and label | native | accepted |
| step_3 | center flow card | Librarian, Research Coding, Python candidate ranking and domain Adapter | native | accepted |
| step_4 | fourth flow card | Docker execution, dependency install and bounded repair | native | accepted |
| step_4_terminal | fourth card visual | Real command execution metaphor | native | accepted |
| step_5 | right flow card | Evaluator, Keep/Reject, rollback and hidden holdout | native | accepted |
| step_5_metrics | fifth card visual | Improving metric bars and accepted result | native | accepted |
| main_arrows | between steps | Four short workflow transitions | native | accepted |
| trust_header | middle-lower | Explanation of the three safety boundaries | native | accepted |
| guard_scope | lower left | Commit, file, budget, command and evaluator boundary | native | accepted |
| guard_runtime | lower center | Persistence, leases, retry and SSE | native | accepted |
| guard_evidence | lower right | Repeated measurement, rollback, holdout and evidence | native | accepted |
| foundation | bottom band | React, Go control plane, Python Contextual-UCB, SQLite, Docker and Artifact base | native | accepted |
| footer | bottom | Benchmark and AutoResearch scope note | native | accepted |

## Arrow inventory

| ID | Source -> target | Meaning | Status |
|---|---|---|---|
| flow_12 | Research input -> executable plan | material becomes a bounded task | accepted |
| flow_23 | Executable plan -> specialist Agents | validated work is routed by capability | accepted |
| flow_34 | Specialist Agents -> sandbox run | proposals become bounded commands | accepted |
| flow_45 | Sandbox run -> evidence acceptance | real outputs are evaluated and verified | accepted |
| dag_ab / dag_ac / dag_bd / dag_cd | internal plan nodes | small editable DAG visual | accepted |

## Content boundaries

- The figure does not present Sandbox as a model Agent.
- Research Coding owns repository debugging and candidate generation; Python ranks only Go-approved candidates from bounded dataset features and validated history.
- Deterministic Go code owns writes, execution, scoring, rollback, budgets and final acceptance; optimizer failure falls back to FIFO.
- Native Docker is the current default; OpenSandbox is marked optional.
- Public evaluator search and model-invisible hidden holdout are shown as different stages.
- Configuration AutoResearch exposes a bounded result-driven candidate tree, but does not claim MCTS, Q-learning or a trained general RL policy.

## Final visual audit

- Full-size export: accepted after two render passes.
- Text clipping and overlap: accepted; all copy remains within its card or band.
- Connector routing: accepted; workflow arrows stay in the gaps between cards.
- README-scale readability: accepted at a 900 px preview, with the original available on click.
