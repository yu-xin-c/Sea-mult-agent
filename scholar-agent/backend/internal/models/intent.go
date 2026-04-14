package models

// IntentContext is the normalized planner input produced by intent recognition.
type IntentContext struct {
	RawIntent       string         `json:"raw_intent"`
	RewrittenIntent string         `json:"rewritten_intent,omitempty"`
	IntentType      string         `json:"intent_type"`
	Entities        map[string]any `json:"entities"`
	Constraints     map[string]any `json:"constraints"`
	Metadata        map[string]any `json:"metadata"`
	Confidence      float64        `json:"confidence,omitempty"`
	Reasoning       string         `json:"reasoning,omitempty"`
	Source          string         `json:"source,omitempty"` // "llm" or "rule_fallback"
}
