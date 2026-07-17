# Attention Is All You Need 轻量结构消融

本实验由 ScholarAgent 的单节点 `/api/execute` 路径生成代码，并通过 HAI V100 主机上的 Docker 沙箱执行；它不是完整 Planner/Scheduler DAG。实验固定 `random.seed(42)`、`seq_len=12`、`d_model=48`，每个配置重复 5 次。

## 实验边界

- 沙箱可以识别 `Tesla V100-SXM2-32GB`，但本轮为避免临时安装大型依赖，生成代码只使用 Python 标准库，实际计算设备为 CPU。
- 这是 scaled dot-product multi-head self-attention 的结构级前向 smoke test，不是 WMT14 训练，也不报告或宣称复现 BLEU。
- 延迟只有微秒/毫秒级微基准意义，不应外推到真实 Transformer 训练吞吐。

## 结果

| Heads | Scaling | Residual | Time (ms) | Output L2 | Attention entropy |
|---:|:---:|:---:|---:|---:|---:|
| 1 | yes | yes | 27.175 | 12.038065 | 3.584948 |
| 2 | yes | yes | 27.497 | 12.038054 | 3.584948 |
| 4 | yes | yes | 27.925 | 12.038025 | 3.584947 |
| 8 | yes | yes | 28.852 | 12.038023 | 3.584946 |
| 4 | no | yes | 29.020 | 12.037956 | 3.584775 |
| 4 | yes | no | 28.031 | 0.092616 | 3.584947 |
| 4 | no | no | 28.462 | 0.092652 | 3.584775 |

以 `heads=4, scaling=yes, residual=yes` 为基线：

- 去掉残差连接后，输出 L2 下降约 `99.23%`，说明残差路径主导这一随机初始化微型前向的输出尺度。
- 去掉 `1/sqrt(d_k)` 缩放后，注意力熵下降约 `0.0048%`。当前权重初始化较小，因此差异有限。
- 从 4 头增加到 8 头，平均延迟增加约 `1.79%`；这些数值仅用于本次受控 smoke 比较。

## 复现文件

- `2026-07-17_attention_light_ablation.py`：ScholarAgent 生成并实际执行的代码。
- `2026-07-17_attention_light_ablation.json`：结构化结果与实验限制。
