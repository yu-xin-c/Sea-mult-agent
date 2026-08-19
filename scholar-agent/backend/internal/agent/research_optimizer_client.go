package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scholar-agent-backend/internal/models"
)

const (
	optimizerProfileRequestVersion   = "research-optimizer.profile-request/v1"
	optimizerSelectionRequestVersion = "research-optimizer.selection-request/v1"
	optimizerSelectionVersion        = "research-optimizer.selection/v1"
	optimizerValidationVersion       = "research-optimizer.validation/v1"
	optimizerMaxRequestBytes         = 2 * 1024 * 1024
	optimizerMaxResponseBytes        = 1024 * 1024
	optimizerProfileSampleBytes      = 768 * 1024
)

type researchOptimizer interface {
	Profile(context.Context, optimizerProfileRequest) (models.ExperimentDatasetFeatureProfile, error)
	Select(context.Context, optimizerSelectionRequest) (optimizerSelectionResponse, error)
	RecordOutcome(context.Context, models.ExperimentExperienceOutcome) error
	RecordValidation(context.Context, optimizerValidationRecord) error
}

type optimizerProfileRequest struct {
	Version  string                           `json:"version"`
	Manifest models.ExperimentDatasetManifest `json:"manifest"`
	Samples  map[string][]map[string]any      `json:"samples,omitempty"`
}

type optimizerSelectionRequest struct {
	Version              string                                  `json:"version"`
	CampaignID           string                                  `json:"campaign_id"`
	TrialNumber          int                                     `json:"trial_number"`
	Phase                string                                  `json:"phase"`
	Context              *models.ExperimentDatasetFeatureProfile `json:"context,omitempty"`
	Candidates           []models.ExperimentCandidate            `json:"candidates"`
	CandidateHints       []optimizerCandidateHint                `json:"candidate_hints"`
	InFlight             []models.ExperimentCandidate            `json:"in_flight,omitempty"`
	History              []models.ExperimentTrial                `json:"history"`
	BaselineScore        float64                                 `json:"baseline_score"`
	BestScore            float64                                 `json:"best_score"`
	RemainingTrials      int                                     `json:"remaining_trials"`
	RemainingWallSeconds int                                     `json:"remaining_wall_seconds"`
}

type optimizerCandidateHint struct {
	CandidateID  string `json:"candidate_id"`
	FrontierKind string `json:"frontier_kind"`
	BeamRank     int    `json:"beam_rank,omitempty"`
}

type optimizerSelectionResponse struct {
	Version               string   `json:"version"`
	PolicyVersion         string   `json:"policy_version"`
	CandidateID           string   `json:"candidate_id"`
	Route                 string   `json:"route,omitempty"`
	FrontierKind          string   `json:"frontier_kind,omitempty"`
	BeamRank              int      `json:"beam_rank,omitempty"`
	RouteVisitCount       int      `json:"route_visit_count,omitempty"`
	RouteMeanReward       float64  `json:"route_mean_reward,omitempty"`
	RouteTopKMeanReward   float64  `json:"route_top_k_mean_reward,omitempty"`
	RouteBestReward       float64  `json:"route_best_reward,omitempty"`
	RouteExplorationBonus float64  `json:"route_exploration_bonus,omitempty"`
	NodeVisitCount        int      `json:"node_visit_count,omitempty"`
	NodeMeanReward        float64  `json:"node_mean_reward,omitempty"`
	NodeExplorationBonus  float64  `json:"node_exploration_bonus,omitempty"`
	VirtualVisits         int      `json:"virtual_visits,omitempty"`
	SelectionScore        float64  `json:"selection_score,omitempty"`
	Propensity            float64  `json:"propensity"`
	PredictedReward       *float64 `json:"predicted_reward,omitempty"`
	ReasonCodes           []string `json:"reason_codes,omitempty"`
}

type optimizerValidationRecord struct {
	Version        string    `json:"version"`
	CampaignID     string    `json:"campaign_id"`
	Status         string    `json:"status"`
	RequestedRuns  int       `json:"requested_runs"`
	PassedRuns     int       `json:"passed_runs"`
	SearchBaseline float64   `json:"search_baseline"`
	SearchBest     float64   `json:"search_best"`
	RecordedAt     time.Time `json:"recorded_at"`
}

