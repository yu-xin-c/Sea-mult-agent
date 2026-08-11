# BERT/BGE CPU 意图分类器：结构与原理

本文解释 ScholarAgent 的 CPU 意图分类方案为什么采用 BERT 类编码器、模型内部如何处理一句话，以及微调时究竟更新了什么。

双模型整体方案见 [02_dual_model_finetuning_guide.md](02_dual_model_finetuning_guide.md)。

## 1. BERT 解决什么问题

意图分类是一个判别任务：输入一句话，从有限标签中选择最合适的一类。

```text
输入：复现这篇论文，并运行三组轻量消融
输出：Paper_Reproduction
```

BERT 不负责逐字写答案，而是把整句话编码成一个能表达语义的向量，再由分类头输出每个类别的概率。

## 2. Encoder-only 结构

```mermaid
flowchart LR
    A["原始文本"] --> B["Tokenizer"]
    B --> C["Token + Position + Segment Embedding"]
    C --> D["双向 Transformer Encoder x N"]
    D --> E["句向量 CLS / Pooling"]
    E --> F["Dropout + Linear 分类头"]
    F --> G["Softmax 类别概率"]
```

“Encoder-only”表示模型的主要工作是理解输入，不包含像 Qwen 那样不断生成下一个 token 的解码过程。

## 3. Tokenizer 和输入表示

Tokenizer 先把文本拆成模型词表中的 token：

```text
复现这篇论文并运行消融
→ [CLS] 复 现 这 篇 论 文 并 运 行 消 融 [SEP]
```

每个位置的输入向量由三部分相加：

```text
InputEmbedding = TokenEmbedding + PositionEmbedding + SegmentEmbedding
```

- Token Embedding：表示“这个 token 是什么”。
- Position Embedding：表示“它在句子中的位置”。
- Segment Embedding：原始 BERT 用于区分句子 A 和句子 B；单句分类通常都属于同一段。

`[CLS]` 是放在句首的特殊 token，经过多层注意力后，它的隐藏状态可以作为整句话的聚合表示。

## 4. 双向自注意力

BERT 的关键是双向 Self-Attention。每个 token 都可以同时关注左侧和右侧上下文。

例如“论文”既能看到前面的“复现”，也能看到后面的“消融实验”。这有利于区分：

```text
解释论文中的注意力机制          → General
复现论文并对比原始指标          → Paper_Reproduction
```

对于一组输入向量 `X`，注意力层先计算：

```text
Q = XWq
K = XWk
V = XWv
```

再计算 token 之间的相关程度：

```text
Attention(Q, K, V) = softmax(QK^T / sqrt(d))V
```

- `Q`：当前位置想寻找什么信息。
- `K`：每个位置可以被怎样匹配。
- `V`：匹配后真正汇聚的内容。
- `sqrt(d)`：避免点积过大导致 Softmax 过于尖锐。

## 5. 多头注意力

单个注意力头只能学习一种匹配空间。Multi-Head Attention 并行学习多组 `Q/K/V`：

```text
Head 1：关注动作词，如“复现、运行、比较”
Head 2：关注对象，如“论文、框架、代码”
Head 3：关注约束，如“三组、smoke、GPU”
...
```

不同头的输出拼接后再经过线性映射。这里的“关注内容”只是帮助理解的例子，不代表每个头一定具有人类可命名的固定职责。

## 6. Feed-Forward、残差和归一化

每个 Encoder Layer 通常包含两块：

1. Multi-Head Self-Attention
2. Position-wise Feed-Forward Network

它们周围还有残差连接和 LayerNorm：

```text
H1 = LayerNorm(X + SelfAttention(X))
H2 = LayerNorm(H1 + FeedForward(H1))
```

残差连接让深层模型更容易训练；LayerNorm 稳定不同层的数值范围；Feed-Forward Network 对每个位置进行非线性特征变换。

## 7. 本项目候选 BGE-small 的结构

`BAAI/bge-small-zh-v1.5` 是 BERT 类中文编码器。官方配置的主要参数是：

| 参数 | 数值 | 含义 |
|---|---:|---|
| Encoder 层数 | 4 | 重复 4 个 Transformer Encoder Layer |
| Hidden Size | 512 | 每个 token 的隐藏向量维度 |
| Attention Heads | 8 | 每层 8 个注意力头 |
| Intermediate Size | 2048 | Feed-Forward 中间维度 |
| Vocabulary Size | 21128 | Tokenizer 词表规模 |
| Max Positions | 512 | 最大位置长度 |
| 参数量 | 约 24M | 适合本地 CPU 推理 |

