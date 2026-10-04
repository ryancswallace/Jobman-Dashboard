package api

import "time"

// Target describes configured execution capabilities, never inferred health or capacity.
type Target struct {
	Scope
	TargetID   string           `json:"targetId"`
	Name       string           `json:"name"`
	Kind       string           `json:"kind"`
	State      string           `json:"state"`
	Revision   string           `json:"revision"`
	CreatedAt  time.Time        `json:"createdAt"`
	UpdatedAt  time.Time        `json:"updatedAt"`
	AsOf       time.Time        `json:"asOf"`
	Generation TargetGeneration `json:"generation"`
}
type TargetPartition struct {
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
}
type TargetStore struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type TargetProvider struct {
	Kind        string `json:"kind"`
	Region      string `json:"region,omitempty"`
	ClusterName string `json:"clusterName,omitempty"`
}
type TargetGeneration struct {
	ID                  string            `json:"id"`
	Number              string            `json:"number"`
	ExecutionBackend    string            `json:"executionBackend"`
	Transport           string            `json:"transport"`
	Runtimes            []string          `json:"runtimes"`
	OperatingSystems    []string          `json:"operatingSystems"`
	Architectures       []string          `json:"architectures"`
	Capabilities        []string          `json:"capabilities"`
	Partitions          []TargetPartition `json:"partitions"`
	PartitionCount      string            `json:"partitionCount"`
	PartitionsTruncated bool              `json:"partitionsTruncated"`
	LogStore            *TargetStore      `json:"logStore,omitempty"`
	ArtifactStores      []TargetStore     `json:"artifactStores"`
	Provider            TargetProvider    `json:"provider"`
}
type TargetTotal struct {
	Scope
	Total string    `json:"total"`
	AsOf  time.Time `json:"asOf"`
}
type TargetPage struct {
	Page[Target]
	Total  string        `json:"total"`
	Totals []TargetTotal `json:"totals"`
}
type TargetDetail struct {
	Target       Target         `json:"target"`
	Sources      []SourceStatus `json:"sources"`
	Completeness string         `json:"completeness"`
	FetchedAt    time.Time      `json:"fetchedAt"`
}
type TargetPartitionPage struct {
	Page[TargetPartition]
	TargetID     string `json:"targetId"`
	GenerationID string `json:"generationId"`
	Total        string `json:"total"`
}
