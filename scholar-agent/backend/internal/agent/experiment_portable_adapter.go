package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scholar-agent-backend/internal/models"
)

const (
	portableExperimentAdapterName = "portable.v1"
	portableExperimentSpecPath    = experimentWorkspaceDirectory + "/portable_spec.json"
	portableExperimentUploadDir   = experimentWorkspaceDirectory + "/uploads"
)

type portableExperimentAdapter struct{}

func (portableExperimentAdapter) Name() string   { return portableExperimentAdapterName }
func (portableExperimentAdapter) Domain() string { return "*" }

func (portableExperimentAdapter) Matches(_ *models.Task, files []benchmarkUploadedFile) bool {
	for _, file := range files {
		name := strings.ToLower(filepath.Base(file.Name))
		if name == "experiment.json" || name == "research_experiment.json" {
			return true
		}
	}
	return false
}

func (a portableExperimentAdapter) Prepare(_ context.Context, _ *models.Task, files []benchmarkUploadedFile) (workspace string, manifest models.ExperimentDatasetManifest, err error) {
	var sourceSpec models.ExperimentResearchSpec
	var specFile benchmarkUploadedFile
	for _, file := range files {
		name := strings.ToLower(filepath.Base(file.Name))
		if name == "experiment.json" || name == "research_experiment.json" {
			specFile = file
			break
		}
	}
	if strings.TrimSpace(specFile.StoragePath) == "" {
		return "", manifest, fmt.Errorf("portable experiment.json is required")
	}
	raw, err := os.ReadFile(filepath.Clean(specFile.StoragePath))
	if err != nil {
		return "", manifest, err
	}
	if len(raw) > 1024*1024 || json.Unmarshal(raw, &sourceSpec) != nil {
		return "", manifest, fmt.Errorf("portable experiment.json is invalid or exceeds 1 MiB")
	}
	if sourceSpec.Version != models.ExperimentSpecVersion || len(sourceSpec.Strategies) == 0 {
		return "", manifest, fmt.Errorf("portable experiment.json must use %s and declare strategies", models.ExperimentSpecVersion)
	}

	workspace, err = os.MkdirTemp("", "scholar-experiment-portable-")
	if err != nil {
		return "", manifest, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(workspace)
		}
	}()
	uploadRoot := filepath.Join(workspace, filepath.FromSlash(portableExperimentUploadDir))
	if err := os.MkdirAll(uploadRoot, 0o700); err != nil {
		return "", manifest, err
	}
	seenNames := map[string]struct{}{}
	sourceHashes := make([]models.ResearchFileHash, 0, len(files))
	frozenPaths := []string{}
	assets := map[string]string{"spec": portableExperimentSpecPath}
	for _, file := range files {
		path := filepath.Clean(strings.TrimSpace(file.StoragePath))
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", manifest, fmt.Errorf("portable asset %q is unavailable or not a regular file", file.Name)
		}
		if info.Size() <= 0 || info.Size() > experimentMaxUploadBytes {
			return "", manifest, fmt.Errorf("portable asset %q must be between 1 byte and 256 MiB", file.Name)
		}
		base := filepath.Base(file.Name)
		if base == "." || base == "" {
			return "", manifest, fmt.Errorf("portable asset name %q is invalid", file.Name)
		}
		key := strings.ToLower(base)
		if _, exists := seenNames[key]; exists {
			return "", manifest, fmt.Errorf("portable assets contain duplicate basename %q", base)
		}
		seenNames[key] = struct{}{}
		hash, hashErr := sha256File(path)
		if hashErr != nil {
			return "", manifest, hashErr
		}
		if file.SHA256 != "" && !strings.EqualFold(file.SHA256, hash) {
			return "", manifest, fmt.Errorf("portable asset %q checksum mismatch", file.Name)
		}
		sourceHashes = append(sourceHashes, models.ResearchFileHash{Path: base, SHA256: hash})
		if key == "experiment.json" || key == "research_experiment.json" {
			continue
		}
		destination := filepath.Join(uploadRoot, base)
		if err := copyPortableExperimentFile(path, destination); err != nil {
			return "", manifest, err
		}
		relative := filepath.ToSlash(filepath.Join(portableExperimentUploadDir, base))
		assets[base] = relative
		frozenPaths = append(frozenPaths, relative)
	}
	canonicalSpec, _ := json.Marshal(sourceSpec)
	canonicalPath := filepath.Join(workspace, filepath.FromSlash(portableExperimentSpecPath))
	if err := os.WriteFile(canonicalPath, canonicalSpec, 0o400); err != nil {
		return "", manifest, err
	}
	frozenPaths = append(frozenPaths, portableExperimentSpecPath)
	frozen, err := hashExperimentFiles(workspace, frozenPaths)
	if err != nil {
		return "", manifest, err
	}
	domain := chooseNonEmpty(strings.TrimSpace(sourceSpec.Domain), "generic")
	manifest = models.ExperimentDatasetManifest{
		Version: models.ExperimentDatasetVersion, Name: chooseNonEmpty(sourceSpec.Name, "portable experiment"),
		Domain: domain, Adapter: a.Name(), Mapping: map[string]string{"contract": specFile.Name},
		Counts: map[string]int{"source_files": len(sourceHashes)}, Capabilities: map[string]bool{"portable_contract": true},
		Assets: assets, SplitMethod: "adapter_defined", SourceFiles: sourceHashes, FrozenFiles: frozen, CreatedAt: time.Now().UTC(),
	}
	sort.Slice(manifest.SourceFiles, func(i, j int) bool { return manifest.SourceFiles[i].Path < manifest.SourceFiles[j].Path })
	succeeded = true
	return workspace, manifest, nil
}