意图通常只有几十个 token，所以训练和部署时把 `max_length` 设为 `128` 即可，没必要总是填充到 512。

## 8. 如何增加分类头

BGE 原始目标主要是生成文本向量。本项目在句向量后增加一个线性层：

```text
sentence_vector: [batch, 512]
classifier_weight: [512, 4]
logits: [batch, 4]
```

计算过程：

```text
logits = Linear(Dropout(sentence_vector))
probabilities = Softmax(logits)
```

四个输出位置分别对应四个固定标签。`label2id.json` 和 `id2label.json` 必须跟模型一起保存，防止部署时标签顺序错位。

## 9. 分类交叉熵

假设正确类别是 `Paper_Reproduction`，模型给出的概率是：

```json
{
  "Framework_Evaluation": 0.03,
  "Paper_Reproduction": 0.90,
  "Code_Execution": 0.05,
  "General": 0.02
}
```

单个样本的分类损失可以写成：

```text
Loss = -log(P(correct_class))
```

训练会让正确类别概率增大。所有样本损失取平均后反向传播，更新分类头和 BERT 参数。

## 10. 全量微调、冻结和只训分类头

| 方法 | 更新范围 | 优点 | 缺点 |
|---|---|---|---|
| 只训练分类头 | Linear 层 | 最快，不容易破坏预训练能力 | 任务适配能力有限 |
| 冻结底层 | 分类头和顶部若干层 | 训练稳定、成本较低 | 需要选择冻结层数 |
| 全量微调 | 全部 Encoder + 分类头 | 通常适配能力最好 | 小数据时更容易过拟合 |

BGE-small 只有约 24M 参数，V100 可以直接全量微调。为了教学完整性，建议同时跑“只训分类头”和“全量微调”，比较两者的 Macro-F1 和过拟合程度。

## 11. 置信度不是天然可靠

Softmax 输出很方便，但 `0.95` 不一定意味着模型真的有 95% 概率正确。模型可能过度自信。

因此要在独立 Dev 集做校准，例如 Temperature Scaling，并记录：

- Expected Calibration Error
- Brier Score
- Reliability Diagram

自动路由阈值 `0.85` 应根据校准后的概率确定，而不是凭感觉设置。

## 12. CPU 推理链路

```mermaid
flowchart LR
    A["POST /v1/classify"] --> B["批量 Tokenize"]
    B --> C["BERT CPU / ONNX Runtime"]
    C --> D["Softmax 与校准"]
    D --> E["标签、概率、耗时"]
```

开发阶段可以直接使用 PyTorch + FastAPI。稳定后可导出 ONNX，并采用动态量化减少模型大小和 CPU 延迟。

量化前后必须重新评测，不能只验证程序可以启动。至少比较：

- Macro-F1 是否下降
- 各类别 Recall 是否下降
- P50/P95 延迟
- 模型大小和内存占用

## 13. BERT 方案的边界

BERT 分类器擅长：

- 固定标签分类
- 输出可比较的类别概率
- 快速 CPU 推理
- 明确、稳定的服务契约

它不擅长：

- 自由生成复杂 JSON
- 在没有标注的情况下增加新字段
- 从长对话中自动决定复杂工作流
- 同时完成开放式解释和规划

所以第一版让 BERT 负责主路由，让规则负责确定性实体；需要复杂实体和多意图分析时再升级到 Qwen。

## 14. 学习检查表

阅读完本文后，应能回答：

- Encoder-only 和 Decoder-only 有什么区别？
- 为什么 BERT 能同时看到 token 左右两侧？
- `Q/K/V` 分别表示什么？
- `[CLS]` 如何变成分类输入？
- 分类头的输出维度为什么等于标签数量？
- 为什么 Softmax 概率仍然需要校准？
- 为什么 24M 的 BGE-small 适合 CPU 服务？

## 参考资料

- [BERT: Pre-training of Deep Bidirectional Transformers for Language Understanding](https://arxiv.org/abs/1810.04805)
- [BGE-small-zh-v1.5 模型卡](https://huggingface.co/BAAI/bge-small-zh-v1.5)
- [BGE-small 配置](https://huggingface.co/BAAI/bge-small-zh/blob/main/config.json)
