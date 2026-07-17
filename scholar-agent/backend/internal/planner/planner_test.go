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
