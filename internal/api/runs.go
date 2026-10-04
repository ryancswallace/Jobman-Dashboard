package api

import "time"

// JobRun exposes only source-reported run facts for selecting existing evidence.
type JobRun struct {
	ID                 string    `json:"id"`
	Number             string    `json:"number"`
	Phase              string    `json:"phase"`
	DesiredState       string    `json:"desiredState"`
	Outcome            string    `json:"outcome,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
	ExecutionID        string    `json:"executionId,omitempty"`
	ExecutionPhase     string    `json:"executionPhase,omitempty"`
	TargetID           string    `json:"targetId,omitempty"`
	TargetGenerationID string    `json:"targetGenerationId,omitempty"`
	Backend            string    `json:"backend,omitempty"`
	Confidence         string    `json:"confidence,omitempty"`
}
type RunPage struct {
	Page[JobRun]
	Total string `json:"total"`
}
type RunDetail struct {
	Run          JobRun         `json:"run"`
	Sources      []SourceStatus `json:"sources"`
	Completeness string         `json:"completeness"`
	FetchedAt    time.Time      `json:"fetchedAt"`
}
