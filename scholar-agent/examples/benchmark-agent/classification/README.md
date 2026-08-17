# Classification Benchmark Agent Example

这个小样本用于验证独立 Benchmark Agent，不用于证明任何模型效果。

输入是 24 条带 `review` 和 `label` 的分类记录。系统会执行：

```text
数据审计
  -> stratified_hash 划分
  -> train / validation / test_features 物理落盘
  -> 泄漏检查
  -> macro_f1 指标契约
  -> candidate_priority_only Reward 契约
  -> 隐藏标签上的后端指标重算
```

`test_features.jsonl` 不包含 `label`。隐藏标签和隐藏 evaluator 位于仓库工作区之外，Repository Adapter 只能输出 `id + prediction`，最终 `macro_f1` 与 `accuracy` 由 Go 后端重新计算。

运行验收测试：

```bash
cd scholar-agent/backend
go test ./internal/agent -run TestBenchmarkAgentBuildsLeakageSafeContractAndRecomputesHiddenMetrics -v
```

测试会检查：

- 三个 split 都非空；
- 归一化输入不会跨 split；
- 公开 test 文件不含标签；
- 工作区中不存在隐藏标签文件；
- 预测 ID 与隐藏标签一一对应；
- 最终指标来自后端重算，而不是 Adapter 自报。

预期契约摘要见 [`expected_summary.json`](expected_summary.json)。
