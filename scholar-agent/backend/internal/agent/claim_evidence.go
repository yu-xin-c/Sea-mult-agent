package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"

	"github.com/cloudwego/eino/schema"
)

const (
	claimMaxPaperBytes            = 96 * 1024
	claimMaxClaims                = 16
	claimMaxCriteriaPerClaim      = 6
	claimMaxEvidenceArtifactBytes = 16 * 1024
	claimMaxEvidenceContextBytes  = 128 * 1024
	claimMaxEvidenceSummaryRunes  = 320
)

var claimEvidenceArtifactKeys = []string{
	"parsed_paper",
	"repo_manifest",
	"reproduction_mode_report",
	"dependency_install_report",
	"run_metrics",
	"paper_debug_report",
	"paper_patch_manifest",
	"comparison_report",
	"rerun_metrics",
	"rerun_report",
	"gap_debug_report",
	"gap_patch_manifest",
	"result_plot",
}

var claimExecutionDerivedEvidence = map[string]struct{}{
	"run_metrics":       {},
	"rerun_metrics":     {},
	"comparison_report": {},
	"result_plot":       {},
}

type claimEvidenceProposal struct {
	Findings []claimCriterionFinding `json:"findings"`
}

type claimCriterionFinding struct {
	ClaimID       string   `json:"claim_id"`
	CriterionID   string   `json:"criterion_id"`
	Status        string   `json:"status"`
	Confidence    float64  `json:"confidence"`
	ObservedValue string   `json:"observed_value"`
	EvidenceKeys  []string `json:"evidence_keys"`
	Reason        string   `json:"reason"`
}

type claimCriterionLocation struct {
	claimID   string
	criterion models.ClaimCriterion
}

func (a *LibrarianAgent) executeClaimRubricExtraction(ctx context.Context, task *models.Task) error {
	if a == nil || a.ChatModel == nil {
		return failClaimTask(task, fmt.Errorf("claim rubric model is not configured"))
	}
	parsedPaper := strings.TrimSpace(extractTaskInputLike(task, "parsed_paper"))
	if parsedPaper == "" {
		return failClaimTask(task, fmt.Errorf("claim rubric extraction requires parsed_paper"))
	}
	parsedPaper = truncateClaimBytes(parsedPaper, claimMaxPaperBytes)

	message, err := a.ChatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.ClaimRubricSystemPrompt},
		{Role: schema.User, Content: prompts.ClaimRubricUserPrompt(task.Description, parsedPaper)},
	})
	if err != nil {
		return failClaimTask(task, fmt.Errorf("claim rubric extraction failed: %w", err))
	}

	var proposed models.ClaimRubric
	if err := json.Unmarshal([]byte(cleanJSONResponse(message.Content)), &proposed); err != nil {
		return failClaimTask(task, fmt.Errorf("decode claim rubric: %w", err))
	}
	rubric, err := normalizeClaimRubric(proposed)
	if err != nil {
		return failClaimTask(task, err)
	}
	rubricPayload, err := json.Marshal(rubric)
	if err != nil {
		return failClaimTask(task, fmt.Errorf("encode claim rubric: %w", err))
	}
	report := renderClaimRubricReport(rubric)

	task.Result = report
	task.StructuredData = string(rubricPayload)
	task.Status = models.StatusCompleted
	setClaimArtifactValues(task, map[string]string{
		"claim_rubric":        string(rubricPayload),
		"claim_rubric_report": report,
	})
	logToContext(ctx, "[%s] froze %d paper claim(s) in rubric %s", a.Name, len(rubric.Claims), rubric.SHA256)
	return nil
}

