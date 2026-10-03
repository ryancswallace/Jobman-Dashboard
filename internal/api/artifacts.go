package api

import "time"

// Artifact contains published metadata only. Availability never claims that
// the backing storage has been read or that a download is available.
type Artifact struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	RunID              string    `json:"runId"`
	RunNumber          string    `json:"runNumber"`
	ExecutionID        string    `json:"executionId"`
	TargetGenerationID string    `json:"targetGenerationId"`
	SizeBytes          string    `json:"sizeBytes"`
	Checksum           string    `json:"checksum"`
	PublishedAt        time.Time `json:"publishedAt"`
	Availability       string    `json:"availability"`
}

type ArtifactPage struct {
	Page[Artifact]
	Total string `json:"total"`
}
