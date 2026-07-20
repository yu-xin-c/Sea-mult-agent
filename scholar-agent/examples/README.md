# Examples

本目录提供可直接运行、带验收条件的 ScholarAgent 示例。日期化原始结果仍保留在
[`docs/experiments`](../docs/experiments/) 中，作为运行证据和审计记录。

| Example | Flow | Runtime | Status |
|---|---|---|---|
| [Attention paper reproduction](paper-reproduction/) | API -> Planner -> Scheduler -> Agents -> Docker Sandbox -> Artifacts | CPU smoke; GPU passthrough optional | Verified on 2026-07-17 |

示例用于验证产品执行链，不等同于论文的完整训练复现。每个示例都会明确说明运行边界、
成功条件和已验证结果。
