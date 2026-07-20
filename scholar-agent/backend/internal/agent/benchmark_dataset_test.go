package agent

import (
	"os"
	"path/filepath"
	"testing"

	"scholar-agent-backend/internal/models"
)

func TestProfileBenchmarkDatasetInfersCSVContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reviews.csv")
	content := []byte("review,label\ngreat paper,positive\nmissing details,negative\nclear result,positive\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	checksum, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	task := &models.Task{Inputs: map[string]any{
		"uploaded_files": []map[string]any{{
			"name": "reviews.csv", "storage_path": path, "sha256": checksum, "size": len(content),
		}},
	}}

	manifest, err := profileBenchmarkDataset(task)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RowCount != 3 || manifest.InputColumn != "review" || manifest.TargetColumn != "label" {
		t.Fatalf("unexpected dataset contract: %#v", manifest)
	}
	if manifest.SuggestedTask != "classification" || manifest.RequiresConfirmation {
		t.Fatalf("unexpected task inference: %#v", manifest)
	}
	if manifest.SHA256 != checksum || len(manifest.SamplePreview) != 3 {
		t.Fatalf("unexpected checksum or preview: %#v", manifest)
	}
}

func TestProfileBenchmarkDatasetSupportsJSONLAndExplicitColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	content := []byte("{\"features\":[1,2],\"score\":0.4}\n{\"features\":[3,4],\"score\":0.8}\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	task := &models.Task{Inputs: map[string]any{
		"benchmark_input_column":  "features",
		"benchmark_target_column": "score",
		"uploaded_files": []map[string]any{{
			"name": "records.jsonl", "storage_path": path,
		}},
	}}

	manifest, err := profileBenchmarkDataset(task)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Format != "jsonl" || manifest.RowCount != 2 || manifest.MappingConfidence != 1 {
		t.Fatalf("unexpected JSONL manifest: %#v", manifest)
	}
}

func TestProfileBenchmarkDatasetRejectsInvalidColumnHintAndChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(path, []byte("text,label\na,yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	invalidColumn := &models.Task{Inputs: map[string]any{
		"benchmark_input_column": "missing",
		"uploaded_files": []map[string]any{{
			"name": "data.csv", "storage_path": path,
		}},
	}}
	if _, err := profileBenchmarkDataset(invalidColumn); err == nil {
		t.Fatal("expected invalid input column to be rejected")
	}

	invalidChecksum := &models.Task{Inputs: map[string]any{
		"uploaded_files": []map[string]any{{
			"name": "data.csv", "storage_path": path, "sha256": "deadbeef",
		}},
	}}
	if _, err := profileBenchmarkDataset(invalidChecksum); err == nil {
		t.Fatal("expected checksum mismatch to be rejected")
	}
}
