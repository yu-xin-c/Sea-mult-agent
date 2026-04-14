# IntentClassifier 设计与流程说明

本文档介绍 `internal/Intent/classifier.go` 的核心设计、调用流程、入参与出参，帮助你快速理解该模块如何把用户自然语言转换为可供下游 Planner 使用的结构化意图。

## 1. 模块目标

`IntentClassifier` 的目标是把用户输入的原始 query 做两件事：

1. **专业化重写**：在不改变语义的前提下，把表达改得更专业、更清晰。
2. **意图识别与实体抽取**：将 query 分类为预定义意图类型，并抽取实体、约束、置信度与原因。

最终输出统一为 `models.IntentContext`，作为下游规划器（Planner）的标准输入。

## 2. 主要组件

文件：`internal/Intent/classifier.go`

- `IntentClassifier`
  - 字段：
    - `enabled bool`：分类器是否可用。
    - `chatModel *openai.ChatModel`：大模型客户端。
- `llmClassifyResponse`
  - 解析“意图识别”返回 JSON。
- `llmRewriteResponse`
  - 解析“query 重写”返回 JSON。
- `systemPrompt`
  - 意图识别 prompt（包含意图定义、实体定义、few-shot）。
- `rewriteSystemPrompt`
  - query 重写 prompt（包含语义不变约束、few-shot）。

## 3. 启动与可用性

入口函数：`NewIntentClassifier()`

- 从环境变量读取：
  - `OPENAI_API_KEY`（必填）
  - `OPENAI_BASE_URL`（可选，默认 `https://api.deepseek.com/v1`）
  - `OPENAI_MODEL_NAME`（可选，默认 `deepseek-chat`）
- 若初始化失败，返回 `enabled=false` 的实例。

可用性判断：`Enabled()`

- 返回条件：实例非空、`enabled=true`、`chatModel!=nil`。

## 4. 主流程（Classify）

入口方法：

```go
func (c *IntentClassifier) Classify(ctx context.Context, rawQuery string) (models.IntentContext, error)
```

执行顺序：

1. 检查 `Enabled()`，不可用则直接报错。
2. 调用 `rewriteQuery(ctx, rawQuery)` 做专业化重写。
3. 若重写失败，回退到 `rawQuery`（不会中断主流程）。
4. 构造意图识别用户提示词（同时带原始 query 与重写 query）。
5. 调用 LLM 获取意图识别结果。
6. `parseLLMResponse` 解析 JSON。
7. `isValidIntentType` 校验意图类型合法性。
8. `normalizeEntities` 规范化实体（目前重点处理 `frameworks`）。
9. 组装并返回 `models.IntentContext`。

## 5. Query 重写流程（rewriteQuery）

方法：

```go
func (c *IntentClassifier) rewriteQuery(ctx context.Context, rawQuery string) (string, error)
```

特点：

- 使用单独的 `rewriteSystemPrompt`。
- 采用 few-shot 约束风格与输出格式。
- 输出必须是严格 JSON：

```json
{
  "rewritten_query": "..."
}
```

- 解析由 `parseRewriteResponse` 完成。
- 若 `rewritten_query` 为空，视为失败并返回错误。

## 6. 入参与出参

### 6.1 Classify 入参

- `ctx context.Context`
  - 用于请求生命周期控制、超时和取消。
- `rawQuery string`
  - 用户原始输入，作为语义源头。

### 6.2 Classify 出参

- `models.IntentContext`
  - 结构定义在 `internal/models/intent.go`：

```go
type IntentContext struct {
    RawIntent       string         `json:"raw_intent"`
    RewrittenIntent string         `json:"rewritten_intent,omitempty"`
    IntentType      string         `json:"intent_type"`
    Entities        map[string]any `json:"entities"`
    Constraints     map[string]any `json:"constraints"`
    Metadata        map[string]any `json:"metadata"`
    Confidence      float64        `json:"confidence,omitempty"`
    Reasoning       string         `json:"reasoning,omitempty"`
    Source          string         `json:"source,omitempty"` // "llm" or "rule_fallback"
}
```

字段含义：

- `RawIntent`：原始用户 query。
- `RewrittenIntent`：专业化重写后的 query（语义不变）。
- `IntentType`：主意图类型（四选一）：
  - `Framework_Evaluation`
  - `Paper_Reproduction`
  - `Code_Execution`
  - `General`
- `Entities`：从 query 中抽取出的结构化实体。
- `Constraints`：额外约束。
- `Metadata`：附加元信息（当前包含：
  - `normalized_intent`
  - `rewritten_intent`）
- `Confidence`：分类置信度。
- `Reasoning`：分类理由（简短）。
- `Source`：来源标记（当前为 `llm`）。

### 6.3 错误返回

- 当以下情况发生会返回 `error`：
  - 分类器不可用；
  - 意图识别 LLM 调用失败；
  - 意图识别 JSON 解析失败；
  - `intent_type` 缺失或非法。
- 重写失败不会导致 `Classify` 失败，会自动回退为 `rawQuery`。

## 7. 实体规范化规则

当前在 `normalizeEntities()` 里实现：

- 若 `entities["frameworks"]` 是 `[]any`：
  - 逐项转字符串；
  - 去空白并转小写；
  - 自动补齐 `framework_count`（若缺失）。
- 若是 `[]string`：
  - 同样统一为小写+去空白。

## 8. Prompt 设计要点

### 8.1 意图识别 Prompt（`systemPrompt`）

- 明确定义 4 类意图边界。
- 给出实体提取字典（键、类型、含义）。
- 规定严格 JSON 输出格式。
- 使用 10 个 few-shot 示例覆盖常见场景。
- 明确“多意图时选择核心意图”策略。

### 8.2 重写 Prompt（`rewriteSystemPrompt`）

- 强约束“语义不变”。
- 强约束“保留步骤顺序（先/再/然后/最后）”。
- 强约束“仅表达优化，不扩写需求”。
- few-shot 提升输出稳定性与专业术语一致性。

## 9. 对下游的价值

输出的 `IntentContext` 同时携带：

- 原始语义：`RawIntent`
- 专业表达：`RewrittenIntent`
- 结构化信息：`IntentType + Entities + Constraints`

这样下游既可以保留用户原始意图做追溯，又能用专业化表达提升任务规划与提示词生成质量。

