package api

import "time"

type LogRange struct {
	BytesBase64 string     `json:"bytesBase64"`
	ExecutionID string     `json:"executionId"`
	Stream      string     `json:"stream"`
	RunID       string     `json:"runId,omitempty"`
	RunNumber   string     `json:"runNumber,omitempty"`
	StartOffset string     `json:"startOffset"`
	EndOffset   string     `json:"endOffset"`
	NextCursor  string     `json:"nextCursor,omitempty"`
	State       string     `json:"state"`
	Truncated   bool       `json:"truncated"`
	CapturedAt  *time.Time `json:"capturedAt,omitempty"`
}
