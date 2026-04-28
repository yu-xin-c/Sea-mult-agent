# Ask User Question Tool 使用说明

这个目录里的 `ask_user_question` 工具用于实现“主动追问”能力。

它的目标不是直接回答用户，而是在 Agent 发现关键信息缺失时：

1. 创建一条待回答问题
2. 中断当前执行流程
3. 等宿主应用把用户答案传回来
4. 恢复执行，并把答案作为 tool 返回值交给上层 Agent

这和 Claude Code 里 `AskUserQuestion` 的工作方式是一致的：工具触发后暂停，等待宿主恢复，而不是继续让模型猜。

## 目录文件

- `ask_user_question.go`：工具实现、问题管理器、内存存储、恢复数据结构
- `ask_user_question_test.go`：基础单测

## 提供了什么

### 1. 可直接挂到 Eino 的 Tool

`AskUserQuestionTool` 实现了 Eino 的 `InvokableTool` 接口：

```go
tool := askuserquestion.NewAskUserQuestionTool(nil)
```

如果传 `nil`，内部会自动使用默认的内存存储。

### 2. 可被宿主直接操作的问题管理器

```go
manager := askuserquestion.NewAskUserQuestionManager(nil)
tool := askuserquestion.NewAskUserQuestionTool(manager)
```

你可以直接通过 `manager`：

- 创建问题
- 查询问题
- 查询 pending 问题
- 写入用户答案

### 3. 明确的接口结构

核心输入输出类型：

- `AskUserQuestionInput`
- `AnswerUserQuestionInput`
- `AskUserQuestionResume`
- `AskUserQuestionInterrupt`
- `UserQuestion`

这些类型已经预留好了，后面无论接 HTTP、SSE、WebSocket 还是前端轮询，都可以直接复用。

## 快速开始

### 1. 初始化

```go
package main

import (
	askuserquestion "scholar-agent-backend/internal/tools/ask_user_question"
)

func main() {
	manager := askuserquestion.NewAskUserQuestionManager(nil)
	askTool := askuserquestion.NewAskUserQuestionTool(manager)

	_ = askTool
	_ = manager
}
```

### 2. 让模型可调用这个 Tool

如果你在 Eino ChatModel 上启用了 tool calling，把下面这个工具加进去即可：

```go
askTool := askuserquestion.NewAskUserQuestionTool(nil)
toolInfo, _ := askTool.Info(ctx)

_ = toolInfo
```

如果你使用的是：

- `chatModel.BindTools(...)`
- `chatModel.WithTools(...)`
- `compose.ToolsNode`

都可以把这个工具接进去。

## 工具调用时的入参

模型调用 `ask_user_question` 时，建议传这种 JSON：

```json
{
  "session_id": "session-123",
  "task_id": "task-456",
  "agent": "coder_agent",
  "question": "你希望使用论文原始超参数还是快速验证配置？",
  "reason": "训练配置会直接影响后续代码生成和运行时长。",
  "context": "当前任务是复现训练脚本。",
  "required": true,
  "suggested_answers": [
    {
      "label": "原始配置",
      "value": "使用论文原始超参数"
    },
    {
      "label": "快速验证",
      "value": "使用快速验证配置"
    }
  ]
}
```

字段说明：

- `session_id`：宿主侧会话 ID，用于聚合同一轮里的待回答问题
- `task_id`：可选，关联任务 ID
- `agent`：可选，哪个 agent 在提问
- `question`：必须，要展示给用户的问题
- `reason`：为什么必须追问
- `context`：额外上下文
- `required`：是否必须回答
- `suggested_answers`：建议选项，方便前端渲染快捷回复

## 运行时行为

### 首次调用

当工具第一次被调用时：

1. `AskUserQuestionTool` 会把问题写入 `QuestionStore`
2. 生成一条 `pending` 状态的 `UserQuestion`
3. 触发 `tool.StatefulInterrupt(...)`
4. 当前 Agent 执行被暂停

中断时携带的 payload 类型是：

```go
type AskUserQuestionInterrupt struct {
    Type     string
    Question *UserQuestion
}
```

其中 `Type` 固定为：

```text
ask_user_question
```

宿主拿到这个中断后，应当把 `Question` 渲染给用户。

### 恢复执行

用户回答之后，宿主恢复执行时传入：

```go
type AskUserQuestionResume struct {
    QuestionID string
    Answer     string
    Metadata   map[string]string
}
```

恢复成功后，这个 tool 的返回值就是用户答案本身：

```text
使用快速验证配置
```

上层 Agent 可以把这个字符串继续拼回上下文，往后执行。

## 宿主侧建议流程

推荐按照下面的流程接入：

1. 初始化 `AskUserQuestionManager`
2. 初始化 `AskUserQuestionTool`
3. 把 tool 注册给模型或 `ToolsNode`
4. 当执行遇到 interrupt 时，读取 `AskUserQuestionInterrupt`
5. 把问题展示给前端
6. 用户回答后，宿主调用 `manager.Answer(...)` 或在 resume 时直接携带 `AskUserQuestionResume`
7. 恢复原先的 Agent / Graph 执行

## 直接操作 Manager 的示例

如果你暂时还没把它接进真正的 tool-calling，只想先把宿主能力跑通，可以直接操作 manager：

```go
ctx := context.Background()
manager := askuserquestion.NewAskUserQuestionManager(nil)

question, err := manager.Ask(ctx, askuserquestion.AskUserQuestionInput{
	SessionID: "session-1",
	TaskID:    "task-1",
	Agent:     "coder_agent",
	Question:  "请确认数据集下载地址",
	Reason:    "没有数据集地址无法继续生成训练脚本",
	Required:  true,
})
if err != nil {
	panic(err)
}

pending, err := manager.ListPending(ctx, "session-1")
if err != nil {
	panic(err)
}

_ = pending

answered, err := manager.Answer(ctx, question.ID, askuserquestion.AnswerUserQuestionInput{
	Answer: "https://example.com/dataset.zip",
})
if err != nil {
	panic(err)
}

_ = answered
```

这个模式适合你先把：

- 后端问题队列
- 前端提问弹窗
- 用户回复接口

先打通，再回头接 LLM 的 tool calling。

## 当前实现的限制

当前版本是“先把接口和核心逻辑立住”的实现，限制如下：

- `QuestionStore` 默认是内存实现，服务重启后问题会丢失
- 还没有接 HTTP API
- 还没有接现有 agent
- 还没有接前端 UI
- 还没有做问题过期、超时、取消机制

## 后续推荐接法

你下一步可以按这个顺序接：

1. 先加 API
2. 再让前端能展示 pending question 并提交 answer
3. 最后把某个 agent 改成真正走 tool calling

如果只想先验证闭环，最小方案是：

1. 后端暴露 `GET /questions/pending`
2. 后端暴露 `POST /questions/:id/answer`
3. 某个 agent 执行时捕获 interrupt
4. 用户回答后 resume