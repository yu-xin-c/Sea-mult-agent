# 双模型意图识别真实训练记录（2026-07-22）

本文记录 ScholarAgent 意图识别模块的一次真实训练与评测。实验同时训练了 CPU 友好的 BGE/BERT 分类器和 GPU 上的 Qwen3-0.6B LoRA 路由器。本文只陈述本次已运行、可由产物复核的结果，不把单次实验外推为生产结论。

## 1. 实验目标

统一识别以下四类请求：

- `Framework_Evaluation`：评测框架、指标或 benchmark 设计
- `Paper_Reproduction`：论文环境搭建、复现、结果对齐
- `Code_Execution`：运行代码、计算指标、绘图或调试
- `General`：不属于以上三类的一般科研请求

对比两条技术路线：

| 路线 | 输出形式 | 预期部署位置 | 主要价值 |
| --- | --- | --- | --- |
| BGE/BERT 分类器 | 四分类概率 | CPU、本地快速路由 | 延迟低、输出空间受控 |
| Qwen3-0.6B + LoRA | 严格 JSON | GPU、复杂请求路由 | 可同时生成意图、实体和约束 |

## 2. 运行环境

- 远端 GPU：Tesla V100-SXM2-32GB
- Python：3.10.11
- PyTorch：2.6.0+cu124
- Transformers：5.7.0
- PEFT：0.18.1
- 随机种子：42

训练程序位于 `ai-services/intent_recognition/training/`。完整模型权重和逐样本预测保存在本地忽略目录 `tmp-realtest-logs/intent-dual-20260722/`，不会提交到 Git；仓库中提交训练代码、数据集和清单。

## 3. 数据与隔离检查

训练集和开发集由固定模板与槽位组合生成，测试集来自仓库已有意图 benchmark。四类样本保持平衡。

| 数据划分 | 样本数 | 每类样本数 |
| --- | ---: | ---: |
| train | 640 | 160 |
| dev | 160 | 40 |
| frozen test | 80 | 20 |

归一化文本交集检查结果：`train/dev=0`、`train/test=0`、`dev/test=0`。数据清单见 `ai-services/intent_recognition/data/dual_router_v1/manifest.json`。

| 文件 | SHA-256 |
| --- | --- |
| `train.jsonl` | `fa5ef847da56c7faca06341a95a7d5baa346bed5cf16c782805b373ae6e6f24b` |
| `dev.jsonl` | `f43cae7b0417edf187ef6294d5ebe020213da38a722a307b86a5b576900193c6` |
| `test.jsonl` | `92e3bb8e333b62bd207358cb4e1287dc33ffa9231a9e6a4faa33c5cab176ae18` |
| 原始冻结测试集 | `ea4a8352780e1884257f8c686710d2e014946635bf041372256112d07f61ee62` |

## 4. BGE/BERT 分类器

### 配置

- 基座：`BAAI/bge-small-zh-v1.5`
- 参数量：23,955,972，全部参与微调
- epoch：8
- batch size：32
- learning rate：`2e-5`
- max length：128
- weight decay：0.01
- warmup ratio：0.1

复现命令：

```bash
cd scholar-agent/ai-services/intent_recognition
python3 training/train_bert.py \
  --data-dir data/dual_router_v1 \
  --model BAAI/bge-small-zh-v1.5 \
  --output-dir runs/bert_bge_seed42 \
  --epochs 8 \
  --batch-size 32 \
  --seed 42
```

### 结果

| 指标 | 结果 |
| --- | ---: |
| 训练耗时（V100） | 6.82 s |
| 最佳 dev Macro-F1 | 0.9559 |
| frozen test Accuracy | 0.9625 |
| frozen test Macro-F1 | 0.9625 |
| CPU 单样本平均延迟 | 9.55 ms |
| CPU P95 延迟 | 10.10 ms |

测试集共有 3 个错误：两条论文复现请求被判为 `General`，一条 RAG 综述请求被判为 `Framework_Evaluation`。这说明词面相近的“分析、比较、综述”仍可能混淆任务目标。

