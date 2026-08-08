# AutoResearch Architecture Visual Audit

Target canvas: 1600 x 900, white and light-slate background, four-column README architecture figure.

| ID | Region | Content | Medium | Style | Status |
|---|---|---|---|---|---|
| header | top | Product label, title, subtitle and spec version | native SVG text and shapes | dark slate, cyan accent | accepted |
| orchestration | left column | User input, intent, fixed Planner, Scheduler and Artifact | native SVG | blue family | accepted |
| contract | center-left | ResearchSpec, file scope, hashes and budgets | native SVG | cyan with amber and green guards | accepted |
| execution | center-right | Baseline, candidate, policy, evaluator, decision, Keep/Reject and feedback loop | native SVG | multi-color decision states | accepted |
| evidence | right column | TrialLedger, 1-5 repeated validations, stability metrics, resource evidence and UI | native SVG | green with blue/cyan outputs | accepted |
| cross-connectors | between columns | freeze, authorize and emit transitions | native SVG paths | blue/green arrows | accepted |
| boundary | footer | Explicit trust boundary statement | native SVG | dark slate band | accepted |

Acceptance checks: no clipped text, no overlapping nodes or arrows, every connector has a visible destination, all four columns remain legible at README width, and the PNG export matches the SVG content.

Audit result: accepted after correcting the Reject-to-budget connector direction, moving the feedback loop origin to the budget node, and adding repeated-validation/resource labels. Verified at 1600 x 900; the current PNG export matches the SVG at that size.
