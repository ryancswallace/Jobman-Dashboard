package api

import "time"

type ReportRequest struct {
	Profile string `json:"profile"`
	RunID   string `json:"runId,omitempty"`
}

// Report distinguishes a mutable queue task from original immutable semantic
// IDs. Detail appears only after reauthorization and exact pair verification.
type Report struct {
	Scope
	TaskID             string        `json:"taskId"`
	JobID              string        `json:"jobId"`
	SourceRevision     string        `json:"sourceRevision"`
	RunID              string        `json:"runId,omitempty"`
	Profile            string        `json:"profile"`
	State              string        `json:"state"`
	CreatedAt          time.Time     `json:"createdAt"`
	ExpiresAt          time.Time     `json:"expiresAt"`
	Outdated           bool          `json:"outdated"`
	ReportID           string        `json:"reportId,omitempty"`
	EvidenceID         string        `json:"evidenceId,omitempty"`
	AnalysisEvidenceID string        `json:"analysisEvidenceId,omitempty"`
	FailureCode        string        `json:"failureCode,omitempty"`
	Detail             *ReportDetail `json:"detail,omitempty"`
}
type ReportPage struct {
	Items      []Report  `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
	FetchedAt  time.Time `json:"fetchedAt"`
}
type ReportVersions struct {
	Companion        string `json:"companion"`
	Engine           string `json:"engine"`
	Jobman           string `json:"jobman"`
	Collector        string `json:"collector"`
	EvidenceSchema   int    `json:"evidenceSchema"`
	ReportSchema     int    `json:"reportSchema"`
	GenerationSchema int    `json:"generationSchema"`
	ProposalSchema   int    `json:"proposalSchema"`
}
type ReportAnalyzer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type ReportGenerator struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Profile  string `json:"profile"`
	Locality string `json:"locality"`
}
type ReportConfidence struct {
	Score int    `json:"score"`
	Band  string `json:"band"`
	Basis string `json:"basis"`
}
type Finding struct {
	ID                    string           `json:"id"`
	Code                  string           `json:"code"`
	Category              string           `json:"category"`
	Severity              string           `json:"severity"`
	Title                 string           `json:"title"`
	Explanation           string           `json:"explanation"`
	Confidence            ReportConfidence `json:"confidence"`
	SupportingEvidence    []string         `json:"supportingEvidence"`
	ContradictingEvidence []string         `json:"contradictingEvidence"`
	ContradictingFindings []string         `json:"contradictingFindings"`
	Analyzer              string           `json:"analyzer"`
}
type ReportAction struct {
	ID                   string   `json:"id"`
	Code                 string   `json:"code"`
	Kind                 string   `json:"kind"`
	Summary              string   `json:"summary"`
	Description          string   `json:"description"`
	SupportingEvidence   []string `json:"supportingEvidence"`
	RequiresConfirmation bool     `json:"requiresConfirmation"`
}
type ReportRetry struct {
	Verdict            string           `json:"verdict"`
	ExistingPolicy     string           `json:"existingPolicy"`
	Confidence         ReportConfidence `json:"confidence"`
	Rationale          string           `json:"rationale"`
	Reasons            []string         `json:"reasons"`
	SupportingEvidence []string         `json:"supportingEvidence"`
	EarliestAt         *time.Time       `json:"earliestAt,omitempty"`
}
type CitationRef struct {
	ID               string `json:"id"`
	Code             string `json:"code"`
	Label            string `json:"label"`
	Kind             string `json:"kind"`
	SourceEvidenceID string `json:"sourceEvidenceId,omitempty"`
	StartOffset      string `json:"startOffset,omitempty"`
	EndOffset        string `json:"endOffset,omitempty"`
}
type ReportMissingEvidence struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}
type ReportWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type ReportOmission struct {
	Code    string   `json:"code"`
	Affects []string `json:"affects"`
}
type ReportRedaction struct {
	Code    string   `json:"code"`
	Affects []string `json:"affects"`
	Count   string   `json:"count"`
}
type ReportDisclosure struct {
	ProviderInvoked      bool     `json:"providerInvoked"`
	GeneratedContentUsed bool     `json:"generatedContentUsed"`
	Locality             string   `json:"locality"`
	Profile              string   `json:"profile,omitempty"`
	Provider             string   `json:"provider,omitempty"`
	Model                string   `json:"model,omitempty"`
	RequestID            string   `json:"requestId,omitempty"`
	Classes              []string `json:"classes"`
	ItemIDs              []string `json:"itemIds"`
	ArtifactIDs          []string `json:"artifactIds"`
	EnrichmentIDs        []string `json:"enrichmentIds"`
	ItemCount            string   `json:"itemCount"`
	ArtifactCount        string   `json:"artifactCount"`
	EnrichmentCount      string   `json:"enrichmentCount"`
	ArtifactBytes        string   `json:"artifactBytes"`
	EnrichmentBytes      string   `json:"enrichmentBytes"`
	RequestBytes         string   `json:"requestBytes"`
	RedactionNoticeCount string   `json:"redactionNoticeCount"`
}
type ReportDetail struct {
	CapturedAt        time.Time               `json:"capturedAt"`
	GeneratedAt       time.Time               `json:"generatedAt"`
	ControlInstanceID string                  `json:"controlInstanceId"`
	ControlVersion    string                  `json:"controlVersion"`
	ContractVersion   string                  `json:"contractVersion"`
	Platform          string                  `json:"platform"`
	Phase             string                  `json:"phase"`
	Outcome           string                  `json:"outcome,omitempty"`
	Runs              []RunReference          `json:"runs"`
	Versions          ReportVersions          `json:"versions"`
	Mode              string                  `json:"mode"`
	PrimaryFindingID  string                  `json:"primaryFindingId"`
	Analyzers         []ReportAnalyzer        `json:"analyzers"`
	Generators        []ReportGenerator       `json:"generators"`
	Findings          []Finding               `json:"findings"`
	Actions           []ReportAction          `json:"actions"`
	Retry             ReportRetry             `json:"retry"`
	Citations         []CitationRef           `json:"citations"`
	MissingEvidence   []ReportMissingEvidence `json:"missingEvidence"`
	Warnings          []ReportWarning         `json:"warnings"`
	Disclosure        ReportDisclosure        `json:"disclosure"`
	Omissions         []ReportOmission        `json:"omissions"`
	RedactionNotices  []ReportRedaction       `json:"redactionNotices"`
}

// Citation bytes always come from the sealed object. Start/EndOffset address
// sanitized artifact data. Original offsets are supplied only when the selected
// byte mapping is exact; a whole artifact still records its original selection.
type Citation struct {
	CitationRef
	TaskID               string     `json:"taskId"`
	ReportID             string     `json:"reportId"`
	EvidenceID           string     `json:"evidenceId"`
	AnalysisEvidenceID   string     `json:"analysisEvidenceId"`
	ValueJSON            string     `json:"valueJSON,omitempty"`
	BytesBase64          *string    `json:"bytesBase64,omitempty"`
	RangeBasis           string     `json:"rangeBasis,omitempty"`
	OriginalStartOffset  string     `json:"originalStartOffset,omitempty"`
	OriginalEndOffset    string     `json:"originalEndOffset,omitempty"`
	OriginalOffsetsExact bool       `json:"originalOffsetsExact"`
	SourceEntityID       string     `json:"sourceEntityId,omitempty"`
	SourceRevision       string     `json:"sourceRevision,omitempty"`
	RunID                string     `json:"runId,omitempty"`
	RunNumber            string     `json:"runNumber,omitempty"`
	ExecutionID          string     `json:"executionId,omitempty"`
	Stream               string     `json:"stream,omitempty"`
	Quality              string     `json:"quality"`
	Disclosure           string     `json:"disclosure"`
	CapturedAt           *time.Time `json:"capturedAt,omitempty"`
	ObservedAt           *time.Time `json:"observedAt,omitempty"`
}
