# Attention Is All You Need 项目原生 DAG 轻量消融

本实验由 ScholarAgent 项目自身的完整生产链执行，而不是在远端单独启动 Python 脚本。执行路径为：

`POST /api/plan` -> `PlanStore` -> `POST /api/plans/:id/execute` -> `Scheduler` -> `Librarian/Coder/Sandbox/Data Agent` -> Docker Sandbox -> Artifact 与 SSE 回传。

## 执行验收

- Plan ID：`3be879c8-ce9d-472d-af5f-ddb3e7c9acd1`
- Intent：`Paper_Reproduction`
- 仓库：`https://github.com/harvardnlp/annotated-transformer`
- 复现模式：`smoke`
- DAG：8 个节点全部完成，0 个失败
- 产物：15 个，包括 `repo_url`、`repo_manifest`、`run_metrics` 和 `comparison_report`
- 事件：61 条；终态 SSE 回放从 `plan_started` 开始，以 `plan_completed` 结束
- 沙箱：PyTorch `2.0.1+cpu`，固定 seed `20260717`

项目在第一次远端验收时虽然完成了 8 节点 DAG，但没有遵守指定仓库，也只运行了固定 4-head 基线。随后修复了指定仓库优先级、显式 smoke 模式识别和消融 runner 生成逻辑；本记录来自修复后的第二次完整执行。

## 实验设置

- `d_model=64`
- `batch_size=2`
- `sequence_length=16`
- 每组预热 8 次，计时 40 次并报告中位数
- 各组共享相同 Q/K/V/O 投影权重与输入
- 变量：heads `1/2/4/8`、attention scaling、residual connection

## 结果

| 配置 | Median time (ms) | Attention entropy | Output L2 |
|---|---:|---:|---:|
| heads=1 | 0.261365 | 2.725055 | 43.983147 |
| heads=2 | 0.294604 | 2.721438 | 43.998783 |
| heads=4（基线） | 0.303809 | 2.725760 | 44.000565 |
| heads=8 | 0.322235 | 2.726724 | 43.989876 |
| no scaling（heads=4） | 0.272600 | 2.171573 | 44.311558 |
| no residual（heads=4） | 0.283523 | 2.725760 | 3.151376 |

以 heads=4、scaling/residual 均开启为结构基线：

- heads 从 1 增至 8 时，中位前向延迟增加 `23.29%`。
- 关闭 `1/sqrt(d_k)` scaling 后，注意力熵下降 `20.33%`，随机输入下的 softmax 分布更尖锐。
- 关闭 residual 后，输出 L2 下降 `92.84%`；该数值仅反映这组固定随机初始化的单层前向尺度。

## 实验边界

- ScholarAgent 克隆并扫描了指定公开仓库，然后生成受控的 `scholar_repro_smoke.py` 消融入口；消融计算不是仓库原始训练命令。
- 本实验只验证多头注意力结构和项目端到端执行链，不下载 WMT14、不训练完整 Transformer、不计算 BLEU。
- 延迟来自 CPU 小张量微基准，不能外推为 V100 训练吞吐，也不能与独立 GPU 实验直接比较。

完整结构化指标见 `2026-07-17_attention_project_native_ablation.json`。
