# Real Repository AutoResearch Examples

This directory contains bounded AutoResearch task packages that were executed through the deployed ScholarAgent API and Docker sandbox. Every result records the actual upstream commit, public search evidence, and a model-hidden holdout. The current packages now request those exact commits; request-to-checkout enforcement was verified end to end by the two later LightRAG runs. These are module-level engineering experiments, not full upstream product benchmarks.

## Verified Campaigns

All four final campaigns used `search_runs=3`, `search_aggregation=worst`, and three hidden validation runs.

| Target | Recorded commit, pinned by current package | Public search | Hidden holdout | Candidate trials | Result |
|---|---|---:|---:|---:|---|
| [rank-bm25](https://github.com/dorianbrown/rank_bm25) | `47aa3ddf8dc1ebeb7ef4e65f2b4536af44594099` | `5/9 -> 9/9` | `1/4 -> 4/4`, `3/3` runs passed | 1 | Validated |
| [Tenacity](https://github.com/jd/tenacity) | `26f719dc73d3c5612b9c1b8d18a7883837790ad8` | `6/7 -> 7/7` | `0/4 -> 4/4`, `3/3` runs passed | 1 | Validated |
| [LightRAG](https://github.com/HKUDS/LightRAG) | `24ee484864357865b20770e478b177ae68391796` | `4/8 -> 8/8` | `1/4 -> 4/4`, `3/3` runs passed | 1 | Validated, deterministic target stop |
| [Microsoft GraphRAG](https://github.com/microsoft/graphrag) | `14a00ad88fc33cf2b52f4f113f25807556f8e25e` | `6/12 -> 12/12` | `1/4 -> 4/4`, `3/3` runs passed | 8, 4 kept | Validated |

The scores are frozen case ratios emitted by the task-package evaluators. A completed plan is not automatically a pass: the final report must satisfy the predeclared hidden threshold and integrity checks. The initial four records predate `requested_revision` in `repo_manifest`; use the later LightRAG records as evidence for exact revision enforcement.

## Package Contract

Each current package contains exactly three uploaded files:

```text
<target>/
├── autoresearch.json       # editable/protected scope, commands, metric and budgets
├── evaluator.py            # public search contract
└── holdout_evaluator.py    # model-hidden final contract
```

The current specifications also freeze:

- `repository_revision`: exact 40-character upstream commit.
- `search_runs`: evaluator repetitions for the baseline and every candidate.
- `search_aggregation`: `mean`, `median`, or direction-aware `worst`.
- `target_score`: deterministic search stop after a kept candidate reaches the declared target.
- `validation_runs`: fresh hidden validation processes after search.

## Run Through ScholarAgent

Use the standard-library runner from the project root:

```bash
python3 examples/autoresearch/run_campaign.py \
  --repository https://github.com/HKUDS/LightRAG.git \
  --package examples/autoresearch/real_repositories/lightrag \
  --output examples/autoresearch/real_repositories/results/my_lightrag_run.json \
  --base-url http://localhost:8080 \
  --max-trials 8 \
  --wall-minutes 5 \
  --validation-runs 3
```

The runner uploads the three files, creates the eight-node AutoResearch plan, waits for terminal status, and stores the complete `plan_graph`. Its console summary includes requested and actual revisions, acquisition method, target stop, search statistics, and hidden validation statistics.

## Evidence Files

The principal machine-readable records are:

| File | Purpose |
|---|---|
| `results/2026-08-10_rank_bm25_e2e.json` | rank-bm25 full product run |
| `results/2026-08-10_tenacity_e2e.json` | Tenacity full product run |
| `results/2026-08-10_lightrag_e2e.json` | original LightRAG run before revision pinning was enforced |
| `results/2026-08-10_lightrag_pinned_revision_e2e.json` | exact-revision run that exposed score-plateau budget waste |
| `results/2026-08-10_lightrag_target_stop_e2e.json` | exact-revision target-stop verification |
| `results/2026-08-10_graphrag_v2_e2e.json` | revised GraphRAG contract and untouched holdout |

Earlier `holdout_v1` through `holdout_v6` files preserve the failed GraphRAG development sequence. They remain negative evidence and must not be presented as accepted fixes.

Inspect a result without trusting prose:

```bash
jq '{
  plan: .plan_graph.id,
  status: .plan_graph.status,
  manifest: (.plan_graph.artifacts.repo_manifest.value | fromjson |
    {requested_revision, repository_commit, acquisition_method}),
  search: (.plan_graph.artifacts.research_trial_ledger.value | fromjson |
    {baseline_score, best_score, target_score, completed_trials, stop_reason}),
  validation: (.plan_graph.artifacts.research_validation_report.value | fromjson |
    {status, validation_mode, holdout_baseline_score, mean_score, passed_runs})
}' examples/autoresearch/real_repositories/results/2026-08-10_lightrag_target_stop_e2e.json
```

Detailed experiment interpretation and architecture changes are in [`docs/autoresearch/08_real_repository_experiments.md`](../../../docs/autoresearch/08_real_repository_experiments.md).
