# Intent 优化 01：接入 LLM 意图识别 + 重写并行 + Redis 会话短期记忆

## 目标

- 接入 Go 版 LLM 意图识别能力，替换当前 `/api/plan` 里的纯规则识别主路径。
- 将“意图识别”和“Query 重写”改为并行执行，降低串行两次模型调用带来的时延。
- 真实接入用户与会话管理：由后端生成 `anon_user_id` 与 `session_id`，并通过 `HttpOnly Cookie` 持久化。
- 使用 Redis 存储 session 与消息记录，短期记忆从 Redis 拉取后注入 `PromptMemory`。
- 保留回退能力：LLM 或 Redis 故障时，自动回退到规则版 `inferIntentContextV2()`。

## 当前问题

截至目前，项目中 intent 相关链路存在几个明显缺口：

- `/api/plan` 仍然使用规则版 `inferIntentContextV2()`。
- `IntentClassifier` 已存在，但仍依赖 `mockPromptMemory()`，没有真实 session 和记忆来源。
- Query 重写与意图识别是串行执行，总耗时接近两次 LLM 调用之和。
- `/api/chat` 没有统一的会话标识，也没有把消息沉淀为短期记忆。

## 本次优化的核心设计

### 1. 用户与会话都由后端生成

采用后端统一生成方案：

- `anon_user_id`
  - 由后端首次请求时生成 UUID。
  - 使用 `HttpOnly Cookie` 写入浏览器。
  - 后续所有 `/api/chat`、`/api/plan` 请求都从 Cookie 中获取。

- `session_id`
  - 同样由后端生成 UUID。
  - 通过 `HttpOnly Cookie` 保存。
  - `/api/chat` 返回体中额外返回 `session_id`，便于前端调试和链路展示。
  - `/api/plan` 不需要前端显式传入，直接由后端从 Cookie 中读取。

### 2. Redis 负责保存短期记忆

Redis 中维护两类 key：

- `sa:intent:sess:{session_id}`
  - 类型：HASH
  - 字段：
    - `user_id`
    - `created_at`
    - `updated_at`

- `sa:intent:turns:{session_id}`
  - 类型：LIST
  - 元素：JSON 格式的 turn 记录

Turn 结构建议：

```json
{
  "role": "user|assistant",
  "content": "消息内容",
  "ts_ms": 1710000000000,
  "intent_type": "可选",
  "entities": {}
}
```

说明：

- `LPUSH + LTRIM` 保留最近 N 条消息，例如 30 条。
- 每次写入都会刷新 TTL，例如 7 天。
- 读取时按时间正序还原，供 prompt 拼接使用。

### 3. 短期记忆从 Redis 构造 PromptMemory

`PromptMemory` 不再使用 mock 数据，而是实时构建：

- `SessionID`：来自 Cookie 里的真实 `session_id`
- `RecentTurns`：来自 Redis 最近 K 条消息
- `UserProfile`：至少注入 `user_id`
- `Preferences`：先预留空对象
- `TopicHints`：先预留，后续可做关键词提取

### 4. 意图识别与重写改为并行

当前串行流程：

1. 先 rewrite
2. 再 classify

优化后流程：

1. 从 Redis 读取短期记忆
2. 用 `errgroup` 并行执行：
   - `ClassifyOnly(rawQuery, memory)`
   - `Rewrite(rawQuery, memory)`
3. 聚合结果返回统一的 `IntentContext`

这样做的收益：

- 降低 LLM 串行等待时间
- 保持 rewrite 与 classify 都能使用同一份短期记忆
- rewrite 失败时只回退到 `rawQuery`，不会影响分类主流程

## 路由接入方案

### `/api/chat`

作用：

- 确保 `anon_user_id` 和 `session_id` Cookie 存在
- 用户消息写入 Redis
- 助手回复写入 Redis
- 返回 `session_id` 与 `anon_user_id`

预期返回示例：

```json
{
  "response": "...",
  "session_id": "...",
  "anon_user_id": "..."
}
```

### `/api/plan`

新的 intent 推断顺序：

1. 后端从 Cookie 中读取 `anon_user_id/session_id`
2. 优先调用 `IntentClassifier.Classify(ctx, userID, sessionID, rawIntent)`
3. 若 LLM 不可用、超时、解析失败，则回退到 `inferIntentContextV2()`
4. 将 `session_id` 和 `anon_user_id` 写入 `intent_context.metadata`

规则回退时建议：

- `Source = "rule_fallback"`
- 保持 `RawIntent/IntentType/Entities/Constraints` 结构不变

## 失败策略

为了保证主链路可用，必须允许局部失败：

- Redis 初始化失败
  - memory store 自动降级为 noop
  - 不阻断 `/api/chat` 和 `/api/plan`

- Redis 读取失败
  - 使用空记忆继续调用 LLM

- Rewrite 失败
  - `RewrittenIntent = RawIntent`
  - 在 metadata 记录 `rewrite_error`

- LLM 分类失败
  - 自动回退规则版 `inferIntentContextV2()`
  - `Source = "rule_fallback"`

## 推荐环境变量

```bash
INTENT_LLM_ENABLED=true
INTENT_LLM_TIMEOUT=8s
INTENT_SESSION_TTL=168h
INTENT_TURNS_FETCH=10
INTENT_TURNS_MAX=30

REDIS_ADDR=127.0.0.1:6379
REDIS_USERNAME=
REDIS_PASSWORD=
REDIS_DB=0

INTENT_COOKIE_SAMESITE=lax
INTENT_COOKIE_SECURE=false
INTENT_COOKIE_DOMAIN=
INTENT_SESSION_COOKIE_MAX_AGE_SECONDS=604800
```

## 风险与注意事项

### 1. CORS 与 Cookie 兼容性

因为启用了 `Access-Control-Allow-Credentials: true`，`Access-Control-Allow-Origin` 不能继续固定为 `*`，否则浏览器不会带 Cookie。

因此后端需要：

- 动态回显请求 Origin
- 设置 `Vary: Origin`

### 2. Redis 中保存的是用户原始消息

这意味着：

- TTL 必须配置
- 生产环境需要 Redis ACL、内网访问限制
- 如有合规要求，应进一步做敏感信息脱敏

### 3. LLM 超时必须可控

建议意图识别链路设置独立超时，例如 `5s ~ 8s`，避免 `/api/plan` 被长时间阻塞。

## 本次改动的落地点

建议对应代码职责如下：

- `backend/internal/api/cookies.go`
  - 负责 `anon_user_id/session_id` Cookie 的生成与写入

- `backend/internal/Intent/memory_redis.go`
  - 负责 Redis session/turns 读写

- `backend/internal/Intent/classifier.go`
  - 拆分 `ClassifyOnly` 与 `Rewrite`
  - 使用 `errgroup` 并行执行
  - 从 Redis 加载短期记忆
  - 对外暴露 `RecordTurn` 供 chat 链路复用

- `backend/internal/api/routes.go`
  - `/api/chat` 写入 turn
  - `/api/plan` 优先走 LLM，失败回退规则

## 验收标准

- 首次请求后浏览器能拿到 `sa_uid` 与 `sa_sid` Cookie。
- `/api/chat` 可以把 user/assistant 消息写入 Redis。
- `/api/plan` 能从 Redis 拉取短期记忆并参与 LLM 意图识别。
- Query 重写与意图识别是并行执行，而不是串行。
- LLM/Redis 出现故障时，`/api/plan` 仍可通过规则版正常工作。

