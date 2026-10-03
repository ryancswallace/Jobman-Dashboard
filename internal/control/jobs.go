package control

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type jobResponse struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		NamespaceID string `json:"namespaceId"`
		Namespace   string `json:"namespace"`
		ID          string `json:"id"`
		Name        string `json:"name"`
		Owner       *struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"owner"`
		Labels    map[string]string `json:"labels"`
		Revision  int64             `json:"revision"`
		CreatedAt time.Time         `json:"createdAt"`
		UpdatedAt time.Time         `json:"updatedAt"`
	} `json:"metadata"`
	Spec struct {
		Placement struct {
			TargetID           string `json:"targetId"`
			TargetGenerationID string `json:"targetGenerationId"`
			ExecutionBackend   string `json:"executionBackend"`
		} `json:"placement"`
	} `json:"spec"`
	Status struct {
		Imported  bool `json:"imported"`
		Lifecycle struct {
			api.Lifecycle
			StartedAt   *time.Time `json:"startedAt"`
			CompletedAt *time.Time `json:"completedAt"`
		} `json:"lifecycle"`
		CurrentRun *api.RunReference `json:"currentRun"`
		Group      struct {
			api.GroupReference
			GraphDisposition string `json:"graphDisposition"`
		} `json:"group"`
		Phase                 string `json:"phase"`
		DesiredState          string `json:"desiredState"`
		Outcome               string `json:"outcome"`
		ObservationConfidence string `json:"observationConfidence"`
		NativeID              string `json:"nativeId"`
		Scheduler             *struct {
			State      string     `json:"state"`
			Reason     string     `json:"reason"`
			Cluster    string     `json:"cluster"`
			ObservedAt *time.Time `json:"observedAt"`
		} `json:"scheduler"`
	} `json:"status"`
}

func (j jobResponse) normalize(deployment string, ns api.Namespace, principalID string) (api.Job, error) {
	m, s, p := j.Metadata, j.Status, j.Spec.Placement
	if j.APIVersion != contract || j.Kind != "Job" || m.NamespaceID != ns.ID || m.Namespace != ns.Name || !uuid(m.ID) || m.Revision < 1 || m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() || s.Phase == "" || s.DesiredState == "" || (p.TargetID != "" && !uuid(p.TargetID)) || (p.TargetGenerationID != "" && !uuid(p.TargetGenerationID)) {
		return api.Job{}, monitoring.ErrSource
	}
	if s.CurrentRun != nil && (!uuid(s.CurrentRun.ID) || !decimal(s.CurrentRun.Number) || s.CurrentRun.Number == "0" || (s.CurrentRun.ExecutionID != "" && !uuid(s.CurrentRun.ExecutionID))) {
		return api.Job{}, monitoring.ErrSource
	}
	if (s.Group.CollectionID != "" && !uuid(s.Group.CollectionID)) || (s.Group.GraphID != "" && !uuid(s.Group.GraphID)) || (s.Group.CollectionIndex != nil && *s.Group.CollectionIndex < 0) || (s.Group.GraphIndex != nil && *s.Group.GraphIndex < 0) {
		return api.Job{}, monitoring.ErrSource
	}
	result := api.Job{Scope: api.Scope{DeploymentID: deployment, NamespaceID: ns.ID}, ID: m.ID, Name: m.Name, TargetID: p.TargetID, TargetGenerationID: p.TargetGenerationID, Backend: p.ExecutionBackend, Revision: strconv.FormatInt(m.Revision, 10), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt, StartedAt: s.Lifecycle.StartedAt, CompletedAt: s.Lifecycle.CompletedAt, DesiredState: s.DesiredState, Phase: s.Phase, Outcome: s.Outcome, Confidence: s.ObservationConfidence, Labels: m.Labels, Disposition: s.Group.GraphDisposition, Imported: s.Imported, Lifecycle: &s.Lifecycle.Lifecycle, CurrentRun: s.CurrentRun, Group: &s.Group.GroupReference}
	if result.Labels == nil {
		result.Labels = map[string]string{}
	}
	if m.Owner != nil {
		if !uuid(m.Owner.ID) {
			return api.Job{}, monitoring.ErrSource
		}
		result.Owner = &api.Owner{ID: m.Owner.ID, DisplayName: m.Owner.DisplayName, IsCurrentUser: principalID != "" && m.Owner.ID == principalID}
	}
	if s.Scheduler != nil {
		result.Scheduler = &api.Scheduler{State: s.Scheduler.State, Reason: s.Scheduler.Reason, Cluster: s.Scheduler.Cluster, ObservedAt: s.Scheduler.ObservedAt, JobID: s.NativeID}
	} else if s.NativeID != "" {
		result.Scheduler = &api.Scheduler{JobID: s.NativeID}
	}
	return result, nil
}

func (c *Client) Jobs(ctx context.Context, actor monitoring.Actor, query monitoring.SourceQuery) (monitoring.JobPage, error) {
	if query.Limit < 1 || query.Limit > 200 || query.CreatedBefore.IsZero() || len(query.Cursor) > 8192 {
		return monitoring.JobPage{}, monitoring.ErrSource
	}
	d, ns, err := c.authorize(ctx, actor, api.Scope{DeploymentID: c.ID(), NamespaceID: query.NamespaceID}, "jobs.read")
	if err != nil {
		return monitoring.JobPage{}, err
	}
	q := url.Values{"limit": {strconv.Itoa(query.Limit)}, "createdBefore": {query.CreatedBefore.UTC().Format(time.RFC3339Nano)}}
	for key, value := range map[string]string{"pageToken": query.Cursor, "phase": query.Phase, "outcome": query.Outcome, "jobId": query.JobID} {
		if value != "" {
			q.Set(key, value)
		}
	}
	if query.Owner != "" {
		if query.Owner != "me" || !uuid(d.principalID) {
			return monitoring.JobPage{}, monitoring.ErrAuthority
		}
		q.Set("ownerPrincipalId", d.principalID)
	}
	if query.Attention {
		q.Set("confidence", "attention")
	}
	if query.CompletedFrom != nil {
		q.Set("completedFrom", query.CompletedFrom.UTC().Format(time.RFC3339Nano))
	}
	if query.CompletedTo != nil {
		q.Set("completedBefore", query.CompletedTo.UTC().Format(time.RFC3339Nano))
	}
	var response struct {
		APIVersion    string        `json:"apiVersion"`
		Kind          string        `json:"kind"`
		Items         []jobResponse `json:"items"`
		NextPageToken string        `json:"nextPageToken"`
		AsOf          time.Time     `json:"asOf"`
	}
	if err := c.get(ctx, actor, "jobs.read", ns.ID, "/v1/namespaces/"+ns.Name+"/jobs", q, &response); err != nil {
		return monitoring.JobPage{}, err
	}
	if response.APIVersion != contract || response.Kind != "JobList" || len(response.Items) > query.Limit || response.AsOf.IsZero() || len(response.NextPageToken) > 8192 || (response.NextPageToken != "" && (response.NextPageToken == query.Cursor || len(response.Items) == 0)) {
		return monitoring.JobPage{}, monitoring.ErrSource
	}
	result := monitoring.JobPage{Items: []api.Job{}, NextCursor: response.NextPageToken, AsOf: response.AsOf}
	seen := map[string]bool{}
	for _, value := range response.Items {
		job, err := value.normalize(c.ID(), ns, d.principalID)
		if err != nil {
			return monitoring.JobPage{}, err
		}
		if seen[job.ID] || job.CreatedAt.After(query.CreatedBefore) {
			return monitoring.JobPage{}, monitoring.ErrSource
		}
		seen[job.ID] = true
		result.Items = append(result.Items, job)
	}
	if err := c.recheck(ctx, actor, d, ns, "jobs.read"); err != nil {
		return monitoring.JobPage{}, err
	}
	return result, nil
}

func (c *Client) Job(ctx context.Context, actor monitoring.Actor, scope api.Scope, id string) (api.Job, error) {
	if !uuid(id) {
		return api.Job{}, monitoring.ErrNotFound
	}
	d, ns, err := c.authorize(ctx, actor, scope, "jobs.read")
	if err != nil {
		return api.Job{}, err
	}
	var response jobResponse
	if err := c.get(ctx, actor, "jobs.read", ns.ID, "/v1/namespaces/"+ns.Name+"/jobs/"+id, nil, &response); err != nil {
		return api.Job{}, err
	}
	job, err := response.normalize(c.ID(), ns, d.principalID)
	if err != nil {
		return api.Job{}, err
	}
	if job.ID != id {
		return api.Job{}, monitoring.ErrSource
	}
	if err := c.recheck(ctx, actor, d, ns, "jobs.read"); err != nil {
		return api.Job{}, err
	}
	return job, nil
}

func (c *Client) Summary(ctx context.Context, actor monitoring.Actor, scope api.Scope, window api.Window) (monitoring.Counts, error) {
	if window.From.IsZero() || !window.From.Before(window.To) {
		return monitoring.Counts{}, monitoring.ErrSource
	}
	d, ns, err := c.authorize(ctx, actor, scope, "jobs.read")
	if err != nil {
		return monitoring.Counts{}, err
	}
	q := url.Values{"completedFrom": {window.From.UTC().Format(time.RFC3339Nano)}, "completedBefore": {window.To.UTC().Format(time.RFC3339Nano)}}
	var response struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Summary    struct {
			NamespaceID           string            `json:"namespaceId"`
			Namespace             string            `json:"namespace"`
			AsOf                  time.Time         `json:"asOf"`
			CompletedFrom         time.Time         `json:"completedFrom"`
			CompletedBefore       time.Time         `json:"completedBefore"`
			Total                 string            `json:"total"`
			Active                string            `json:"active"`
			AwaitingExecution     string            `json:"awaitingExecution"`
			EvidenceAttention     string            `json:"evidenceAttention"`
			MissingCompletionTime string            `json:"missingCompletionTime"`
			ByPhase               map[string]string `json:"byPhase"`
			ByOutcome             map[string]string `json:"byOutcome"`
		} `json:"summary"`
	}
	if err := c.get(ctx, actor, "jobs.read", ns.ID, "/v1/namespaces/"+ns.Name+"/summary", q, &response); err != nil {
		return monitoring.Counts{}, err
	}
	s := response.Summary
	if response.APIVersion != contract || response.Kind != "NamespaceSummary" || s.NamespaceID != ns.ID || s.Namespace != ns.Name || s.AsOf.IsZero() || !s.CompletedFrom.Equal(window.From) || !s.CompletedBefore.Equal(window.To) || !decimal(s.Total) || s.ByPhase == nil || s.ByOutcome == nil {
		return monitoring.Counts{}, monitoring.ErrSource
	}
	result := monitoring.Counts{AsOf: s.AsOf, Terminal: map[string]int64{}}
	for _, field := range []struct {
		value string
		dest  *int64
	}{{s.Active, &result.Active}, {s.AwaitingExecution, &result.AwaitingExecution}, {s.EvidenceAttention, &result.EvidenceAttention}, {s.MissingCompletionTime, &result.MissingCompletionTime}} {
		if !decimal(field.value) {
			return monitoring.Counts{}, monitoring.ErrSource
		}
		*field.dest, _ = strconv.ParseInt(field.value, 10, 64)
	}
	for _, value := range s.ByPhase {
		if !decimal(value) {
			return monitoring.Counts{}, monitoring.ErrSource
		}
	}
	if running, ok := s.ByPhase["running"]; ok {
		result.Running, _ = strconv.ParseInt(running, 10, 64)
	}
	for outcome, value := range s.ByOutcome {
		if outcome == "" || len(outcome) > 128 || !decimal(value) {
			return monitoring.Counts{}, monitoring.ErrSource
		}
		result.Terminal[outcome], _ = strconv.ParseInt(value, 10, 64)
	}
	if err := c.recheck(ctx, actor, d, ns, "jobs.read"); err != nil {
		return monitoring.Counts{}, err
	}
	return result, nil
}
