package reports

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/observability"
	"github.com/ryancswallace/jobman-diagnose/deterministic"
	"github.com/ryancswallace/jobman-diagnose/diagnosis"
	"github.com/ryancswallace/jobman/diagnostic"
)

type Queue interface {
	EnqueueReport(context.Context, monitoring.Actor, Subject, string) (Task, error)
	ClaimReport(context.Context) (Claim, error)
	AnalyzeReport(context.Context, string, string) error
	CompleteReport(context.Context, string, string, Object, monitoring.Actor) error
	FailReport(context.Context, string, string, string) error
	ReportTask(context.Context, string, string) (Task, error)
	VerifyReportActor(context.Context, string, monitoring.Actor) error
	ListReportTasks(context.Context, string, api.Scope, string, string, int) ([]Task, error)
	DeleteExpiredReports(context.Context) ([]Object, error)
	ReportObjectReferenced(context.Context, string) (bool, error)
}

type Request struct {
	Scope                                 api.Scope
	JobID, RunID, Profile, IdempotencyKey string
}
type Result struct {
	Task     Task
	Pair     *Pair
	Outdated bool
}
type Page struct {
	Items      []Result
	NextCursor string
}

type ServiceConfig struct {
	Observer         *observability.Registry
	Sources          []Source
	Queue            Queue
	Objects          ObjectReader
	Logs             LogService
	Redaction        *RedactionPolicy
	CompanionVersion string
}
type Service struct {
	observer  *observability.Registry
	sources   map[string]Source
	queue     Queue
	objects   ObjectReader
	writer    objectWriter
	logs      LogService
	redaction *RedactionPolicy
	version   string
	engine    diagnosis.Diagnostician
}

func NewService(c ServiceConfig) (*Service, error) {
	if len(c.Sources) < 1 || len(c.Sources) > 32 || c.Queue == nil || c.Objects == nil || len(c.CompanionVersion) == 0 || len(c.CompanionVersion) > 128 {
		return nil, ErrInvalid
	}
	s := &Service{observer: c.Observer, sources: make(map[string]Source), queue: c.Queue, objects: c.Objects, logs: c.Logs, redaction: c.Redaction, version: c.CompanionVersion}
	s.writer, _ = c.Objects.(objectWriter)
	for _, src := range c.Sources {
		if src == nil || !uuidPattern.MatchString(src.ID()) || s.sources[src.ID()] != nil {
			return nil, ErrInvalid
		}
		s.sources[src.ID()] = src
	}
	var err error
	s.engine, err = deterministic.New(c.CompanionVersion, time.Now)
	if err != nil {
		return nil, ErrInvalid
	}
	return s, nil
}

func (s *Service) capture(ctx context.Context, a monitoring.Actor, request Request, revision uint64) (Snapshot, PreparedCollection, error) {
	var empty Snapshot
	var prepared PreparedCollection
	for _, id := range []string{request.Scope.DeploymentID, request.Scope.NamespaceID, request.JobID} {
		if !uuidPattern.MatchString(id) {
			return empty, prepared, ErrInvalid
		}
	}
	if request.RunID != "" && !uuidPattern.MatchString(request.RunID) || request.Profile != "metadata" && request.Profile != "include_log_tail" {
		return empty, prepared, ErrInvalid
	}
	source := s.sources[request.Scope.DeploymentID]
	if source == nil {
		return empty, prepared, monitoring.ErrNotFound
	}
	d, err := source.Discover(ctx, a)
	if err != nil {
		return empty, prepared, err
	}
	allowed := false
	for _, ns := range d.Deployment.Namespaces {
		if ns.ID == request.Scope.NamespaceID {
			allowed = ns.AuthorizationExpiresAt.After(time.Now()) && slices.Contains(ns.Capabilities, "jobs.read") && slices.Contains(ns.Capabilities, "evidence.read") && (request.Profile != "include_log_tail" || slices.Contains(ns.Capabilities, "logs.read"))
			break
		}
	}
	if !allowed {
		return empty, prepared, monitoring.ErrForbidden
	}
	if d.Deployment.ID != request.Scope.DeploymentID || !uuidPattern.MatchString(d.InstanceID) {
		return empty, prepared, monitoring.ErrSource
	}
	selection := diagnostic.SharedSelection{DeploymentID: request.Scope.DeploymentID, ControlInstanceID: d.InstanceID, NamespaceID: request.Scope.NamespaceID, JobID: request.JobID, RunID: request.RunID, ExpectedJobRevision: revision}
	snapshot, err := source.DiagnosticSnapshot(ctx, a, selection)
	if err != nil {
		return empty, prepared, err
	}
	if snapshot.RecoveryEpoch != d.RecoveryEpoch {
		return empty, prepared, snapshotChanged()
	}
	prepared, err = PrepareCollection(ctx, snapshot, selection, CollectionOptions{IncludeLogTail: request.Profile == "include_log_tail", Redaction: s.redaction})
	return snapshot, prepared, err
}

