# Attention Is All You Need 独立 V100 GPU 结构消融

本实验通过 HAI 基础环境中的独立 Python 脚本运行，**没有经过 ScholarAgent Planner、Scheduler 或 Docker Sandbox DAG**。硬件为 `Tesla V100-SXM2-32GB`，使用 PyTorch `2.6.0+cu124` 和 CUDA `12.4`。实验固定随机种子和 Q/K/V/O 投影权重，仅改变注意力头数、缩放项和残差连接。

## 实验设置

- `seed=42`
- `batch_size=16`
- `seq_len=128`
- `d_model=256`
- 每个配置预热 5 次，之后运行 5 组、每组 20 次前向
- 使用 CUDA Event 计时，报告 5 组平均单步延迟的中位数
- 通过 `/root/agent-wiki/.v100.lock` 与已有 GPU 作业串行，避免资源争用污染计时

## 结果

| Heads | Scaling | Residual | Median time (ms) | Output L2 | Attention entropy |
|---:|:---:|:---:|---:|---:|---:|
| 1 | yes | yes | 0.2450 | 732.0685 | 6.3027 |
| 2 | yes | yes | 0.3026 | 731.9886 | 6.2996 |
| 4 | yes | yes | 0.3206 | 731.7886 | 6.3006 |
| 8 | yes | yes | 0.3887 | 731.7638 | 6.2987 |
| 4 | no | yes | 0.2911 | 943.5482 | 0.9697 |
| 4 | yes | no | 0.2914 | 112.2665 | 6.3006 |
| 4 | no | no | 0.2812 | 606.5909 | 0.9697 |

以 `heads=4, scaling=yes, residual=yes` 为基线：

- 从 4 头增加到 8 头，前向延迟增加约 `21.24%`。从 4 头减少到 1 头，延迟下降约 `23.59%`。
- 去掉 `1/sqrt(d_k)` 缩放后，注意力熵下降约 `84.61%`，输出 L2 增加约 `28.94%`。未缩放的 logits 使 softmax 明显变尖锐。
- 去掉残差连接后，输出 L2 下降约 `84.66%`；注意力熵不变，因为残差只作用于注意力输出之后。

## 实验边界

- 这是固定合成输入与权重的前向 smoke test，没有训练模型或下载数据集。
- 本实验不运行 WMT14，也不计算或宣称复现 BLEU。
- 延迟只代表本次单 V100 微基准，不应直接外推为完整 Transformer 的训练吞吐。
- 本实验与早先的标准库 CPU 消融使用不同维度和权重初始化，两组绝对数值不应直接横向比较。

原始代码与完整逐组计时见 `2026-07-17_attention_gpu_ablation.py` 和 `2026-07-17_attention_gpu_ablation.json`。