func (a *DataAgent) executeClaimEvidenceBuild(ctx context.Context, task *models.Task) error {
	rubric, rubricJSON, err := claimRubricFromTask(task)
	if err != nil {
		return failClaimTask(task, err)
	}
	evidence, evidenceValues, evidenceContext := collectClaimEvidence(task)
	status := "assessed"
	statusReason := "all criterion verdicts were produced and validated against the bounded evidence inventory"
	adjudicationComplete := false

	var proposal claimEvidenceProposal
	if a == nil || a.ChatModel == nil {
		status = "degraded"
		statusReason = "claim adjudication model is unavailable; all criteria remain unverifiable"
	} else {
		message, generateErr := a.ChatModel.Generate(ctx, []*schema.Message{
			{Role: schema.System, Content: prompts.ClaimEvidenceSystemPrompt},
			{Role: schema.User, Content: prompts.ClaimEvidenceUserPrompt(rubricJSON, evidenceContext)},
		})
		if generateErr != nil {
			status = "degraded"
			statusReason = "claim adjudication failed; all criteria remain unverifiable: " + generateErr.Error()
		} else if decodeErr := json.Unmarshal([]byte(cleanJSONResponse(message.Content)), &proposal); decodeErr != nil {
			status = "degraded"
			statusReason = "claim adjudication returned invalid JSON; all criteria remain unverifiable: " + decodeErr.Error()
		} else {
			adjudicationComplete = true
		}
	}

	results, validationErr := buildClaimEvidenceResults(rubric, proposal, evidence, evidenceValues, adjudicationComplete, statusReason)
	if validationErr != nil {
		status = "degraded"
		statusReason = "claim adjudication violated the evidence contract; all criteria remain unverifiable: " + validationErr.Error()
		results, _ = buildClaimEvidenceResults(rubric, claimEvidenceProposal{}, evidence, evidenceValues, false, statusReason)
	}
	summary := summarizeClaimEvidence(results)
	graph := models.ClaimEvidenceGraph{
		Version:      models.ClaimEvidenceGraphVersion,
		Status:       status,
		StatusReason: truncateClaimRunes(statusReason, 1200),
		RubricSHA256: rubric.SHA256,
		Evidence:     evidence,
		Claims:       results,
		Summary:      summary,
	}
	graphPayload, err := json.Marshal(graph)
	if err != nil {
		return failClaimTask(task, fmt.Errorf("encode claim evidence graph: %w", err))
	}
	report := renderClaimEvidenceReport(rubric, graph)

	task.Result = report
	task.StructuredData = string(graphPayload)
	task.Status = models.StatusCompleted
	setClaimArtifactValues(task, map[string]string{
		"claim_evidence_graph":      string(graphPayload),
		"claim_verification_report": report,
	})
	logToContext(ctx, "[%s] built claim evidence graph: verified=%d partial=%d contradicted=%d unverifiable=%d blocked=%d",
		a.Name, summary.Verified, summary.PartiallyReproduced, summary.Contradicted, summary.Unverifiable, summary.BlockedByMissingAsset)
	return nil
}

