# 意图路由基准测试报告

## 1. 实验目标

本次实验针对“用户输入一句自然语言”的意图识别路由能力进行离线评测，比较以下三种预测模式：

- `rule`
  - 纯规则路由
- `llm`
  - 纯大模型路由
- `auto`
  - 优先使用大模型，失败时回退到规则路由

评测目标是观察不同路由策略在当前四分类意图空间下的准确率、类别均衡性和置信度质量。

## 2. 标签空间

本次实验统一使用以下 4 个意图标签：

- `Framework_Evaluation`
- `Paper_Reproduction`
- `Code_Execution`
- `General`

## 3. 数据集设计

数据集文件：

- `docs/intent/benchmarks/2026-04-22_intent_eval_dataset.jsonl`

数据集规模：

- 总样本数：80
- 每类样本数：20
- 数据分布：严格均衡

设计原则：

- 输入形式统一为“一句话用户请求”
- 同时覆盖清晰样本和边界样本
- 尤其关注以下易混淆区域：
  - `Framework_Evaluation` vs `Code_Execution`
  - `Paper_Reproduction` vs `General`
  - `General` vs `Code_Execution`

## 4. 评测指标

本次报告使用以下科研常见分类指标：

- `Accuracy`
- `Micro-F1`
- `Macro-Precision`
- `Macro-Recall`
- `Macro-F1`
- `Weighted-F1`
- `Balanced Accuracy`
- `Top-1 Brier Score`
- `Expected Calibration Error`

其中：

- `rule` 模式不返回置信度，因此没有校准指标
- `llm` 和 `auto` 输出了 `confidence`，因此统计了 `Brier Score` 和 `ECE`

## 5. 实验命令

执行目录：

```bash
cd /Users/bytedance/project/Sea-mult-agent/scholar-agent/backend
```

执行命令：

```bash
./intent-eval -dataset ../docs/intent/benchmarks/2026-04-22_intent_eval_dataset.jsonl -predictor rule -include-predictions > ../docs/intent/benchmarks/2026-04-22_rule_eval.json
./intent-eval -dataset ../docs/intent/benchmarks/2026-04-22_intent_eval_dataset.jsonl -predictor llm -include-predictions > ../docs/intent/benchmarks/2026-04-22_llm_eval.json
./intent-eval -dataset ../docs/intent/benchmarks/2026-04-22_intent_eval_dataset.jsonl -predictor auto -include-predictions > ../docs/intent/benchmarks/2026-04-22_auto_eval.json
```

结果文件：

- `docs/intent/benchmarks/2026-04-22_rule_eval.json`
- `docs/intent/benchmarks/2026-04-22_llm_eval.json`
- `docs/intent/benchmarks/2026-04-22_auto_eval.json`

## 6. 核心结果

| Predictor | Accuracy | Macro-F1 | Weighted-F1 | Balanced Accuracy | Mean Confidence | Brier Score | ECE |
|---|---:|---:|---:|---:|---:|---:|---:|
| `rule` | 0.8750 | 0.8729 | 0.8729 | 0.8750 | - | - | - |
| `llm` | 0.9750 | 0.9868 | 0.9868 | 0.9750 | 0.9251 | 0.0067 | 0.0749 |
| `auto` | 1.0000 | 1.0000 | 1.0000 | 1.0000 | 0.9269 | 0.0063 | 0.0731 |

结论概览：

- 样本放大到 80 条后，`rule` 与 `llm` 都暴露出更真实的误差模式
- `rule` 模式整体可用，但在 `Code_Execution` 和 `Paper_Reproduction` 边界样本上误判较多
- `llm` 分类能力很强，但出现了 2 条 JSON 解析失败，说明链路瓶颈还在结构化输出稳定性
- `auto` 相比 `llm` 没有损失准确率，同时通过回退机制吸收了 LLM 输出失败

## 7. 分类别结果

### 7.1 `rule`

| Class | Precision | Recall | F1 | Support |
|---|---:|---:|---:|---:|
| `Framework_Evaluation` | 0.8333 | 1.0000 | 0.9091 | 20 |
| `Paper_Reproduction` | 1.0000 | 0.8500 | 0.9189 | 20 |
| `Code_Execution` | 0.9333 | 0.7000 | 0.8000 | 20 |
| `General` | 0.7917 | 0.9500 | 0.8636 | 20 |

### 7.2 `llm`

| Class | Precision | Recall | F1 | Support |
|---|---:|---:|---:|---:|
| `Framework_Evaluation` | 1.0000 | 0.9000 | 0.9474 | 20 |
| `Paper_Reproduction` | 1.0000 | 1.0000 | 1.0000 | 20 |
| `Code_Execution` | 1.0000 | 1.0000 | 1.0000 | 20 |
| `General` | 1.0000 | 1.0000 | 1.0000 | 20 |

### 7.3 `auto`

| Class | Precision | Recall | F1 | Support |
|---|---:|---:|---:|---:|
| `Framework_Evaluation` | 1.0000 | 1.0000 | 1.0000 | 20 |
| `Paper_Reproduction` | 1.0000 | 1.0000 | 1.0000 | 20 |
| `Code_Execution` | 1.0000 | 1.0000 | 1.0000 | 20 |
| `General` | 1.0000 | 1.0000 | 1.0000 | 20 |

## 8. 误分类分析