func (s *Service) subject(r Request, v Snapshot, p PreparedCollection) Subject {
	return Subject{Scope: r.Scope, ControlInstanceID: v.Value.Source.ControlInstanceID, RecoveryEpoch: v.RecoveryEpoch, JobID: r.JobID, Revision: strconv.FormatUint(v.Value.Job.Revision, 10), RunID: r.RunID, Profile: r.Profile, SnapshotFingerprint: p.SnapshotFingerprint, EngineVersion: diagnosis.SharedEngineVersion, CollectorVersion: diagnostic.SharedCollectorVersion, CompanionVersion: s.version, JobmanVersion: v.Value.JobmanVersion}
}
func subjectRequest(subject Subject) Request {
	return Request{Scope: subject.Scope, JobID: subject.JobID, RunID: subject.RunID, Profile: subject.Profile}
}
func (s *Service) current(ctx context.Context, a monitoring.Actor, task Task) (bool, error) {
	if err := s.queue.VerifyReportActor(ctx, task.ID, a); err != nil {
		return false, err
	}
	v, p, err := s.capture(ctx, a, subjectRequest(task.Subject), 0)
	if err != nil {
		return false, err
	}
	if v.Value.Source.ControlInstanceID != task.Subject.ControlInstanceID || v.RecoveryEpoch != task.Subject.RecoveryEpoch {
		return false, snapshotChanged()
	}
	if err := s.queue.VerifyReportActor(ctx, task.ID, a); err != nil {
		return false, err
	}
	return s.subject(subjectRequest(task.Subject), v, p) != task.Subject, nil
}

func (s *Service) Request(ctx context.Context, a monitoring.Actor, r Request) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := IdempotencyHash(r.IdempotencyKey); err != nil {
		return Result{}, err
	}
	v, p, err := s.capture(ctx, a, r, 0)
	if err != nil {
		return Result{}, err
	}
	task, err := s.queue.EnqueueReport(ctx, a, s.subject(r, v, p), r.IdempotencyKey)
	if err != nil {
		return Result{}, err
	}
	outdated, err := s.current(ctx, a, task)
	if err != nil {
		return Result{}, err
	}
	return Result{Task: task, Outdated: outdated}, nil
}

func (s *Service) Get(ctx context.Context, a monitoring.Actor, id string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if !uuidPattern.MatchString(id) {
		return Result{}, ErrInvalid
	}
	task, err := s.queue.ReportTask(ctx, a.Account.ID, id)
	if err != nil {
		return Result{}, err
	}
	outdated, err := s.current(ctx, a, task)
	if err != nil {
		return Result{}, err
	}
	result := Result{Task: task, Outdated: outdated}
	if task.State == "ready" {
		if task.Object == nil {
			return Result{}, ErrObject
		}
		pair, err := s.objects.Read(ctx, task.Subject, *task.Object)
		if err != nil {
			return Result{}, err
		}
		result.Pair = &pair
		// File access is outside SQL/source transactions; reauthorize the exact
		// stored profile again before returning any findings or sealed bytes.
		result.Outdated, err = s.current(ctx, a, task)
		if err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, a monitoring.Actor, scope api.Scope, job, before string, limit int) (Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	page := Page{Items: []Result{}}
	if limit < 1 || limit > 20 || before != "" && !uuidPattern.MatchString(before) {
		return page, ErrInvalid
	}
	if _, _, err := s.capture(ctx, a, Request{Scope: scope, JobID: job, Profile: "metadata"}, 0); err != nil {
		return page, err
	}
	tasks, err := s.queue.ListReportTasks(ctx, a.Account.ID, scope, job, before, limit+1)
	if err != nil {
		return page, err
	}
	if len(tasks) > limit {
		tasks = tasks[:limit]
		page.NextCursor = tasks[len(tasks)-1].ID
	}
	// At most one metadata snapshot per distinct selected run/profile in this
	// bounded page. No object or evidence bytes are loaded for catalog rows.
	type check struct {
		snapshot Snapshot
		prepared PreparedCollection
		err      error
	}
	checked := map[string]check{}
	for _, task := range tasks {
		if task.Subject.Scope != scope || task.Subject.JobID != job {
			return Page{}, ErrInvalid
		}
		if err := s.queue.VerifyReportActor(ctx, task.ID, a); err != nil {
			return Page{}, err
		}
		key := task.Subject.RunID + "/" + task.Subject.Profile
		c, ok := checked[key]
		if !ok {
			c.snapshot, c.prepared, c.err = s.capture(ctx, a, subjectRequest(task.Subject), 0)
			checked[key] = c
		}
		if c.err != nil {
			if isDenied(c.err) {
				continue
			}
			return Page{}, c.err
		}
		if c.snapshot.Value.Source.ControlInstanceID != task.Subject.ControlInstanceID || c.snapshot.RecoveryEpoch != task.Subject.RecoveryEpoch {
			continue
		}
		page.Items = append(page.Items, Result{Task: task, Outdated: s.subject(subjectRequest(task.Subject), c.snapshot, c.prepared) != task.Subject})
	}
	// A source check after preparing rows fences revocation during catalog work.
	for key, c := range checked {
		if c.err != nil {
			continue
		}
		for _, task := range tasks {
			if task.Subject.RunID+"/"+task.Subject.Profile == key {
				var latest Snapshot
				var prepared PreparedCollection
				if latest, prepared, err = s.capture(ctx, a, subjectRequest(task.Subject), 0); err != nil {
					return Page{}, err
				}
				if latest.Value.Source.ControlInstanceID != c.snapshot.Value.Source.ControlInstanceID || latest.RecoveryEpoch != c.snapshot.RecoveryEpoch {
					return Page{}, snapshotChanged()
				}
				for i := range page.Items {
					row := &page.Items[i]
					if row.Task.Subject.RunID+"/"+row.Task.Subject.Profile == key {
						row.Outdated = s.subject(subjectRequest(row.Task.Subject), latest, prepared) != row.Task.Subject
					}
				}
				break
			}
		}
	}
	for _, row := range page.Items {
		if err = s.queue.VerifyReportActor(ctx, row.Task.ID, a); err != nil {
			return Page{}, err
		}
	}
	return page, nil
}

func isDenied(err error) bool {
	var e *api.Error
	return errors.As(err, &e) && (e.Code == "forbidden" || e.Code == "not_found_or_inaccessible" || e.Code == "unauthenticated")
}