func normalizeClaimRubric(proposed models.ClaimRubric) (models.ClaimRubric, error) {
	claims := proposed.Claims
	if len(claims) > claimMaxClaims {
		claims = claims[:claimMaxClaims]
	}
	normalized := models.ClaimRubric{
		Version:        models.ClaimRubricVersion,
		PaperTitle:     truncateClaimRunes(strings.TrimSpace(proposed.PaperTitle), 300),
		SourceArtifact: "parsed_paper",
		Claims:         make([]models.PaperClaim, 0, len(claims)),
	}
	if normalized.PaperTitle == "" {
		normalized.PaperTitle = "Unspecified paper"
	}

	for _, rawClaim := range claims {
		statement := truncateClaimRunes(strings.TrimSpace(rawClaim.Statement), 2000)
		if statement == "" {
			continue
		}
		title := truncateClaimRunes(strings.TrimSpace(rawClaim.Title), 240)
		if title == "" {
			title = truncateClaimRunes(statement, 120)
		}
		criteria := rawClaim.Criteria
		if len(criteria) > claimMaxCriteriaPerClaim {
			criteria = criteria[:claimMaxCriteriaPerClaim]
		}
		normalizedCriteria := make([]models.ClaimCriterion, 0, len(criteria))
		for _, rawCriterion := range criteria {
			description := truncateClaimRunes(strings.TrimSpace(rawCriterion.Description), 1200)
			if description == "" {
				continue
			}
			criterion := models.ClaimCriterion{
				Description:      description,
				MetricName:       truncateClaimRunes(strings.TrimSpace(rawCriterion.MetricName), 160),
				ExpectedValue:    rawCriterion.ExpectedValue,
				Tolerance:        rawCriterion.Tolerance,
				Unit:             truncateClaimRunes(strings.TrimSpace(rawCriterion.Unit), 80),
				RequiredEvidence: normalizeRequiredEvidence(rawCriterion.RequiredEvidence),
			}
			if criterion.ExpectedValue == nil {
				criterion.Tolerance = nil
			} else if criterion.Tolerance != nil && *criterion.Tolerance < 0 {
				criterion.Tolerance = nil
			}
			normalizedCriteria = append(normalizedCriteria, criterion)
		}
		if len(normalizedCriteria) == 0 {
			continue
		}

		claimIndex := len(normalized.Claims) + 1
		claimID := fmt.Sprintf("claim-%03d", claimIndex)
		for criterionIndex := range normalizedCriteria {
			normalizedCriteria[criterionIndex].ID = fmt.Sprintf("%s.criterion-%02d", claimID, criterionIndex+1)
		}
		normalized.Claims = append(normalized.Claims, models.PaperClaim{
			ID:            claimID,
			Title:         title,
			Statement:     statement,
			SourceLocator: claimSourceLocator(rawClaim.SourceLocator),
			ClaimType:     normalizeClaimType(rawClaim.ClaimType),
			Importance:    clampUnit(rawClaim.Importance),
			Criteria:      normalizedCriteria,
		})
	}
	if len(normalized.Claims) == 0 {
		return models.ClaimRubric{}, fmt.Errorf("claim rubric contains no valid independently gradable claims")
	}
	normalized.SHA256 = claimRubricSHA256(normalized)
	return normalized, nil
}

func claimRubricFromTask(task *models.Task) (models.ClaimRubric, string, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "claim_rubric"))
	if raw == "" {
		return models.ClaimRubric{}, "", fmt.Errorf("claim evidence build requires claim_rubric")
	}
	var rubric models.ClaimRubric
	if err := json.Unmarshal([]byte(raw), &rubric); err != nil {
		return models.ClaimRubric{}, "", fmt.Errorf("decode frozen claim rubric: %w", err)
	}
	if rubric.Version != models.ClaimRubricVersion || len(rubric.Claims) == 0 {
		return models.ClaimRubric{}, "", fmt.Errorf("claim rubric version or claims are invalid")
	}
	if expected := claimRubricSHA256(rubric); rubric.SHA256 == "" || rubric.SHA256 != expected {
		return models.ClaimRubric{}, "", fmt.Errorf("claim rubric SHA-256 mismatch")
	}
	canonicalRubric, err := normalizeClaimRubric(rubric)
	if err != nil {
		return models.ClaimRubric{}, "", fmt.Errorf("validate frozen claim rubric: %w", err)
	}
	receivedPayload, err := json.Marshal(rubric)
	if err != nil {
		return models.ClaimRubric{}, "", err
	}
	canonicalPayload, err := json.Marshal(canonicalRubric)
	if err != nil {
		return models.ClaimRubric{}, "", err
	}
	if string(receivedPayload) != string(canonicalPayload) {
		return models.ClaimRubric{}, "", fmt.Errorf("claim rubric is not in canonical frozen form")
	}
	return rubric, string(receivedPayload), nil
}

