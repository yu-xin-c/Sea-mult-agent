package models

const (
	ClaimRubricVersion        = "claim.rubric/v1"
	ClaimEvidenceGraphVersion = "claim.evidence/v1"
)

// ClaimVerificationStatus is the bounded verdict vocabulary used by the
// claim-to-evidence graph.
type ClaimVerificationStatus string

const (
	ClaimStatusVerified            ClaimVerificationStatus = "verified"
	ClaimStatusPartiallyReproduced ClaimVerificationStatus = "partially_reproduced"
	ClaimStatusContradicted        ClaimVerificationStatus = "contradicted"
	ClaimStatusUnverifiable        ClaimVerificationStatus = "unverifiable"
	ClaimStatusBlockedMissingAsset ClaimVerificationStatus = "blocked_by_missing_asset"
)

// ClaimCriterion is one independently gradable item under a paper claim.
type ClaimCriterion struct {
	ID               string   `json:"id"`
	Description      string   `json:"description"`
	MetricName       string   `json:"metric_name,omitempty"`
	ExpectedValue    *float64 `json:"expected_value,omitempty"`
	Tolerance        *float64 `json:"tolerance,omitempty"`
	Unit             string   `json:"unit,omitempty"`
	RequiredEvidence []string `json:"required_evidence"`
}

// PaperClaim is a top-level claim with one or more independently gradable
// criteria. IDs are assigned deterministically by the backend.
type PaperClaim struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Statement     string           `json:"statement"`
	SourceLocator string           `json:"source_locator"`
	ClaimType     string           `json:"claim_type"`
	Importance    float64          `json:"importance"`
	Criteria      []ClaimCriterion `json:"criteria"`
}

// ClaimRubric freezes the paper claims before repository execution so the
// acceptance criteria cannot drift after results are observed.
type ClaimRubric struct {
	Version        string       `json:"version"`
	PaperTitle     string       `json:"paper_title"`
	SourceArtifact string       `json:"source_artifact"`
	SHA256         string       `json:"sha256"`
	Claims         []PaperClaim `json:"claims"`
}

// ClaimEvidenceNode is immutable evidence inventory metadata. The graph stores
// hashes and bounded summaries rather than duplicating full upstream artifacts.
type ClaimEvidenceNode struct {
	ID           string `json:"id"`
	ArtifactKey  string `json:"artifact_key"`
	EvidenceType string `json:"evidence_type"`
	SHA256       string `json:"sha256,omitempty"`
	Available    bool   `json:"available"`
	Summary      string `json:"summary,omitempty"`
}

// ClaimCriterionVerdict records one model-assisted verdict after backend
// validation of IDs, status, confidence, and evidence references.
type ClaimCriterionVerdict struct {
	CriterionID   string                  `json:"criterion_id"`
	Description   string                  `json:"description"`
	Status        ClaimVerificationStatus `json:"status"`
	Confidence    float64                 `json:"confidence"`
	ObservedValue string                  `json:"observed_value,omitempty"`
	EvidenceIDs   []string                `json:"evidence_ids"`
	Reason        string                  `json:"reason"`
}

// ClaimEvidenceResult aggregates criterion verdicts for one top-level claim.
type ClaimEvidenceResult struct {
	ClaimID       string                  `json:"claim_id"`
	Title         string                  `json:"title"`
	Statement     string                  `json:"statement"`
	SourceLocator string                  `json:"source_locator"`
	ClaimType     string                  `json:"claim_type"`
	Status        ClaimVerificationStatus `json:"status"`
	Confidence    float64                 `json:"confidence"`
	Criteria      []ClaimCriterionVerdict `json:"criteria"`
}

// ClaimEvidenceSummary provides deterministic graph-level coverage statistics.
type ClaimEvidenceSummary struct {
	TotalClaims               int     `json:"total_claims"`
	TotalCriteria             int     `json:"total_criteria"`
	Verified                  int     `json:"verified"`
	PartiallyReproduced       int     `json:"partially_reproduced"`
	Contradicted              int     `json:"contradicted"`
	Unverifiable              int     `json:"unverifiable"`
	BlockedByMissingAsset     int     `json:"blocked_by_missing_asset"`
	CriterionEvidenceCoverage float64 `json:"criterion_evidence_coverage"`
}

// ClaimEvidenceGraph binds the frozen rubric to immutable upstream evidence and
// validated claim verdicts.
type ClaimEvidenceGraph struct {
	Version      string                `json:"version"`
	Status       string                `json:"status"`
	StatusReason string                `json:"status_reason,omitempty"`
	RubricSHA256 string                `json:"rubric_sha256"`
	Evidence     []ClaimEvidenceNode   `json:"evidence"`
	Claims       []ClaimEvidenceResult `json:"claims"`
	Summary      ClaimEvidenceSummary  `json:"summary"`
}
