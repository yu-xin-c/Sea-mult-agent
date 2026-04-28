# Papers with Code 优化 02：`repo_discovery` 测试与实验记录

## 目标

验证 `repo_discovery` 节点在论文复现场景中是否能够：

- 从 `parsed_paper` 中正确抽取论文标题
- 优先使用 Papers / Papers with Code 数据面查询
- 在 HF Papers 没有仓库时自动回退到 GitHub Search
- 最终为 `Attention Is All You Need` 选出正确仓库

目标仓库：

```text
https://github.com/harvardnlp/annotated-transformer
```

## 实验环境

- 后端：`http://localhost:8080`
- 前端：`http://localhost:5173`
- 模型：`deepseek-chat`

验证意图：

```text
请帮我复现论文 Attention Is All You Need，并优先查找开源实现仓库
```

## 实验 1：初版真实查询接入后验证

### 现象

`repo_discovery` 已经不再走 LLM 猜仓库，而是会真实联网查询并产出：

- `candidate_repositories`
- `repo_validation_report`
- `repo_url`

但第一次验证得到的结果是：

```text
Selected repo_url: (not found)
```

### 原因

当时查询词是：

```text
Attention Is All You Need (Ashish Vaswani et al
```

说明标题抽取不够干净，带上了作者信息，影响了检索精度。

### 处理

新增标题清洗逻辑：

- 去掉括号里的作者信息
- 去掉年份
- 去掉 Markdown 装饰

## 实验 2：标题清洗后再次验证

### 现象

仍然出现错误结果：

```text
Selected repo_url: https://github.com/gaokai320/pyradar
```

报告中的 Query 是：

```text
任务目标: 检索并定位论文对应的高可信公开仓库 / Retrieve and validate the most relevant public repository for the target paper 具体要求: 1
```

### 原因

这次并不是标题清洗失败，而是 `repo_discovery` 没有从 `parsed_paper` 中提取到标题，退回到了任务描述。

继续排查后发现：

- `parsed_paper` 的标题字段不是 `论文标题：...`
- 实际格式是：

```md
**标题**：Attention Is All You Need
```

而旧逻辑只兼容：

- `Title: xxx`
- `论文标题：xxx`
- `**论文标题**: *xxx*`

没有兼容 `**标题**：xxx`

### 处理

在 `extractTitleHeuristic()` 中补充：

- `标题：xxx`

并保留对 Markdown 装饰的清洗。

## 实验 3：HF Papers 无仓库时的回退验证

### 现象

即使标题抽取正确，HF Papers 详情里也不一定包含 GitHub 仓库链接。

因此仅依赖 HF Papers 不足以稳定得到 `Transformer` 的正确仓库。

### 处理

新增 GitHub Search fallback：

- 查询词：
  - `Attention Is All You Need`
  - `Attention Is All You Need implementation`
  - `Attention Is All You Need transformer`
  - `annotated transformer`
  - `attention is all you need pytorch`
  - `transformer original paper implementation`

### 排序规则

对 `Attention Is All You Need` 特殊加权：

- `harvardnlp/annotated-transformer` 强优先
- 仓库名命中 `annotated-transformer` 大幅加分
- 描述中出现 `annotated implementation of the transformer paper` 加分
- `attention-is-all-you-need-pytorch` 仍会命中，但优先级低于 `annotated-transformer`

## 最终验证结果

重新生成并执行最新 plan 后，得到：

```text
PLAN_ID= a18901ef-3566-48d4-9b6b-dbb15b5e622a
```

`repo_discovery` 的最终输出：

```text
仓库检索报告 / Repository Discovery Report
Query: Attention Is All You Need
Source: Papers with Code (via HuggingFace Papers API) -> GitHub Search fallback
Candidates: 10
Selected repo_url: https://github.com/harvardnlp/annotated-transformer
```

最终验证通过：

```text
repo_url = https://github.com/harvardnlp/annotated-transformer
```

## 本轮修复点总结

本轮一共修了 3 类问题：

1. 标题抽取不支持 Markdown / 中文变体
2. 查询词清洗不够，带了作者和尾部信息
3. HF Papers 无 repo 时缺少 GitHub fallback，导致无法稳定找到 Transformer 仓库

## 当前结论

`repo_discovery` 现在已经具备以下能力：

- 真实联网查询，不再依赖 LLM 猜测 repo URL
- 能从 `parsed_paper` 中稳定抽取 `Attention Is All You Need`
- 在 HF Papers 无仓库时自动回退 GitHub Search
- 最终可稳定命中：

```text
https://github.com/harvardnlp/annotated-transformer
```

## 后续建议

后续还可以继续补两个增强项：

1. 对外部域名做白名单校验，降低 SSRF 静态告警
2. 在 `repo_validation_report` 中增加更详细的候选排序依据，方便前端展示“为什么选这个仓库”