func collectClaimEvidence(task *models.Task) ([]models.ClaimEvidenceNode, map[string]string, string) {
	evidence := make([]models.ClaimEvidenceNode, 0, len(claimEvidenceArtifactKeys))
	values := make(map[string]string, len(claimEvidenceArtifactKeys))
	var contextBuilder strings.Builder
	contextBytes := 0
	for _, key := range claimEvidenceArtifactKeys {
		value := strings.TrimSpace(extractTaskInputLike(task, key))
		values[key] = value
		node := models.ClaimEvidenceNode{
			ID:           "evidence-" + strings.ReplaceAll(key, "_", "-"),
			ArtifactKey:  key,
			EvidenceType: claimEvidenceType(key),
			Available:    value != "",
		}
		if value != "" {
			sum := sha256.Sum256([]byte(value))
			node.SHA256 = hex.EncodeToString(sum[:])
			node.Summary = summarizeClaimEvidenceValue(key, value)
		}
		evidence = append(evidence, node)

		contextBuilder.WriteString("ARTIFACT key=")
		contextBuilder.WriteString(key)
		contextBuilder.WriteString(" type=")
		contextBuilder.WriteString(node.EvidenceType)
		contextBuilder.WriteString(" available=")
		contextBuilder.WriteString(fmt.Sprint(node.Available))
		if node.SHA256 != "" {
			contextBuilder.WriteString(" sha256=")
			contextBuilder.WriteString(node.SHA256)
		}
		contextBuilder.WriteString("\n")
		if value != "" {
			bounded := boundedClaimEvidenceContent(key, value)
			remaining := claimMaxEvidenceContextBytes - contextBytes
			if remaining > 0 {
				bounded = truncateClaimBytes(bounded, remaining)
				contextBuilder.WriteString(bounded)
				contextBuilder.WriteString("\n")
				contextBytes += len(bounded)
			}
		}
		contextBuilder.WriteString("END ARTIFACT\n\n")
	}
	return evidence, values, contextBuilder.String()
}

func buildClaimEvidenceResults(rubric models.ClaimRubric, proposal claimEvidenceProposal, evidence []models.ClaimEvidenceNode, evidenceValues map[string]string, requireComplete bool, fallbackReason string) ([]models.ClaimEvidenceResult, error) {
	criteria := map[string]claimCriterionLocation{}
	for _, claim := range rubric.Claims {
		for _, criterion := range claim.Criteria {
			criteria[criterion.ID] = claimCriterionLocation{claimID: claim.ID, criterion: criterion}
		}
	}
	evidenceByKey := make(map[string]models.ClaimEvidenceNode, len(evidence))
	for _, node := range evidence {
		evidenceByKey[node.ArtifactKey] = node
	}

	findings := make(map[string]models.ClaimCriterionVerdict, len(proposal.Findings))
	for _, raw := range proposal.Findings {
		location, ok := criteria[strings.TrimSpace(raw.CriterionID)]
		if !ok {
			return nil, fmt.Errorf("unknown criterion_id %q", raw.CriterionID)
		}
		if strings.TrimSpace(raw.ClaimID) != location.claimID {
			return nil, fmt.Errorf("criterion %s was assigned to the wrong claim", raw.CriterionID)
		}
		if _, duplicate := findings[raw.CriterionID]; duplicate {
			return nil, fmt.Errorf("duplicate criterion finding %s", raw.CriterionID)
		}
		status, ok := normalizeClaimVerificationStatus(raw.Status)
		if !ok {
			return nil, fmt.Errorf("invalid claim status %q", raw.Status)
		}
		evidenceIDs := make([]string, 0, len(raw.EvidenceKeys))
		seenEvidence := map[string]struct{}{}
		hasExecutionDerivedEvidence := false
		for _, rawKey := range raw.EvidenceKeys {
			key := strings.TrimSpace(rawKey)
			node, exists := evidenceByKey[key]
			if !exists {
				return nil, fmt.Errorf("criterion %s references unknown evidence key %q", raw.CriterionID, key)
			}
			if !node.Available || strings.TrimSpace(evidenceValues[key]) == "" {
				continue
			}
			if _, duplicate := seenEvidence[key]; duplicate {
				continue
			}
			seenEvidence[key] = struct{}{}
			evidenceIDs = append(evidenceIDs, node.ID)
			if _, direct := claimExecutionDerivedEvidence[key]; direct {
				hasExecutionDerivedEvidence = true
			}
		}
		reason := truncateClaimRunes(strings.TrimSpace(raw.Reason), 1600)
		confidence := clampUnit(raw.Confidence)
		if claimStatusRequiresExecution(status) && !hasExecutionDerivedEvidence {
			status = models.ClaimStatusUnverifiable
			if confidence > 0.25 {
				confidence = 0.25
			}
			reason = "downgraded because no execution-derived metric, comparison, or figure evidence was cited; " + reason
		}
		if reason == "" {
			reason = "no evidence-based reason was supplied"
		}
		findings[raw.CriterionID] = models.ClaimCriterionVerdict{
			CriterionID:   raw.CriterionID,
			Description:   location.criterion.Description,
			Status:        status,
			Confidence:    confidence,
			ObservedValue: truncateClaimRunes(strings.TrimSpace(raw.ObservedValue), 500),
			EvidenceIDs:   evidenceIDs,
			Reason:        reason,
		}
	}
	if requireComplete && len(findings) != len(criteria) {
		return nil, fmt.Errorf("expected %d criterion findings, received %d", len(criteria), len(findings))
	}

	results := make([]models.ClaimEvidenceResult, 0, len(rubric.Claims))
	for _, claim := range rubric.Claims {
		verdicts := make([]models.ClaimCriterionVerdict, 0, len(claim.Criteria))
		for _, criterion := range claim.Criteria {
			verdict, ok := findings[criterion.ID]
			if !ok {
				verdict = models.ClaimCriterionVerdict{
					CriterionID: criterion.ID,
					Description: criterion.Description,
					Status:      models.ClaimStatusUnverifiable,
					Confidence:  0,
					EvidenceIDs: []string{},
					Reason:      truncateClaimRunes(fallbackReason, 1600),
				}
			}
			verdicts = append(verdicts, verdict)
		}
		status, confidence := aggregateClaimVerdicts(verdicts)
		results = append(results, models.ClaimEvidenceResult{
			ClaimID:       claim.ID,
			Title:         claim.Title,
			Statement:     claim.Statement,
			SourceLocator: claim.SourceLocator,
			ClaimType:     claim.ClaimType,
			Status:        status,
			Confidence:    confidence,
			Criteria:      verdicts,
		})
	}
	return results, nil
}

