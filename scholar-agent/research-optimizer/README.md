# Research Optimizer

The Research Optimizer is ScholarAgent's Python learning plane. It profiles bounded samples from frozen datasets, chooses the next candidate from a Go-validated frontier, and stores decision/outcome pairs in SQLite for cross-task learning.

Configuration search uses `hierarchical-contextual-ucb-uct/v2`:

1. Go runs every legal model combination once with its default configuration.
2. Route UCB allocates more tuning budget to promising model trees while preserving exploration.
3. A Top-K Beam plus an explicit exploration lane defines the active parameter frontier.
4. UCT-style node scores choose a parent path inside the selected model tree.
5. Virtual visits spread concurrent workers across routes before real results return.
6. Real validation rewards update scheduling statistics; candidate scores and root defaults remain immutable observations.

The current policy has no simulated rollout or value network, so it is not full MCTS. Continuous Bayesian optimization and Hyperband also require separate domain contracts and are not emulated by this discrete policy.

It does not execute experiments or decide whether a result is scientifically valid. The Go harness retains candidate validation, budgets, evaluator execution, Keep/Reject, rollback, and Holdout acceptance.

The service has no experiment-workspace mount. Go validates the frozen assets and sends only canonical, size-bounded profile samples; it also verifies the returned dataset fingerprint before freezing the profile.

```bash
RESEARCH_OPTIMIZER_API_TOKEN=local-optimizer-token \
RESEARCH_OPTIMIZER_DB_PATH=/tmp/scholar-experiences.sqlite3 \
python3 -m research_optimizer.service
```

Endpoints:

- `POST /v1/profile`
- `POST /v1/select`
- `POST /v1/experience/outcome`
- `POST /v1/experience/validation`
- `GET /v1/stats`
- `GET /health`

Only outcomes from campaigns later marked `validated` are used as cross-task policy history. Failed and rejected actions remain stored to avoid survivorship bias.
