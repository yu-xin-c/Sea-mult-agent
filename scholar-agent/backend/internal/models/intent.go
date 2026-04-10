package models

// IntentContext is the normalized planner input produced by intent recognition.
type IntentContext struct {
	RawIntent   string         `json:"raw_intent"`
	IntentType  string         `json:"intent_type"`
	Entities    map[string]any `json:"entities"`
	Constraints map[string]any `json:"constraints"`
	Metadata    map[string]any `json:"metadata"`
}
