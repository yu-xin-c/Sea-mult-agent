# Attention Is All You Need 真实仓库 Smoke 复现

本实验在 HAI V100 主机上运行 ScholarAgent 内置的 `TestRealPaperReproductionFlow`，用于验证论文解析、代码仓库发现、工作区准备、依赖安装、沙箱执行和结果对照的完整 DAG。

## 执行结果

| 项目 | 结果 |
|------|------|
| 测试状态 | `PASS` |
| DAG 总耗时 | `73.37s` |
| 仓库 | `https://github.com/harvardnlp/annotated-transformer` |
| 复现模式 | `smoke` |
| 运行时 | `torch 2.0.1+cpu` |
| 模型 | 2 层 PyTorch Transformer encoder-decoder |
| 参数量 | `167,680` |
| 输出形状 | `[2, 7, 64]` |
| 输出绝对值均值 | `0.795336` |
| 单次前向耗时 | `4.748 ms` |

任务最终产生了 `parsed_paper`、`repo_url`、`repo_manifest`、`dependency_spec`、`prepared_runtime`、`run_metrics` 和 `comparison_report` 等可追踪 artifact，8 个 DAG 节点全部完成。

## 实验边界

- 系统真实克隆并扫描了 `harvardnlp/annotated-transformer`，但该仓库没有被当前自动化链识别为可直接导入的模型入口，因此 smoke runner 使用 PyTorch 标准 `nn.Transformer` 执行受控前向。
- 本轮没有下载 WMT14、没有训练模型、没有计算 BLEU，也不声称复现论文最终指标。
- HAI 主机和 Docker 沙箱的 GPU runtime 健康检查正常，但本 runner 使用固定的 CPU PyTorch 镜像；V100 可见性由独立 GPU 沙箱验收覆盖。
- 资源探针运行在后端容器内，因此报告 `CUDA GPU=0`；这与 Docker 沙箱能够透传 V100 属于两个不同的检测范围。

## 部署修复

首次运行因默认安装大型通用 PyTorch wheel 而超过测试总超时。项目新增 `SANDBOX_DEFAULT_IMAGE` 配置后，远端使用预装 `torch 2.0.1+cpu` 的 smoke 镜像，依赖节点直接命中已安装包，完整 DAG 在 73.37 秒内通过。

结构级 heads/scaling/residual 消融见 [轻量结构消融](2026-07-17_attention_light_ablation.md)。
