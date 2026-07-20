# Example: Attention Paper Reproduction

这个示例把项目已经跑通的 `Attention Is All You Need` 实验整理为一条可复用的验收路径。
它固定使用 `harvardnlp/annotated-transformer`，以 smoke 模式运行注意力结构消融，重点验证
ScholarAgent 是否能完成从计划生成到沙箱产物回传的完整链路。

## What This Example Tests

```text
POST /api/plan
  -> Planner / PlanStore
  -> POST /api/plans/:id/execute
  -> Scheduler
  -> Librarian / Coder / Sandbox / Data Agents
  -> Docker Sandbox
  -> Artifacts and event history
```

成功运行必须满足：

- 计划意图为 `Paper_Reproduction`，所有 DAG 节点进入 `completed`。
- 系统选择的仓库为 `https://github.com/harvardnlp/annotated-transformer`。
- 至少生成 `repo_url`、`run_metrics` 和 `comparison_report` 三个关键产物。
- 事件历史包含 `plan_started`、`artifact_created` 和 `plan_completed`。

## Run Through the API

先按项目根 README 启动完整服务，然后运行：

```bash
cd scholar-agent
python3 examples/paper-reproduction/run.py \
  --output /tmp/attention-paper-reproduction.json
```

脚本只使用 Python 标准库。它会检查服务健康状态、创建并执行计划、轮询终态、验证关键
产物与事件，最后输出一份不包含 API Key 的运行摘要。远端部署可以通过参数指定：

```bash
python3 examples/paper-reproduction/run.py \
  --base-url http://YOUR_SERVER:8080 \
  --output /tmp/attention-paper-reproduction.json
```

同一入口也可以通过 Makefile 调用：

```bash
SCHOLAR_API_URL=http://localhost:8080 make example-paper-reproduction
```

## Run as an Integration Test

仓库中的真实集成测试使用同一个论文、仓库和 smoke 约束：

```bash
cd scholar-agent/backend
REAL_PAPER_REPRO_TEST=1 \
SANDBOX_URL=http://localhost:8082 \
go test ./tests -run TestRealPaperReproductionFlow -v -count=1
```

该测试需要可用的 LLM 配置、联网仓库访问和已经启动的 Docker Sandbox，因此默认的
`go test ./...` 会跳过它。

示例 API 客户端自身还有一个不访问 LLM、网络或 Docker 的离线测试：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover \
  -s examples/paper-reproduction -p 'test_*.py'
```

## Verified Result

2026-07-17 的项目原生运行结果如下：

| Item | Observed result |
|---|---|
| Plan | 8/8 nodes completed; 0 failed |
| Repository | `harvardnlp/annotated-transformer` |
| Runtime | PyTorch `2.0.1+cpu`; seed `20260717` |
| Evidence | 15 artifacts; 61 events |
| Heads 1 -> 8 | Median forward latency increased `23.29%` |
| No attention scaling | Attention entropy decreased `20.33%` vs. 4-head baseline |
| No residual | Output L2 decreased `92.84%` vs. 4-head baseline |

完整指标与执行元数据：

- [项目原生 DAG 消融报告](../../docs/experiments/2026-07-17_attention_project_native_ablation.md)
- [项目原生 DAG 结构化结果](../../docs/experiments/2026-07-17_attention_project_native_ablation.json)
- [真实仓库 smoke 集成测试](../../docs/experiments/2026-07-17_attention_repo_smoke.md)

## Scope

这是结构级前向 smoke test。系统会克隆并扫描指定仓库，但消融入口由 ScholarAgent 生成，
不会下载 WMT14、训练完整 Transformer 或计算 BLEU。CPU 延迟只适用于本次小张量微基准。

另外两组记录用于辅助对照，不属于本示例的完整项目链：

- [`/api/execute` 单节点 CPU 消融](../../docs/experiments/2026-07-17_attention_light_ablation.md)
  验证单任务代码生成与沙箱执行。
- [独立 V100 GPU 消融](../../docs/experiments/2026-07-17_attention_gpu_ablation.md)
  验证 CUDA 微基准，但没有经过 ScholarAgent Planner、Scheduler 或 Docker Sandbox DAG。