func (a portableExperimentAdapter) BuildSpec(task *models.Task, workspacePath string, manifest models.ExperimentDatasetManifest) (models.ExperimentResearchSpec, error) {
	if manifest.Version != models.ExperimentDatasetVersion || manifest.Adapter != a.Name() {
		return models.ExperimentResearchSpec{}, fmt.Errorf("dataset manifest is not compatible with %s", a.Name())
	}
	if err := verifyExperimentFiles(workspacePath, manifest.FrozenFiles); err != nil {
		return models.ExperimentResearchSpec{}, err
	}
	path, err := benchmarkPathInWorkspace(workspacePath, manifest.Assets["spec"])
	if err != nil {
		return models.ExperimentResearchSpec{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return models.ExperimentResearchSpec{}, err
	}
	var spec models.ExperimentResearchSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return spec, err
	}
	spec.Adapter = a.Name()
	spec.Domain = chooseNonEmpty(strings.TrimSpace(spec.Domain), manifest.Domain, "generic")
	spec.CandidateKind = "strategy_config"
	spec.SearchCommand, err = rewritePortableExperimentCommand(spec.SearchCommand, manifest.Assets)
	if err != nil {
		return spec, fmt.Errorf("portable search command: %w", err)
	}
	spec.HoldoutCommand, err = rewritePortableExperimentCommand(spec.HoldoutCommand, manifest.Assets)
	if err != nil {
		return spec, fmt.Errorf("portable holdout command: %w", err)
	}
	maxTrials := spec.MaxTrials
	if maxTrials == 0 {
		maxTrials = 12
	}
	maxWallSeconds := spec.MaxWallSeconds
	if maxWallSeconds == 0 {
		maxWallSeconds = 900
	}
	validationRuns := spec.ValidationRuns
	if validationRuns == 0 {
		validationRuns = 3
	}
	spec.MaxTrials = boundedTaskInt(task, "experiment_max_trials", maxTrials, 1, 40)
	parallelTrials := spec.MaxParallelTrials
	if parallelTrials == 0 {
		parallelTrials = 1
	}
	spec.MaxParallelTrials = boundedTaskInt(task, "experiment_max_parallel_trials", parallelTrials, 1, 4)
	if spec.EvaluationIsolation == "" {
		spec.EvaluationIsolation = models.ExperimentExecutionSerial
	}
	spec.MaxWallSeconds = boundedTaskInt(task, "experiment_max_wall_seconds", maxWallSeconds, 30, 3600)
	spec.ValidationRuns = boundedTaskInt(task, "experiment_validation_runs", validationRuns, 1, 5)
	if target, err := optionalExperimentTaskFloat(task, "experiment_target_score", -1e12, 1e12); err != nil {
		return spec, err
	} else if target != nil {
		spec.TargetScore = target
	}
	if target, err := optionalExperimentTaskFloat(task, "experiment_holdout_target_score", -1e12, 1e12); err != nil {
		return spec, err
	} else if target != nil {
		spec.HoldoutTargetScore = target
	}
	if spec.HoldoutTargetScore == nil && spec.TargetScore != nil {
		value := *spec.TargetScore
		spec.HoldoutTargetScore = &value
	}
	spec.FrozenFiles = append([]models.ResearchFileHash(nil), manifest.FrozenFiles...)
	spec.CreatedAt = time.Now().UTC()
	return spec, nil
}

func rewritePortableExperimentCommand(command []string, assets map[string]string) ([]string, error) {
	result := make([]string, len(command))
	for index, item := range command {
		result[index] = item
		for name, relative := range assets {
			if name == "spec" {
				continue
			}
			placeholder := "{asset:" + name + "}"
			result[index] = strings.ReplaceAll(result[index], placeholder, "/workspace/"+filepath.ToSlash(relative))
		}
		if strings.Contains(result[index], "{asset:") {
			return nil, fmt.Errorf("command references an unknown asset placeholder in %q", item)
		}
	}
	return result, nil
}

func copyPortableExperimentFile(source, destination string) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, raw, 0o400)
}
