package planner

import (
	"testing"

	"scholar-agent-backend/internal/models"
)

func TestBuildPaperReproductionInputsExplicitSmokeOverridesNegatedBLEU(t *testing.T) {
	inputs := buildPaperReproductionInputs(models.IntentContext{
		RawIntent:   "明确采用 smoke，不训练 WMT14，不做论文 BLEU 复现",
		Entities:    map[string]any{"smoke_reproduction": true},
		Constraints: map[string]any{"reproduction_mode": "smoke"},
	})
	if got := inputs["requested_reproduction_mode"]; got != "smoke" {
		t.Fatalf("requested_reproduction_mode=%v", got)
	}
	if got := inputs["full_reproduction_requested"]; got != false {
		t.Fatalf("full_reproduction_requested=%v", got)
	}
}

func TestBuildRepoDiscoveryInputsIncludesPreferredRepository(t *testing.T) {
	inputs := buildRepoDiscoveryInputs(models.IntentContext{
		Entities: map[string]any{
			"paper_title":        "Attention Is All You Need",
			"preferred_repo_url": "https://github.com/harvardnlp/annotated-transformer",
		},
	})
	if got := inputs["preferred_repo_url"]; got != "https://github.com/harvardnlp/annotated-transformer" {
		t.Fatalf("preferred_repo_url=%v", got)
	}
}

