# 评测器、安全与证据边界

## 1. 冻结评测器

`autoresearch_spec` 在依赖安装和实验开始前读取所有 protected 文件，记录 SHA-256。每个 guard 和 evaluator 命令结束后都会重新计算哈希。任何缺失、替换、符号链接或内容变化都会使研究任务进入 `compromised`，尝试恢复保护文件与最佳 editable 快照，并停止循环；若恢复路径本身已不可信，则宁可失败也不会越过工作区边界写入。

系统还会冻结除 editable、`.git/`、`.scholar/`、虚拟环境、依赖目录和构建缓存以外的源码与关键配置整体指纹。这样可以发现候选在 evaluator 导入期间通过副作用修改另一个未授权源码文件。完整性失败时会恢复初始不可变源码快照；若父目录已被替换成符号链接，恢复会拒绝继续，避免越过工作区边界写文件。

保护集合应至少包含：

- evaluator 脚本。
- benchmark/test 数据。
- 指标配置和固定随机种子配置。
- `autoresearch.json` 本身；从仓库文件加载时由系统自动加入。

## 2. 可修改范围

LLM 没有文件系统工具。它返回结构化候选，Go harness 才执行写入。目标必须同时满足：

1. 在 `editable_files` 白名单中。
2. 是工作区内已存在的普通文件。
3. 路径的每一级都不是符号链接。
4. 文件大小和单轮文件数在硬限制内。
5. 内容没有新引入被禁构造。

写入采用同目录临时文件、同步和原子 rename。Harness 持有当前最佳内容与权限，用于 Reject 或失败回滚。

## 3. 命令边界

ResearchSpec 使用字符串数组保存命令。Harness 不接受模型生成的 shell 命令；它校验 executable allowlist 后，对每个参数单独转义，再在已挂载 `/workspace` 的持久沙箱中运行。模型不能在 Trial 之间改变命令。

实际部署仍应设置：

```text
SANDBOX_NETWORK_MODE=none
SANDBOX_IMAGE_ALLOWLIST=<approved image prefix>
SANDBOX_CONTAINER_USER=<non-root uid:gid>
SANDBOX_READ_ONLY_ROOT=true
```

GPU 实验还需要显式配置 `SANDBOX_DOCKER_GPUS` 和匹配的 CUDA 镜像。

## 4. 独立复验

`autoresearch_validate` 不调用候选模型，直接执行：

1. 检查 TrialLedger 引用的 spec hash。
2. 检查 protected 文件哈希和非 editable 工作区指纹。
3. 检查工作区 editable 文件哈希等于 TrialLedger 的最佳候选。
4. 按冻结的 `validation_runs` 重跑 1 至 5 组 guards 和 evaluator；每组都是新的命令进程。
5. 将每个观察值与账本最佳值按 `max(1e-9, abs(score)*1e-6)` 容差逐次比较。
6. 汇总观察值、均值、总体标准差、失败率与验证阶段命令资源证据。

请求的每一轮都完成且通过才输出 `status=validated`。普通 evaluator 失败或分数漂移会继续收集后续轮次，文件完整性失败则恢复并立即停止。最终 Artifact 仍是向后兼容扩展的 `autoresearch.validation/v1`，包含 spec/ledger hash、预期分数、逐次观察值、均值、标准差、失败率、完整性布尔值、逐轮命令结果和资源摘要。`observed_score` 在多次验证时表示均值。

## 5. 当前可信边界

已能证明：

- 记录的候选代码确实经过给定命令运行。
- evaluator、声明的 benchmark、spec 和其余受指纹覆盖的源码/配置在循环中没有改变。
- Keep/Reject 使用的是冻结主指标与阈值。
- 最终工作区文件和账本最佳候选一致。
- 最佳分数能够在同一环境中按声明次数重复运行得到，且间歇失败不会被最好的一次掩盖。

当前不能证明：

- benchmark 在科学上足够代表真实分布。
- 候选没有通过读取可见测试数据发生过拟合或数据泄漏。
- 候选没有在 evaluator 进程内进行临时 monkey patch、运行时探测或执行后复原的投机行为；哈希检查主要证明命令前后的持久状态。
- 容器内不存在侧信道；当前是单机 Docker 原型，不是零信任多租户环境。
- GPU 算子完全确定；重复新进程不等于自动切换 seed，随机性、驱动和硬件差异仍需在 evaluator 中控制并通过显式多 seed 评测。
- 一个指标提升就意味着论文主张成立。

对高价值研究，推荐把隐藏测试集放到独立评测服务，使用只读数据挂载，并由人工审批 ResearchSpec。当前 P0 的冻结哈希是必要基础，但不是完整的防作弊体系。

重复验证的字段、统计规则、资源口径和真实运行见[重复验证与执行资源证据](07_repeated_validation_and_resource_evidence.md)。