func aggregateClaimVerdicts(verdicts []models.ClaimCriterionVerdict) (models.ClaimVerificationStatus, float64) {
	if len(verdicts) == 0 {
		return models.ClaimStatusUnverifiable, 0
	}
	counts := map[models.ClaimVerificationStatus]int{}
	confidence := 0.0
	for _, verdict := range verdicts {
		counts[verdict.Status]++
		confidence += verdict.Confidence
	}
	confidence /= float64(len(verdicts))
	switch {
	case counts[models.ClaimStatusContradicted] > 0:
		return models.ClaimStatusContradicted, confidence
	case counts[models.ClaimStatusVerified] == len(verdicts):
		return models.ClaimStatusVerified, confidence
	case counts[models.ClaimStatusPartiallyReproduced] > 0 || counts[models.ClaimStatusVerified] > 0:
		return models.ClaimStatusPartiallyReproduced, confidence
	case counts[models.ClaimStatusBlockedMissingAsset] > 0:
		return models.ClaimStatusBlockedMissingAsset, confidence
	default:
		return models.ClaimStatusUnverifiable, confidence
	}
}

func summarizeClaimEvidence(results []models.ClaimEvidenceResult) models.ClaimEvidenceSummary {
	summary := models.ClaimEvidenceSummary{TotalClaims: len(results)}
	criteriaWithEvidence := 0
	for _, result := range results {
		switch result.Status {
		case models.ClaimStatusVerified:
			summary.Verified++
		case models.ClaimStatusPartiallyReproduced:
			summary.PartiallyReproduced++
		case models.ClaimStatusContradicted:
			summary.Contradicted++
		case models.ClaimStatusBlockedMissingAsset:
			summary.BlockedByMissingAsset++
		default:
			summary.Unverifiable++
		}
		for _, criterion := range result.Criteria {
			summary.TotalCriteria++
			if len(criterion.EvidenceIDs) > 0 {
				criteriaWithEvidence++
			}
		}
	}
	if summary.TotalCriteria > 0 {
		summary.CriterionEvidenceCoverage = float64(criteriaWithEvidence) / float64(summary.TotalCriteria)
	}
	return summary
}