func TestPaperReproductionAddsBoundedAblationDesignWhenRequested(t *testing.T) {
	intent := models.IntentContext{
		RawIntent:  "复现 Transformer 并做最多 2 组轻量消融，总耗时 30 分钟，GPU 时间不超过 10 分钟",
		IntentType: "Paper_Reproduction",
		Entities:   map[string]any{"needs_ablation": true, "paper_title": "Transformer"},
		Constraints: map[string]any{
			"reproduction_mode": "smoke",
		},
	}
	plan, err := NewPlanner().BuildPlan(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	var design, prepare *models.TaskNode
	for _, node := range plan.Nodes {
		switch node.Type {
		case "ablation_design":
			design = node
		case "repo_prepare":
			prepare = node
		}
	}
	if design == nil || prepare == nil {
		t.Fatalf("missing ablation design or repo prepare node")
	}
	if got := design.Inputs["ablation_max_experiments"]; got != 2 {
		t.Fatalf("max experiments=%v", got)
	}
	if got := design.Inputs["ablation_max_wall_minutes"]; got != 30 {
		t.Fatalf("max wall minutes=%v", got)
	}
	if got := design.Inputs["ablation_max_gpu_minutes"]; got != 10 {
		t.Fatalf("max GPU minutes=%v", got)
	}
	if !containsArtifact(prepare.RequiredArtifacts, "ablation_plan") {
		t.Fatalf("repo_prepare does not consume ablation_plan: %#v", prepare.RequiredArtifacts)
	}
}

func TestCustomDatasetBenchmarkBuildsBoundedAdapterHarness(t *testing.T) {
	uploaded := []map[string]any{{
		"id": "upload-1", "name": "reviews.csv", "storage_path": "/tmp/reviews.csv", "text_excerpt": "review,label",
	}}
	intent := models.IntentContext{
		RawIntent:  "用 https://github.com/example/research-repo 跑 benchmark，输入列是 review，标签列是 label，最多 64 条样本",
		IntentType: "Custom_Benchmark",
		Entities: map[string]any{
			"needs_custom_benchmark": true,
			"preferred_repo_url":     "https://github.com/example/research-repo",
			"uploaded_files":         uploaded,
		},
	}
	plan, err := NewPlanner().BuildPlan(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if plan.IntentType != "Custom_Benchmark" || len(plan.Nodes) != 11 {
		t.Fatalf("unexpected custom benchmark graph: intent=%s nodes=%d", plan.IntentType, len(plan.Nodes))
	}
	wantedTypes := []string{
		"dataset_profile", "repo_discovery", "repo_prepare", "benchmark_adapter_generate", "resolve_dependencies",
		"prepare_runtime", "install_dependencies", "benchmark_adapter_preflight", "benchmark_execute", "benchmark_validate", "framework_report",
	}
	for index, taskType := range wantedTypes {
		if plan.Nodes[index].Type != taskType {
			t.Fatalf("node %d type=%s, want %s", index, plan.Nodes[index].Type, taskType)
		}
	}
	profile := plan.Nodes[0]
	if profile.AssignedTo != "research_coding_agent" || profile.Inputs["benchmark_input_column"] != "review" || profile.Inputs["benchmark_target_column"] != "label" {
		t.Fatalf("unexpected dataset profile node: %#v", profile)
	}
	discovery := plan.Nodes[1]
	if len(discovery.RequiredArtifacts) != 0 || discovery.Inputs["preferred_repo_url"] != "https://github.com/example/research-repo" {
		t.Fatalf("custom repository discovery incorrectly depends on a paper: %#v", discovery)
	}
	prepare := plan.Nodes[2]
	preparedUploads := prepare.Inputs["uploaded_files"].([]map[string]any)
	if _, leaked := preparedUploads[0]["text_excerpt"]; leaked {
		t.Fatalf("workspace upload reference retained text excerpt: %#v", preparedUploads)
	}
	execute := plan.Nodes[8]
	if execute.Inputs["benchmark_max_samples"] != 64 {
		t.Fatalf("benchmark sample budget=%v", execute.Inputs["benchmark_max_samples"])
	}
	if !containsArtifact(execute.RequiredArtifacts, "validated_benchmark_adapter_spec") || !containsArtifact(plan.Nodes[9].OutputArtifacts, "benchmark_validation_report") {
		t.Fatalf("benchmark evidence contracts are incomplete")
	}
}

func TestPaperReproductionRoutesRepositoryDebugToResearchCodingAgent(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	intent := models.IntentContext{
		RawIntent:  "复现 Transformer，遇到论文代码错误时调试，并画出重跑结果",
		IntentType: "Paper_Reproduction",
		Entities: map[string]any{
			"paper_title": "Transformer",
			"needs_fix":   true,
			"needs_plot":  true,
		},
		Constraints: map[string]any{"reproduction_mode": "smoke"},
	}
	plan, err := NewPlanner().BuildPlan(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	var rubric, baseline, gapDebug, compare, visualize, evidence *models.TaskNode
	for _, node := range plan.Nodes {
		switch node.Type {
		case "claim_rubric_extract":
			rubric = node
		case "paper_code_execute":
			baseline = node
		case "fix_and_rerun":
			gapDebug = node
		case "paper_compare":
			compare = node
		case "result_visualization":
			visualize = node
		case "claim_evidence_build":
			evidence = node
		}
	}
	if rubric == nil || baseline == nil || gapDebug == nil || compare == nil || visualize == nil || evidence == nil {
		t.Fatalf("missing claim-aware repository debug flow: rubric=%v baseline=%v gap=%v compare=%v visualize=%v evidence=%v", rubric != nil, baseline != nil, gapDebug != nil, compare != nil, visualize != nil, evidence != nil)
	}
	if baseline.AssignedTo != "research_coding_agent" || gapDebug.AssignedTo != "research_coding_agent" {
		t.Fatalf("paper debugging was not routed to research coding agent")
	}
	if !containsArtifact(baseline.OutputArtifacts, "paper_debug_report") || !containsArtifact(compare.RequiredArtifacts, "paper_debug_report") {
		t.Fatalf("baseline debug evidence is not propagated")
	}
	if !containsArtifact(gapDebug.RequiredArtifacts, "comparison_report") || !containsArtifact(gapDebug.OutputArtifacts, "rerun_metrics") {
		t.Fatalf("result-gap debug contracts are incomplete: %#v", gapDebug)
	}
	if len(visualize.Dependencies) != 1 || visualize.Dependencies[0] != gapDebug.ID || !containsArtifact(visualize.RequiredArtifacts, "rerun_metrics") {
		t.Fatalf("visualization does not consume rerun evidence: %#v", visualize)
	}
	if !containsArtifact(rubric.RequiredArtifacts, "parsed_paper") || !containsArtifact(rubric.OutputArtifacts, "claim_rubric") {
		t.Fatalf("claim rubric contract is incomplete: %#v", rubric)
	}
	if len(evidence.Dependencies) != 2 || evidence.Dependencies[0] != rubric.ID || evidence.Dependencies[1] != visualize.ID {
		t.Fatalf("claim graph is not the final evidence join: %#v", evidence.Dependencies)
	}
	for _, artifact := range []string{"claim_rubric", "run_metrics", "comparison_report", "rerun_metrics", "result_plot"} {
		if !containsArtifact(evidence.RequiredArtifacts, artifact) {
			t.Fatalf("claim graph is missing required evidence %s: %#v", artifact, evidence.RequiredArtifacts)
		}
	}
	if !containsArtifact(evidence.OutputArtifacts, "claim_evidence_graph") || !containsArtifact(evidence.OutputArtifacts, "claim_verification_report") {
		t.Fatalf("claim graph outputs are incomplete: %#v", evidence.OutputArtifacts)
	}
}
