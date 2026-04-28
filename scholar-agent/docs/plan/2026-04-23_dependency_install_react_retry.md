# 2026-04-23 依赖安装 ReAct 重试改造记录

本文记录 `coder_agent` 在 `install_dependencies` 阶段，从“纯规则纠错”升级为“模型参与的 ReAct 决策 + 规则兜底”的改造。

## 背景

在论文复现、仓库拉起、单节点重跑等场景里，`sandbox_agent` 会执行 `python3 -m pip install ...`。此前已经观察到两类高频失败：

- 标准库被误判为第三方依赖
  - 典型日志：`ERROR: No matching distribution found for shutil`
- 依赖与当前 Python 版本不兼容
  - 典型日志：`Ignored the following versions that require a different python version`
  - 或：`Requires-Python >=3.10 / >=3.11 / >=3.12`

这些错误只靠“原样重试”不会恢复，必须先分析错误，再决定下一步动作。

## 改造前

改造前的 `installDependencies()` 行为是：

1. 解析 `dependency_spec`
2. 做少量依赖清洗与 Python 3.9 兼容归一化
3. 调用一次 `pip install`
4. 如果失败：
   - 命中少量固定规则时尝试一次修复
   - 否则直接失败返回

特点：

- 优点：实现简单、可控、容易定位
- 缺点：只能覆盖提前写死的规则
- 问题：遇到新的 pip 错误模式时，需要人工再补一条规则

## 改造后

改造后的 `installDependencies()` 采用两层恢复策略：

1. 第一层：模型 ReAct 决策
2. 第二层：规则兜底

整体流程如下：

1. 先执行 `pip install`
2. 若安装成功，直接产出 `prepared_runtime` 和 `dependency_install_report`
3. 若安装失败：
   - 把当前依赖列表和 `pip` 错误日志喂给模型
   - 要求模型输出严格 JSON 修复动作
   - 后端按动作类型执行修复
   - 若模型未给出有效变化，再走规则兜底
4. 最多进行 3 轮尝试，避免无限循环

## 模型 ReAct 设计

新增了一个结构化修复计划：

```json
{
  "action": "remove_package",
  "reason": "一句话说明",
  "remove_package": "",
  "replace_package": "",
  "with_package": "",
  "target_image": "",
  "next_dependencies": []
}
```

当前支持的动作：

- `remove_package`
  - 适合标准库误判，例如 `shutil`
- `replace_package`
  - 适合明显包名错误或包名映射
- `upgrade_python`
  - 适合 `Requires-Python >=3.10/3.11/3.12`
- `rewrite_dependencies`
  - 适合需要整体重写依赖列表的情况
- `abort`
  - 模型认为不应继续自动修复

## 当前代码落点

核心改造集中在：

- [coder.go](file:///Users/bytedance/project/Sea-mult-agent/scholar-agent/backend/internal/agent/coder.go)

关键新增点：

- `CoderAgent.ChatModel`
  - 在初始化真实模型后保存句柄，供依赖恢复阶段复用
- `planDependencyRecovery()`
  - 把依赖列表和 pip 错误发给模型，解析其返回的 JSON 动作
- `applyDependencyRecoveryPlan()`
  - 执行模型给出的动作
- `applyRuleBasedDependencyFallback()`
  - 当模型无效、解析失败、或没有产生有效变更时，走固定规则兜底
- `recreateSandboxForDependencies()`
  - 当需要升级 Python 版本时，重建新的沙箱并切换 `runtime_session`

## 规则兜底仍然保留的原因

虽然本次增加了模型参与的 ReAct，但规则层没有删掉，原因如下：

- `shutil`、`pathlib`、`typing` 这类标准库误判属于高确定性场景
- `Requires-Python` 日志特征非常稳定，规则判断更快、更可控
- 当模型输出格式异常、返回空结果、或给出无效动作时，规则兜底可以保持系统稳定

因此当前策略是：

- 优先让模型决策
- 失败时再走规则
- 不让整个修复流程依赖单一模型输出

## 这次改造解决了什么

相较于改造前，现在可以覆盖两类能力：

- 已知错误的自动恢复
  - 例如 `shutil` 被误装
  - 例如 `Requires-Python >=3.10/3.11`
- 未知但可解释错误的模型分析
  - 模型可以基于 pip 日志判断“删哪个包、换哪个包、还是升级 Python”

这意味着后续遇到新的依赖安装失败，不必每次都先改 Go 规则，模型可以先尝试做最小修复。

## 风险与边界

当前实现仍然保留边界，避免“让模型随便改环境”：

- 模型只能输出有限动作，不允许任意执行 shell
- 自动重试次数上限为 3
- Python 升级只允许切换到预定义镜像：
  - `python:3.10-bullseye`
  - `python:3.11-bullseye`
  - `python:3.12-bullseye`
- 若模型输出无效 JSON 或无效动作，不会继续盲重试

## 建议的后续增强

如果后续继续演进，可以考虑：

- 为 `dependency_install_report` 增加“本次修复动作摘要”
- 把模型 ReAct 动作也写入任务日志或 artifact，便于前端展示
- 针对常见生态增加包映射知识：
  - `cv2 -> opencv-python`
  - `PIL -> pillow`
  - `sklearn -> scikit-learn`
- 如果未来默认沙箱升级到 Python 3.11，可下调部分 Python 3.9 兼容补丁的优先级
