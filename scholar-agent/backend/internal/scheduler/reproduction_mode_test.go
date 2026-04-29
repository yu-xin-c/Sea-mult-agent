package scheduler

import (
	"strings"
	"testing"

	"scholar-agent-backend/internal/models"
)

func TestDecideReproductionMode_FullRequestDowngradesWhenResourcesInsufficient(t *testing.T) {
	t.Setenv("PAPER_REPRO_MODE", "full")
	t.Setenv("PAPER_REPRO_FULL_MIN_CPU", "9999")
	t.Setenv("PAPER_REPRO_FULL_MIN_MEMORY_GB", "9999")
	t.Setenv("PAPER_REPRO_FULL_MIN_DISK_GB", "9999")
	t.Setenv("PAPER_REPRO_FULL_MIN_GPU", "99")

	decision := decideReproductionMode(&models.Task{}, t.TempDir())
	if decision.EffectiveMode != reproductionModeSmoke {
		t.Fatalf("expected insufficient full request to downgrade to smoke, got %q", decision.EffectiveMode)
	}
	if decision.FullEligible {
		t.Fatalf("expected full reproduction to be ineligible")
	}
	if !strings.Contains(strings.Join(decision.Reasons, " "), "insufficient") {
		t.Fatalf("expected insufficient-resource reason, got %#v", decision.Reasons)
	}
}

func TestDecideReproductionMode_AutoNeedsExplicitFullRequest(t *testing.T) {
	t.Setenv("PAPER_REPRO_MODE", "auto")
	t.Setenv("PAPER_REPRO_FULL_MIN_CPU", "0")
	t.Setenv("PAPER_REPRO_FULL_MIN_MEMORY_GB", "0")
	t.Setenv("PAPER_REPRO_FULL_MIN_DISK_GB", "0")
	t.Setenv("PAPER_REPRO_FULL_MIN_GPU", "0")

	decision := decideReproductionMode(&models.Task{}, t.TempDir())
	if decision.EffectiveMode != reproductionModeSmoke {
		t.Fatalf("expected auto mode without explicit full request to stay smoke, got %q", decision.EffectiveMode)
	}

	decision = decideReproductionMode(&models.Task{Inputs: map[string]any{"full_reproduction_requested": true}}, t.TempDir())
	if decision.EffectiveMode != reproductionModeFull {
		t.Fatalf("expected auto mode with explicit full request and sufficient thresholds to become full, got %q", decision.EffectiveMode)
	}
}
