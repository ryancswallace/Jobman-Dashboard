package reports

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-diagnose/deterministic"
)

// RunOne claims and finishes at most one persisted task. The database lease
// fences every write; a process crash leaves recoverable work and immutable
// unpublished objects, never a ready record with partial evidence.
func (s *Service) RunOne(parent context.Context) (err error) {
	claim, err := s.queue.ClaimReport(parent)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, TaskTimeout)
	defer cancel()
	defer func() {
		if err != nil {
			cleanup, done := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
			defer done()
			_ = s.queue.FailReport(cleanup, claim.Task.ID, claim.Task.LeaseToken, workerFailure(err))
		}
	}()
	if claim.Task.Subject.Validate() != nil {
		return ErrInvalid
	}
	revision, _ := strconv.ParseUint(claim.Task.Subject.Revision, 10, 64)
	request := subjectRequest(claim.Task.Subject)
	var actor monitoring.Actor
	var snapshot Snapshot
	var prepared PreparedCollection
	last := error(monitoring.ErrForbidden)
	for _, candidate := range claim.Requesters {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e := s.queue.VerifyReportActor(ctx, claim.Task.ID, candidate); e != nil {
			if !isDenied(e) {
				last = e
			}
			continue
		}
		v, p, e := s.capture(ctx, candidate, request, revision)
		if e != nil {
			if !isDenied(e) {
				last = e
			}
			continue
		}
		if s.subject(request, v, p) != claim.Task.Subject {
			return snapshotChanged()
		}
		actor, snapshot, prepared = candidate, v, p
		break
	}
	if actor.Account.ID == "" {
		return last
	}
	if err = s.queue.AnalyzeReport(ctx, claim.Task.ID, claim.Task.LeaseToken); err != nil {
		return err
	}
	// Recover a publication whose prior worker died before its SQL commit.
	// The original pair stays unchanged; source/policy/lease checks still apply.
	_, object, recovered := s.objects.Recover(ctx, claim.Task.Subject, claim.Task.ID)
	if recovered != nil {
		var reader PinnedLogs
		if request.Profile == "include_log_tail" {
			source, ok := s.sources[request.Scope.DeploymentID].(logs.ManifestSource)
			if !ok || s.logs == nil {
				return ErrInvalid
			}
			reader = PinnedLogs{Snapshot: snapshot, Actor: actor, Source: source, Logs: s.logs}
		}
		core, e := prepared.Collect(ctx, reader)
		if e != nil {
			return e
		}
		evidence, e := deterministic.Prepare(ctx, core)
		if e != nil {
			return ErrInvalid
		}
		report, e := s.engine.Diagnose(ctx, evidence)
		if e != nil {
			return ErrInvalid
		}
		pair := Pair{Subject: claim.Task.Subject, Evidence: evidence, Report: report}
		if pair.Validate() != nil {
			return ErrInvalid
		}
		if outdated, e := s.current(ctx, actor, claim.Task); e != nil {
			return e
		} else if outdated {
			return snapshotChanged()
		}
		object, e = s.objects.Put(ctx, claim.Task.ID, pair)
		if errors.Is(e, ErrConflict) {
			_, object, e = s.objects.Recover(ctx, claim.Task.Subject, claim.Task.ID)
		}
		if e != nil {
			return e
		}
	}
	if outdated, e := s.current(ctx, actor, claim.Task); e != nil {
		return e
	} else if outdated {
		return snapshotChanged()
	}
	return s.queue.CompleteReport(ctx, claim.Task.ID, claim.Task.LeaseToken, object, actor)
}

func workerFailure(err error) string {
	var known *api.Error
	if errors.As(err, &known) {
		switch known.Code {
		case "source_unavailable", "authorization_unavailable", "snapshot_changed":
			return known.Code
		case "forbidden", "not_found_or_inaccessible", "unauthenticated":
			return "forbidden"
		}
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrObject) || errors.Is(err, ErrRedactionUnavailable) {
		return "invalid_evidence"
	}
	return "analysis_failed"
}

// Run owns exactly two synchronous analysis loops; cancellation waits for both
// before the caller closes the database or object root. No unbounded goroutine
// is created for an individual analysis or inaccessible source.
func (s *Service) Run(ctx context.Context) {
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			for ctx.Err() == nil {
				err := s.RunOne(ctx)
				if err != nil {
					timer := time.NewTimer(time.Second)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}
		})
	}
	group.Wait()
}

func (s *Service) Prune(ctx context.Context) error {
	objects, err := s.queue.DeleteExpiredReports(ctx)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = s.objects.Remove(object.ID); err != nil {
			return err
		}
	}
	return s.objects.Sweep(ctx, s.queue.ReportObjectReferenced)
}