func renderClaimRubricReport(rubric models.ClaimRubric) string {
	var builder strings.Builder
	builder.WriteString("# Frozen Claim Rubric\n\n")
	builder.WriteString("- Paper: ")
	builder.WriteString(markdownClaimText(rubric.PaperTitle))
	builder.WriteString("\n- Version: `")
	builder.WriteString(rubric.Version)
	builder.WriteString("`\n- SHA-256: `")
	builder.WriteString(rubric.SHA256)
	builder.WriteString("`\n- Claims: ")
	builder.WriteString(fmt.Sprint(len(rubric.Claims)))
	builder.WriteString("\n\n| ID | Claim | Type | Source | Criteria |\n|---|---|---|---|---:|\n")
	for _, claim := range rubric.Claims {
		builder.WriteString("| `")
		builder.WriteString(claim.ID)
		builder.WriteString("` | ")
		builder.WriteString(markdownClaimText(claim.Title))
		builder.WriteString(" | ")
		builder.WriteString(claim.ClaimType)
		builder.WriteString(" | ")
		builder.WriteString(markdownClaimText(claim.SourceLocator))
		builder.WriteString(" | ")
		builder.WriteString(fmt.Sprint(len(claim.Criteria)))
		builder.WriteString(" |\n")
	}
	builder.WriteString("\nThe rubric is frozen before repository execution. Later result nodes may reference it but cannot rewrite its IDs or SHA-256.\n")
	return builder.String()
}

func renderClaimEvidenceReport(rubric models.ClaimRubric, graph models.ClaimEvidenceGraph) string {
	claimByID := make(map[string]models.PaperClaim, len(rubric.Claims))
	for _, claim := range rubric.Claims {
		claimByID[claim.ID] = claim
	}
	var builder strings.Builder
	builder.WriteString("# Claim Verification Report\n\n")
	builder.WriteString("- Graph status: `")
	builder.WriteString(graph.Status)
	builder.WriteString("`\n- Rubric SHA-256: `")
	builder.WriteString(graph.RubricSHA256)
	builder.WriteString("`\n- Evidence coverage: ")
	builder.WriteString(fmt.Sprintf("%.1f%%", graph.Summary.CriterionEvidenceCoverage*100))
	builder.WriteString("\n- Claims: ")
	builder.WriteString(fmt.Sprintf("%d verified, %d partial, %d contradicted, %d unverifiable, %d blocked\n\n",
		graph.Summary.Verified, graph.Summary.PartiallyReproduced, graph.Summary.Contradicted, graph.Summary.Unverifiable, graph.Summary.BlockedByMissingAsset))
	builder.WriteString("| Claim | Status | Confidence | Execution-derived evidence |\n|---|---|---:|---|\n")
	for _, result := range graph.Claims {
		claim := claimByID[result.ClaimID]
		evidenceIDs := []string{}
		for _, criterion := range result.Criteria {
			evidenceIDs = append(evidenceIDs, criterion.EvidenceIDs...)
		}
		evidenceIDs = cleanSortedClaimStrings(evidenceIDs)
		builder.WriteString("| `")
		builder.WriteString(result.ClaimID)
		builder.WriteString("` ")
		builder.WriteString(markdownClaimText(claim.Title))
		builder.WriteString(" | `")
		builder.WriteString(string(result.Status))
		builder.WriteString("` | ")
		builder.WriteString(fmt.Sprintf("%.2f", result.Confidence))
		builder.WriteString(" | ")
		builder.WriteString(markdownClaimText(strings.Join(evidenceIDs, ", ")))
		builder.WriteString(" |\n")
	}
	builder.WriteString("\n## Criterion Evidence\n")
	for _, result := range graph.Claims {
		claim := claimByID[result.ClaimID]
		builder.WriteString("\n### ")
		builder.WriteString(result.ClaimID)
		builder.WriteString(" ")
		builder.WriteString(markdownClaimText(claim.Title))
		builder.WriteString("\n")
		for _, verdict := range result.Criteria {
			builder.WriteString("\n- `")
			builder.WriteString(verdict.CriterionID)
			builder.WriteString("` **")
			builder.WriteString(string(verdict.Status))
			builder.WriteString("**")
			if verdict.ObservedValue != "" {
				builder.WriteString("; observed: ")
				builder.WriteString(markdownClaimText(verdict.ObservedValue))
			}
			builder.WriteString("; evidence: ")
			builder.WriteString(markdownClaimText(strings.Join(verdict.EvidenceIDs, ", ")))
			builder.WriteString("\n  ")
			builder.WriteString(markdownClaimText(verdict.Reason))
			builder.WriteString("\n")
		}
	}
	builder.WriteString("\n## Boundary\n\nA successful command is execution evidence, not scientific verification by itself. Missing metrics or protocol evidence remains `unverifiable`.\n")
	return builder.String()
}