模型权重 SHA-256：`79bcc2f78eb5b41de6355b9b898b6e3cfa79ce7dad4a7be89b3598767c752c8b`。

## 5. Qwen3-0.6B LoRA 路由器

### 配置

- 基座：`Qwen/Qwen3-0.6B`
- 总参数量（含 adapter）：606,142,464
- 可训练参数：10,092,544（约 1.67%）
- LoRA：rank 16，alpha 32，dropout 0.05
- 目标层：`q/k/v/o_proj`、`gate/up/down_proj`
- epoch：3
- micro batch：4
- gradient accumulation：8
- learning rate：`1e-4`
- max length：256
- FP16，随机种子 42

复现命令：

```bash
cd scholar-agent/ai-services/intent_recognition
python3 training/train_qwen_lora.py \
  --data-dir data/dual_router_v1 \
  --model Qwen/Qwen3-0.6B \
  --output-dir runs/qwen3_06b_lora_seed42 \
  --epochs 3 \
  --batch-size 4 \
  --eval-batch-size 8 \
  --gradient-accumulation 8 \
  --lora-rank 16 \
  --lora-alpha 32 \
  --seed 42
```

### 训练过程

| Epoch | Train loss | Dev Accuracy | Dev Macro-F1 | Schema valid rate |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 1.0248 | 0.8313 | 0.8358 | 0.9938 |
| 2 | 0.0472 | 0.9438 | 0.9436 | 1.0000 |
| 3 | 0.0174 | 0.9563 | 0.9561 | 1.0000 |

### 测试结果

| 指标 | 结果 |
| --- | ---: |
| 训练耗时（V100） | 306.25 s |
| frozen test Accuracy | 0.9875 |
| frozen test Macro-F1 | 0.9936 |
| JSON parse rate | 1.0000 |
| Schema valid rate | 0.9875 |
| V100 单样本平均延迟 | 301.56 ms |
| V100 P95 延迟 | 466.46 ms |

唯一失败样例是“帮我计算 confusion matrix 并把热力图画出来”。模型生成了语法正确的 JSON，但把意图写成未定义的 `Plotting`，因此 JSON 可解析、业务 schema 不合法，并按错误计入召回率。这也是为什么不能只报告 JSON parse rate。

最佳 LoRA adapter SHA-256：`afd8386a268dedb688431235e27b60251629efa7beb726d2e7d540032f68cde3`。

## 6. 评测代码修复

实验中发现原 `classification_metrics` 只在混淆矩阵中统计已知预测标签。Qwen 生成未知标签时，样本虽然影响 Accuracy，却没有进入对应类别的 FN，导致 Macro-F1 被虚高为 1.0。

本次修复在 `training/common.py` 中记录 `unknown_by_expected`，将未知预测计入真实类别的 FN 和 support；同时新增 `training/test_common.py` 回归测试。修复后 Qwen 测试 Macro-F1 为 0.9936，而不是错误的 1.0。

## 7. 结论与限制

本次小规模实验中，BGE/BERT 的 CPU 平均延迟约为 Qwen GPU 生成延迟的 1/32，适合默认路由；Qwen 能输出实体和约束，适合复杂请求或低置信度回退。两者不是简单替代关系，更合理的产品形态是“BERT 快速分类 + Qwen 复杂解析/仲裁”。

当前限制也必须保留：仅运行一个随机种子；train/dev 是程序生成数据，不是人工标注生产流量；测试集只有 80 条且类别均衡；尚未评测实体与约束抽取的精确率；本次结果不能证明 OOD 泛化或生产稳定性。

## 8. 本地验证

```bash
cd scholar-agent/ai-services/intent_recognition
PYTHONPATH=training python3 -m unittest -v training.test_common
python3 -m py_compile training/common.py training/build_dataset.py \
  training/train_bert.py training/train_qwen_lora.py
```