type httpResearchOptimizer struct {
	baseURL string
	token   string
	client  *http.Client
}

func newHTTPResearchOptimizerFromEnv() researchOptimizer {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("RESEARCH_OPTIMIZER_URL")), "/")
	if baseURL == "" {
		return nil
	}
	timeout := 5 * time.Second
	return &httpResearchOptimizer{
		baseURL: baseURL,
		token:   strings.TrimSpace(os.Getenv("RESEARCH_OPTIMIZER_API_TOKEN")),
		client:  &http.Client{Timeout: timeout},
	}
}

func (c *httpResearchOptimizer) Profile(ctx context.Context, request optimizerProfileRequest) (models.ExperimentDatasetFeatureProfile, error) {
	var response models.ExperimentDatasetFeatureProfile
	err := c.post(ctx, "/v1/profile", request, &response)
	return response, err
}

func (c *httpResearchOptimizer) Select(ctx context.Context, request optimizerSelectionRequest) (optimizerSelectionResponse, error) {
	var response optimizerSelectionResponse
	err := c.post(ctx, "/v1/select", request, &response)
	return response, err
}

func (c *httpResearchOptimizer) RecordOutcome(ctx context.Context, outcome models.ExperimentExperienceOutcome) error {
	return c.post(ctx, "/v1/experience/outcome", outcome, nil)
}

func (c *httpResearchOptimizer) RecordValidation(ctx context.Context, record optimizerValidationRecord) error {
	return c.post(ctx, "/v1/experience/validation", record, nil)
}