func normalizeRequiredEvidence(values []string) []string {
	allowed := map[string]struct{}{
		"paper": {}, "repository": {}, "environment": {}, "run": {}, "metric": {}, "patch": {}, "comparison": {}, "figure": {},
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return []string{"paper", "run", "comparison"}
	}
	return out
}

func normalizeClaimType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "quantitative", "qualitative", "efficiency", "ablation", "robustness":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "qualitative"
	}
}

func claimSourceLocator(value string) string {
	value = truncateClaimRunes(strings.TrimSpace(value), 500)
	if value == "" {
		return "not specified in parsed artifact"
	}
	return value
}

func normalizeClaimVerificationStatus(value string) (models.ClaimVerificationStatus, bool) {
	status := models.ClaimVerificationStatus(strings.ToLower(strings.TrimSpace(value)))
	switch status {
	case models.ClaimStatusVerified, models.ClaimStatusPartiallyReproduced, models.ClaimStatusContradicted, models.ClaimStatusUnverifiable, models.ClaimStatusBlockedMissingAsset:
		return status, true
	default:
		return "", false
	}
}

func claimStatusRequiresExecution(status models.ClaimVerificationStatus) bool {
	return status == models.ClaimStatusVerified || status == models.ClaimStatusPartiallyReproduced || status == models.ClaimStatusContradicted
}

func claimRubricSHA256(rubric models.ClaimRubric) string {
	rubric.SHA256 = ""
	payload, _ := json.Marshal(rubric)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func claimEvidenceType(key string) string {
	switch key {
	case "parsed_paper":
		return "paper"
	case "repo_manifest":
		return "repository"
	case "reproduction_mode_report", "dependency_install_report":
		return "environment"
	case "run_metrics", "rerun_metrics":
		return "metric"
	case "paper_debug_report", "rerun_report", "gap_debug_report":
		return "run"
	case "paper_patch_manifest", "gap_patch_manifest":
		return "patch"
	case "comparison_report":
		return "comparison"
	case "result_plot":
		return "figure"
	default:
		return "artifact"
	}
}

func summarizeClaimEvidenceValue(key, value string) string {
	if key == "result_plot" {
		return fmt.Sprintf("image artifact present (%d encoded bytes)", len(value))
	}
	compact := strings.Join(strings.Fields(value), " ")
	return truncateClaimRunes(compact, claimMaxEvidenceSummaryRunes)
}

func boundedClaimEvidenceContent(key, value string) string {
	if key == "result_plot" {
		return fmt.Sprintf("[image artifact omitted; encoded_bytes=%d]", len(value))
	}
	return truncateClaimBytes(value, claimMaxEvidenceArtifactBytes)
}

func truncateClaimBytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && (value[limit]&0xC0) == 0x80 {
		limit--
	}
	return value[:limit] + "...[truncated]"
}

func truncateClaimRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "...[truncated]"
}

func markdownClaimText(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func cleanSortedClaimStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func setClaimArtifactValues(task *models.Task, values map[string]string) {
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = values
}

func failClaimTask(task *models.Task, err error) error {
	if task != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
	}
	return err
}