### 8.1 `rule` 的误分类样本

共有 10 条误分类，主要集中在 `Code_Execution`，其次是 `Paper_Reproduction` 和 `General`：

1. 样本：
   - `帮我写脚本统计这个 CSV 的均值和标准差`
   - 标注：`Code_Execution`
   - 预测：`General`
2. 样本：
   - `用 Python 读取日志并统计每个接口的 P95 延迟`
   - 标注：`Code_Execution`
   - 预测：`Framework_Evaluation`
3. 样本：
   - `帮我跑个脚本把 JSONL 数据转换成 CSV`
   - 标注：`Code_Execution`
   - 预测：`General`
4. 样本：
   - `帮我运行一段代码生成箱线图比较不同实验组`
   - 标注：`Code_Execution`
   - 预测：`Framework_Evaluation`
5. 样本：
   - `帮我写脚本合并两个结果文件并输出 diff`
   - 标注：`Code_Execution`
   - 预测：`General`
6. 样本：
   - `帮我跑脚本提取日志里的 request id 并计数`
   - 标注：`Code_Execution`
   - 预测：`General`
7. 样本：
   - `重现实验论文里的 ablation study，并检查与原文表格的差异`
   - 标注：`Paper_Reproduction`
   - 预测：`General`
8. 样本：
   - `根据论文描述恢复训练流程，重现实验曲线`
   - 标注：`Paper_Reproduction`
   - 预测：`Code_Execution`
9. 样本：
   - `请重跑论文里的 baseline 和主方法，比较结果是否一致`
   - 标注：`Paper_Reproduction`
   - 预测：`Framework_Evaluation`
10. 样本：
   - `讲一下 evaluation harness 在模型评测中的作用`
   - 标注：`General`
   - 预测：`Framework_Evaluation`

问题分析：

- 对“脚本类代码请求”的显式触发词依赖仍然较强
- 对“统计型代码任务”和“性能评测请求”的边界区分不够细
- 对“论文复现 + 曲线 / 表格 / baseline / ablation”这类复合表达，容易被局部关键词带偏
- `evaluation / benchmark / harness` 等词会把部分通用知识问答误吸引到 `Framework_Evaluation`

说明当前规则的主要短板是：

- 对“脚本类代码请求”的显式触发词依赖较强
- 对“指标统计”与“框架性能评测”的边界区分还不够细

### 8.2 `llm` 与 `auto`

- `llm` 在语义分类层面基本正确，但出现了 2 条结构化输出解析失败
- 这 2 条都来自 `Framework_Evaluation`
- 失败原因不是分类错，而是返回 JSON 末尾存在额外内容，导致 `json unmarshal` 失败
- `auto` 在当前数据集上没有误分类
- `auto` 有少量样本触发了规则回退，但最终都保持正确，说明混合策略有效吸收了 LLM 输出不稳定问题

## 9. 混淆矩阵观察

`rule` 模式的主要混淆方向如下：

- `Code_Execution -> General`：4
- `Code_Execution -> Framework_Evaluation`：2
- `Paper_Reproduction -> General`：1
- `Paper_Reproduction -> Code_Execution`：1
- `Paper_Reproduction -> Framework_Evaluation`：1
- `General -> Framework_Evaluation`：1

`llm` 的主要问题不是类别混淆，而是：

- `Framework_Evaluation -> __ERROR__`：2

`auto` 的混淆矩阵在本次实验中为单位阵，没有出现类别混淆。

## 10. 校准指标分析

### 10.1 `llm`

- `Mean Confidence = 0.9251`
- `Top-1 Brier Score = 0.0067`
- `ECE = 0.0749`

### 10.2 `auto`

- `Mean Confidence = 0.9269`
- `Top-1 Brier Score = 0.0063`
- `ECE = 0.0731`

解释：

- `Brier Score` 越低越好，本次 `auto` 仍略优于 `llm`
- `ECE` 越低越好，本次 `auto` 也略优于 `llm`
- `auto` 的优势并不来自更强的分类能力，而来自对 LLM 解析失败样本的兜底恢复

## 11. 实验结论

本次实验可以得到以下结论：

1. 当前 `rule` 路由在大样本集上的 `Macro-F1` 已降到 `0.8729`，说明纯规则策略难以覆盖真实表达多样性。
2. 当前 `llm` 路由的语义判断能力明显强于规则，但结构化 JSON 输出稳定性仍是风险点。
3. `auto` 在本次 80 条样本上达到 `1.0000 Accuracy`，是当前最稳妥的线上策略。
4. 如果后续重点优化规则链路，应优先补足“脚本类代码请求”“统计型代码任务”“论文复现中的复合表达”三类模式。
5. 如果后续重点优化 LLM 链路，应优先处理 JSON 解析鲁棒性，而不是继续单纯堆 prompt 示例。

## 12. 后续建议

建议下一步继续补 3 类实验：

1. 扩充边界样本
   - 增加“统计 / 绘图 / 分析 / 对比”混合表达
2. 做分层评测
   - 区分中文、英文、中英混合样本
3. 做稳定性评测
   - 多次重复运行 `llm` 与 `auto`，统计波动范围和置信区间

如果要继续接近论文风格，还可以进一步增加：

- Bootstrap 置信区间
- 多 prompt 版本 A/B 对照
- 不同模型版本对照
- 不同类别先验分布下的鲁棒性测试