func (c *httpResearchOptimizer) post(ctx context.Context, path string, payload any, output any) error {
	if c == nil || c.client == nil || c.baseURL == "" {
		return fmt.Errorf("research optimizer is not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(body) > optimizerMaxRequestBytes {
		return fmt.Errorf("research optimizer request exceeds 2 MiB")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, optimizerMaxResponseBytes+1)
	responseBody, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if len(responseBody) > optimizerMaxResponseBytes {
		return fmt.Errorf("research optimizer response exceeds 1 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("research optimizer returned HTTP %d: %s", response.StatusCode, truncateBenchmarkText(string(responseBody), 1000))
	}
	if output == nil || len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("decode research optimizer response: %w", err)
	}
	return nil
}

func experimentFallbackFeatureProfile(manifest models.ExperimentDatasetManifest) models.ExperimentDatasetFeatureProfile {
	numeric := make(map[string]float64, len(manifest.Counts))
	for key, value := range manifest.Counts {
		numeric[key] = float64(value)
	}
	boolean := make(map[string]bool, len(manifest.Capabilities))
	for key, value := range manifest.Capabilities {
		boolean[key] = value
	}
	fingerprint := experimentDatasetFingerprint(manifest.SourceFiles)
	profilePayload, _ := json.Marshal(struct {
		Domain      string             `json:"domain"`
		Adapter     string             `json:"adapter"`
		Fingerprint string             `json:"fingerprint"`
		Numeric     map[string]float64 `json:"numeric"`
		Boolean     map[string]bool    `json:"boolean"`
	}{manifest.Domain, manifest.Adapter, fingerprint, numeric, boolean})
	profileHash := sha256.Sum256(profilePayload)
	return models.ExperimentDatasetFeatureProfile{
		Version: models.ExperimentFeatureVersion, ID: "context-" + hex.EncodeToString(profileHash[:8]),
		Extractor: "manifest-fallback/v1", Domain: manifest.Domain, Adapter: manifest.Adapter,
		DatasetFingerprint: fingerprint, Numeric: numeric, Boolean: boolean, CreatedAt: time.Now().UTC(),
	}
}

func experimentDatasetFingerprint(files []models.ResearchFileHash) string {
	ordered := append([]models.ResearchFileHash(nil), files...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Path == ordered[j].Path {
			return ordered[i].SHA256 < ordered[j].SHA256
		}
		return ordered[i].Path < ordered[j].Path
	})
	hash := sha256.New()
	for _, file := range ordered {
		_, _ = hash.Write([]byte(file.Path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(strings.ToLower(file.SHA256)))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (a *ResearchCodingAgent) profileExperimentDataset(ctx context.Context, workspacePath string, manifest models.ExperimentDatasetManifest) models.ExperimentDatasetFeatureProfile {
	fallback := experimentFallbackFeatureProfile(manifest)
	if a == nil || a.Optimizer == nil {
		return fallback
	}
	samples, sampleErr := experimentOptimizerProfileSamples(workspacePath, manifest)
	if sampleErr != nil {
		logToContext(ctx, "[%s] research optimizer sampling failed; using manifest fallback: %v", a.Name, sampleErr)
		return fallback
	}
	profile, err := a.Optimizer.Profile(ctx, optimizerProfileRequest{
		Version: optimizerProfileRequestVersion, Manifest: manifest, Samples: samples,
	})
	if err != nil {
		logToContext(ctx, "[%s] research optimizer profile unavailable; using manifest fallback: %v", a.Name, err)
		return fallback
	}
	if err := validateExperimentFeatureProfile(profile, manifest); err != nil {
		logToContext(ctx, "[%s] research optimizer profile rejected; using manifest fallback: %v", a.Name, err)
		return fallback
	}
	return profile
}

func validateExperimentFeatureProfile(profile models.ExperimentDatasetFeatureProfile, manifest models.ExperimentDatasetManifest) error {
	if profile.Version != models.ExperimentFeatureVersion || strings.TrimSpace(profile.ID) == "" || strings.TrimSpace(profile.Extractor) == "" {
		return fmt.Errorf("feature profile identity is incomplete")
	}
	if profile.Domain != manifest.Domain || profile.Adapter != manifest.Adapter || profile.DatasetFingerprint != experimentDatasetFingerprint(manifest.SourceFiles) {
		return fmt.Errorf("feature profile does not match the dataset manifest")
	}
	if len(profile.Numeric) > 128 || len(profile.Boolean) > 128 {
		return fmt.Errorf("feature profile exceeds the bounded feature count")
	}
	for key, value := range profile.Numeric {
		if strings.TrimSpace(key) == "" || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("feature profile contains an invalid numeric feature")
		}
	}
	return nil
}

func experimentOptimizerProfileSamples(workspacePath string, manifest models.ExperimentDatasetManifest) (map[string][]map[string]any, error) {
	if manifest.Adapter != "retrieval.v1" {
		return nil, nil
	}
	result := map[string][]map[string]any{}
	for _, item := range []struct {
		name string
		keys []string
	}{
		{name: "corpus", keys: []string{"id", "text", "links"}},
		{name: "search_cases", keys: []string{"id", "query", "relevant_doc_ids"}},
	} {
		relative := strings.TrimSpace(manifest.Assets[item.name])
		path, err := benchmarkPathInWorkspace(workspacePath, relative)
		if err != nil {
			return nil, err
		}
		rows, err := readExperimentOptimizerSample(path, item.keys, 256, optimizerProfileSampleBytes)
		if err != nil {
			return nil, err
		}
		result[item.name] = rows
	}
	return result, nil
}

func readExperimentOptimizerSample(path string, keys []string, limit int, maxBytes int) ([]map[string]any, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("optimizer profile asset is unavailable or not a regular file: %s", filepath.Base(path))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows := make([]map[string]any, 0, min(limit, 256))
	encodedBytes := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() && len(rows) < limit {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var source map[string]any
		if err := json.Unmarshal(line, &source); err != nil {
			return nil, fmt.Errorf("decode optimizer profile sample: %w", err)
		}
		row := make(map[string]any, len(keys))
		for _, key := range keys {
			if value, exists := source[key]; exists {
				row[key] = boundedExperimentOptimizerValue(key, value)
			}
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("encode optimizer profile sample: %w", err)
		}
		if encodedBytes+len(encoded) > maxBytes {
			break
		}
		encodedBytes += len(encoded)
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func boundedExperimentOptimizerValue(key string, value any) any {
	switch typed := value.(type) {
	case string:
		limit := 4096
		if key == "id" {
			limit = 256
		}
		return truncateBenchmarkText(typed, limit)
	case []any:
		result := make([]any, 0, min(len(typed), 32))
		for _, item := range typed[:min(len(typed), 32)] {
			result = append(result, truncateBenchmarkText(fmt.Sprint(item), 256))
		}
		return result
	case bool, float64, nil:
		return typed
	default:
		return truncateBenchmarkText(fmt.Sprint(typed), 256)
	}
}

func experimentCampaignID(task *models.Task, specHash string, started time.Time) string {
	identity := strings.TrimSpace(specHash) + ":" + started.UTC().Format(time.RFC3339Nano)
	if task != nil && strings.TrimSpace(task.ID) != "" {
		identity = task.ID + ":" + identity
	}
	hash := sha256.Sum256([]byte(identity))
	return "campaign-" + hex.EncodeToString(hash[:10])
}

func (a *ResearchCodingAgent) selectExperimentCandidate(
	ctx context.Context,
	campaignID string,
	trialNumber int,
	phase string,
	spec models.ExperimentResearchSpec,
	ledger models.ExperimentTrialLedger,
	queue []models.ExperimentCandidate,
	inFlight []models.ExperimentCandidate,
	remainingWallSeconds int,
) (models.ExperimentCandidate, []models.ExperimentCandidate, models.ExperimentPolicyDecision) {
	frontier, hints := experimentSelectionFrontier(queue, phase, ledger.Trials, spec.Direction, spec.BeamWidth, spec.ExplorationSlots)
	availableIDs := make([]string, len(frontier))
	requestHints := make([]optimizerCandidateHint, 0, len(frontier))
	for index := range frontier {
		availableIDs[index] = frontier[index].ID
		hint := hints[frontier[index].ID]
		requestHints = append(requestHints, optimizerCandidateHint{
			CandidateID: frontier[index].ID, FrontierKind: hint.FrontierKind, BeamRank: hint.BeamRank,
		})
	}
	selectedIndex, fallbackStats := experimentFallbackSelection(phase, frontier, hints, ledger.Trials, inFlight)
	decision := models.ExperimentPolicyDecision{
		Version: models.ExperimentDecisionVersion, CampaignID: campaignID, TrialNumber: trialNumber,
		Phase: phase, PolicyVersion: fallbackStats.PolicyVersion, CandidateID: frontier[selectedIndex].ID,
		AvailableCandidateIDs: append([]string(nil), availableIDs...), Propensity: 1,
		Route: fallbackStats.Route, FrontierKind: fallbackStats.FrontierKind, BeamRank: fallbackStats.BeamRank,
		RouteVisitCount: fallbackStats.RouteVisitCount,
		RouteMeanReward: fallbackStats.RouteMeanReward, RouteTopKMeanReward: fallbackStats.RouteTopKMeanReward,
		RouteBestReward: fallbackStats.RouteBestReward, RouteExplorationBonus: fallbackStats.RouteExplorationBonus,
		NodeVisitCount: fallbackStats.NodeVisitCount, NodeMeanReward: fallbackStats.NodeMeanReward,
		NodeExplorationBonus: fallbackStats.NodeExplorationBonus, VirtualVisits: fallbackStats.VirtualVisits,
		SelectionScore: fallbackStats.SelectionScore, ReasonCodes: append([]string(nil), fallbackStats.ReasonCodes...),
		Fallback: true, SelectedAt: time.Now().UTC(),
	}
	if a != nil && a.Optimizer != nil {
		response, err := a.Optimizer.Select(ctx, optimizerSelectionRequest{
			Version: optimizerSelectionRequestVersion, CampaignID: campaignID, TrialNumber: trialNumber,
			Phase:   phase,
			Context: spec.ContextProfile, Candidates: append([]models.ExperimentCandidate(nil), frontier...),
			CandidateHints: requestHints,
			InFlight:       append([]models.ExperimentCandidate(nil), inFlight...),
			History:        append([]models.ExperimentTrial(nil), ledger.Trials...), BaselineScore: ledger.BaselineScore,
			BestScore: ledger.BestScore, RemainingTrials: spec.MaxTrials - ledger.CompletedTrials,
			RemainingWallSeconds: max(0, remainingWallSeconds),
		})
		if err != nil {
			decision.ReasonCodes = []string{"optimizer_unavailable"}
			logToContext(ctx, "[%s] research optimizer selection unavailable; using hierarchical Go fallback: %v", a.Name, err)
		} else if index, validationErr := validateOptimizerSelection(response, frontier, hints); validationErr != nil {
			decision.ReasonCodes = []string{"optimizer_response_rejected"}
			logToContext(ctx, "[%s] research optimizer selection rejected; using hierarchical Go fallback: %v", a.Name, validationErr)
		} else {
			selectedIndex = index
			decision.PolicyVersion = response.PolicyVersion
			decision.CandidateID = response.CandidateID
			decision.Route = response.Route
			decision.FrontierKind = response.FrontierKind
			decision.BeamRank = response.BeamRank
			decision.RouteVisitCount = response.RouteVisitCount
			decision.RouteMeanReward = response.RouteMeanReward
			decision.RouteTopKMeanReward = response.RouteTopKMeanReward
			decision.RouteBestReward = response.RouteBestReward
			decision.RouteExplorationBonus = response.RouteExplorationBonus
			decision.NodeVisitCount = response.NodeVisitCount
			decision.NodeMeanReward = response.NodeMeanReward
			decision.NodeExplorationBonus = response.NodeExplorationBonus
			decision.VirtualVisits = response.VirtualVisits
			decision.SelectionScore = response.SelectionScore
			decision.Propensity = response.Propensity
			decision.PredictedReward = response.PredictedReward
			decision.ReasonCodes = append([]string(nil), response.ReasonCodes...)
			decision.Fallback = false
		}
	}
	selected := frontier[selectedIndex]
	remaining := make([]models.ExperimentCandidate, 0, len(queue)-1)
	for _, candidate := range queue {
		if candidate.ID != selected.ID {
			remaining = append(remaining, candidate)
		}
	}
	return selected, remaining, decision
}

type experimentFrontierHint struct {
	FrontierKind string
	BeamRank     int
}

const experimentMaxPolicyCandidates = 256

func experimentSelectionFrontier(
	queue []models.ExperimentCandidate,
	phase string,
	history []models.ExperimentTrial,
	direction string,
	beamWidth int,
	explorationSlots int,
) ([]models.ExperimentCandidate, map[string]experimentFrontierHint) {
	hints := map[string]experimentFrontierHint{}
	if len(queue) == 0 {
		return nil, hints
	}
	if phase == models.ExperimentPhaseModelDefaults {
		frontier := make([]models.ExperimentCandidate, 0, len(queue))
		for _, candidate := range queue {
			if candidate.Depth == 0 {
				hints[candidate.ID] = experimentFrontierHint{FrontierKind: models.ExperimentFrontierBeam, BeamRank: 1}
				frontier = append(frontier, candidate)
			}
		}
		return frontier, hints
	}

	beamRanks := experimentBeamParentRanks(history, direction, beamWidth)
	nodeVisits := experimentNodeVisitCounts(history)
	type queuedCandidate struct {
		Candidate models.ExperimentCandidate
		Index     int
	}
	routeOrder := []string{}
	beamByRoute := map[string][]queuedCandidate{}
	explorationByRoute := map[string][]queuedCandidate{}
	seenRoute := map[string]struct{}{}
	for index, candidate := range queue {
		if candidate.Depth <= 0 {
			continue
		}
		if _, exists := seenRoute[candidate.Strategy]; !exists {
			seenRoute[candidate.Strategy] = struct{}{}
			routeOrder = append(routeOrder, candidate.Strategy)
		}
		item := queuedCandidate{Candidate: candidate, Index: index}
		if rank := beamRanks[candidate.Strategy][candidate.ParentID]; rank > 0 {
			beamByRoute[candidate.Strategy] = append(beamByRoute[candidate.Strategy], item)
			hints[candidate.ID] = experimentFrontierHint{FrontierKind: models.ExperimentFrontierBeam, BeamRank: rank}
		} else {
			explorationByRoute[candidate.Strategy] = append(explorationByRoute[candidate.Strategy], item)
		}
	}
	for route, candidates := range explorationByRoute {
		sort.SliceStable(candidates, func(left, right int) bool {
			leftVisits := nodeVisits[candidates[left].Candidate.ParentID]
			rightVisits := nodeVisits[candidates[right].Candidate.ParentID]
			if leftVisits != rightVisits {
				return leftVisits < rightVisits
			}
			if candidates[left].Candidate.Depth != candidates[right].Candidate.Depth {
				return candidates[left].Candidate.Depth < candidates[right].Candidate.Depth
			}
			return candidates[left].Index < candidates[right].Index
		})
		limit := min(explorationSlots, len(candidates))
		for _, item := range candidates[:limit] {
			beamByRoute[route] = append(beamByRoute[route], item)
			hints[item.Candidate.ID] = experimentFrontierHint{FrontierKind: models.ExperimentFrontierExploration}
		}
	}

	frontier := make([]models.ExperimentCandidate, 0, min(len(queue), experimentMaxPolicyCandidates))
	for round := 0; len(frontier) < experimentMaxPolicyCandidates; round++ {
		added := false
		for _, route := range routeOrder {
			if round >= len(beamByRoute[route]) {
				continue
			}
			frontier = append(frontier, beamByRoute[route][round].Candidate)
			added = true
			if len(frontier) == experimentMaxPolicyCandidates {
				break
			}
		}
		if !added {
			break
		}
	}
	return frontier, hints
}

func experimentBeamParentRanks(history []models.ExperimentTrial, direction string, beamWidth int) map[string]map[string]int {
	byRoute := map[string][]models.ExperimentTrial{}
	for _, trial := range history {
		if trial.Score != nil {
			byRoute[trial.Candidate.Strategy] = append(byRoute[trial.Candidate.Strategy], trial)
		}
	}
	ranks := map[string]map[string]int{}
	for route, trials := range byRoute {
		sort.SliceStable(trials, func(left, right int) bool {
			if *trials[left].Score == *trials[right].Score {
				if trials[left].DurationMS == trials[right].DurationMS {
					return trials[left].Number < trials[right].Number
				}
				return trials[left].DurationMS < trials[right].DurationMS
			}
			if direction == "minimize" {
				return *trials[left].Score < *trials[right].Score
			}
			return *trials[left].Score > *trials[right].Score
		})
		ranks[route] = map[string]int{}
		for index, trial := range trials[:min(beamWidth, len(trials))] {
			ranks[route][trial.Candidate.ID] = index + 1
		}
	}
	return ranks
}

func experimentNodeVisitCounts(history []models.ExperimentTrial) map[string]int {
	visits := map[string]int{}
	for _, trial := range history {
		if trial.Reward == nil {
			continue
		}
		path := trial.BackpropPath
		if len(path) == 0 {
			path = []string{trial.Candidate.ID}
		}
		for _, nodeID := range path {
			visits[nodeID]++
		}
	}
	return visits
}

type experimentSelectionStats struct {
	PolicyVersion         string
	Route                 string
	FrontierKind          string
	BeamRank              int
	RouteVisitCount       int
	RouteMeanReward       float64
	RouteTopKMeanReward   float64
	RouteBestReward       float64
	RouteExplorationBonus float64
	NodeVisitCount        int
	NodeMeanReward        float64
	NodeExplorationBonus  float64
	VirtualVisits         int
	SelectionScore        float64
	ReasonCodes           []string
}

type experimentRewardStats struct {
	Visits int
	Sum    float64
	Values []float64
}

func (stats *experimentRewardStats) add(reward float64) {
	stats.Visits++
	stats.Sum += reward
	stats.Values = append(stats.Values, reward)
}

func (stats experimentRewardStats) topKMean(limit int) float64 {
	if len(stats.Values) == 0 {
		return 0
	}
	values := append([]float64(nil), stats.Values...)
	sort.Sort(sort.Reverse(sort.Float64Slice(values)))
	limit = min(max(1, limit), len(values))
	sum := 0.0
	for _, value := range values[:limit] {
		sum += value
	}
	return sum / float64(limit)
}

func (stats experimentRewardStats) best() float64 {
	if len(stats.Values) == 0 {
		return 0
	}
	best := stats.Values[0]
	for _, value := range stats.Values[1:] {
		best = math.Max(best, value)
	}
	return best
}

func (stats experimentRewardStats) mean() float64 {
	if stats.Visits == 0 {
		return 0
	}
	return stats.Sum / float64(stats.Visits)
}

func experimentFallbackSelection(phase string, candidates []models.ExperimentCandidate, hints map[string]experimentFrontierHint, history []models.ExperimentTrial, inFlight []models.ExperimentCandidate) (int, experimentSelectionStats) {
	if phase == models.ExperimentPhaseModelDefaults {
		hint := hints[candidates[0].ID]
		return 0, experimentSelectionStats{
			PolicyVersion: models.ExperimentPolicyModelEnumeration,
			Route:         candidates[0].Strategy, FrontierKind: hint.FrontierKind, BeamRank: hint.BeamRank,
			ReasonCodes: []string{"model_default_required", "bounded_exhaustive_enumeration"},
		}
	}

	routeStats := map[string]*experimentRewardStats{}
	nodeStats := map[string]*experimentRewardStats{}
	totalVisits := 0
	for _, trial := range history {
		if trial.Reward == nil {
			continue
		}
		route := routeStats[trial.Candidate.Strategy]
		if route == nil {
			route = &experimentRewardStats{}
			routeStats[trial.Candidate.Strategy] = route
		}
		route.add(*trial.Reward)
		totalVisits++
		path := trial.BackpropPath
		if len(path) == 0 {
			path = []string{trial.Candidate.ID}
		}
		for _, nodeID := range path {
			node := nodeStats[nodeID]
			if node == nil {
				node = &experimentRewardStats{}
				nodeStats[nodeID] = node
			}
			node.add(*trial.Reward)
		}
	}
	routeVirtual := map[string]int{}
	nodeVirtual := map[string]int{}
	for _, candidate := range inFlight {
		routeVirtual[candidate.Strategy]++
		parentID := candidate.ParentID
		if parentID == "" {
			parentID = candidate.ID
		}
		nodeVirtual[parentID]++
	}

	const explorationScale = 0.35
	const virtualLoss = 0.05
	routeScores := map[string]float64{}
	routeBonuses := map[string]float64{}
	selectedRoute := ""
	selectedRouteScore := math.Inf(-1)
	for _, candidate := range candidates {
		if _, exists := routeScores[candidate.Strategy]; exists {
			continue
		}
		stats := routeStats[candidate.Strategy]
		if stats == nil {
			stats = &experimentRewardStats{}
		}
		virtual := routeVirtual[candidate.Strategy]
		bonus := explorationScale * math.Sqrt(math.Log(float64(totalVisits+len(inFlight)+2))/float64(max(1, stats.Visits+virtual)))
		score := stats.topKMean(3) + bonus - virtualLoss*float64(virtual)
		routeScores[candidate.Strategy], routeBonuses[candidate.Strategy] = score, bonus
		if score > selectedRouteScore {
			selectedRoute, selectedRouteScore = candidate.Strategy, score
		}
	}

	selectedIndex := -1
	selectedNodeScore := math.Inf(-1)
	selectedParent := ""
	selectedNodeBonus := 0.0
	for index, candidate := range candidates {
		if candidate.Strategy != selectedRoute {
			continue
		}
		parentID := candidate.ParentID
		if parentID == "" {
			parentID = candidate.ID
		}
		stats := nodeStats[parentID]
		if stats == nil {
			stats = &experimentRewardStats{}
		}
		virtual := nodeVirtual[parentID]
		routeVisits := 0
		if routeStats[selectedRoute] != nil {
			routeVisits = routeStats[selectedRoute].Visits
		}
		bonus := explorationScale * math.Sqrt(math.Log(float64(routeVisits+routeVirtual[selectedRoute]+2))/float64(max(1, stats.Visits+virtual)))
		score := stats.mean() + bonus - virtualLoss*float64(virtual)
		if score > selectedNodeScore {
			selectedIndex, selectedNodeScore, selectedParent, selectedNodeBonus = index, score, parentID, bonus
		}
	}
	if selectedIndex < 0 {
		selectedIndex = 0
		selectedRoute = candidates[0].Strategy
	}
	route := routeStats[selectedRoute]
	if route == nil {
		route = &experimentRewardStats{}
	}
	node := nodeStats[selectedParent]
	if node == nil {
		node = &experimentRewardStats{}
	}
	virtual := routeVirtual[selectedRoute] + nodeVirtual[selectedParent]
	reasons := []string{"outer_ucb_route_selection", "inner_uct_parameter_path"}
	selectedHint := hints[candidates[selectedIndex].ID]
	if selectedHint.FrontierKind == models.ExperimentFrontierBeam {
		reasons = append(reasons, "top_k_beam_parent")
	} else {
		reasons = append(reasons, "low_visit_exploration_lane")
	}
	if virtual > 0 {
		reasons = append(reasons, "in_flight_virtual_visit_penalty")
	}
	return selectedIndex, experimentSelectionStats{
		PolicyVersion: models.ExperimentPolicyHierarchicalSearch, Route: selectedRoute,
		FrontierKind: selectedHint.FrontierKind, BeamRank: selectedHint.BeamRank,
		RouteVisitCount: route.Visits, RouteMeanReward: route.mean(), RouteTopKMeanReward: route.topKMean(3),
		RouteBestReward: route.best(), RouteExplorationBonus: routeBonuses[selectedRoute],
		NodeVisitCount: node.Visits, NodeMeanReward: node.mean(), NodeExplorationBonus: selectedNodeBonus,
		VirtualVisits: virtual, SelectionScore: selectedRouteScore + selectedNodeScore, ReasonCodes: reasons,
	}
}

func validateOptimizerSelection(response optimizerSelectionResponse, candidates []models.ExperimentCandidate, hints map[string]experimentFrontierHint) (int, error) {
	if response.Version != optimizerSelectionVersion || strings.TrimSpace(response.PolicyVersion) == "" {
		return -1, fmt.Errorf("selection identity is incomplete")
	}
	if response.Propensity <= 0 || response.Propensity > 1 || math.IsNaN(response.Propensity) || math.IsInf(response.Propensity, 0) {
		return -1, fmt.Errorf("selection propensity is invalid")
	}
	if response.PredictedReward != nil && (math.IsNaN(*response.PredictedReward) || math.IsInf(*response.PredictedReward, 0)) {
		return -1, fmt.Errorf("predicted reward is invalid")
	}
	for _, value := range []float64{response.RouteMeanReward, response.RouteTopKMeanReward, response.RouteBestReward, response.RouteExplorationBonus, response.NodeMeanReward, response.NodeExplorationBonus, response.SelectionScore} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return -1, fmt.Errorf("selection statistics are invalid")
		}
	}
	if response.RouteVisitCount < 0 || response.NodeVisitCount < 0 || response.VirtualVisits < 0 {
		return -1, fmt.Errorf("selection visit counts are invalid")
	}
	for index := range candidates {
		if candidates[index].ID == response.CandidateID {
			if response.Route != "" && response.Route != candidates[index].Strategy {
				return -1, fmt.Errorf("selected route does not match the candidate strategy")
			}
			expected := hints[response.CandidateID]
			if response.FrontierKind != expected.FrontierKind || response.BeamRank != expected.BeamRank {
				return -1, fmt.Errorf("selected frontier evidence does not match the frozen candidate hint")
			}
			return index, nil
		}
	}
	return -1, fmt.Errorf("selected candidate is not in the frozen queue")
}

func defaultExperimentRewardSpec() models.ExperimentRewardSpec {
	return models.ExperimentRewardSpec{
		Version: models.ExperimentRewardVersion, QualityTransform: "baseline_scaled_delta",
		DurationPenaltyPerSecond: 0.0001, FailurePenalty: 1,
	}
}

func experimentReward(spec models.ExperimentResearchSpec, baseline float64, score *float64, durationMS int64, failed bool) (float64, *float64) {
	durationPenalty := spec.RewardSpec.DurationPenaltyPerSecond * float64(max(int64(0), durationMS)) / 1000
	if failed || score == nil {
		return -spec.RewardSpec.FailurePenalty - durationPenalty, nil
	}
	delta := experimentDelta(spec.Direction, *score, baseline)
	denominator := math.Max(math.Abs(baseline), 1)
	normalized := delta / denominator
	return normalized - durationPenalty, experimentFloatPointer(delta)
}

func (a *ResearchCodingAgent) recordExperimentOutcome(ctx context.Context, outcome models.ExperimentExperienceOutcome) {
	if a == nil || a.Optimizer == nil {
		return
	}
	if err := a.Optimizer.RecordOutcome(ctx, outcome); err != nil {
		logToContext(ctx, "[%s] research optimizer outcome recording failed: %v", a.Name, err)
	}
}
