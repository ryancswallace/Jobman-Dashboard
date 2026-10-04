package notifications

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type ActivationClaim struct {
	RuleID, LeaseToken string
	Revision, Attempts int64
	Actor              monitoring.Actor
}
type ActivationRepository interface {
	ClaimNotificationActivation(context.Context) (ActivationClaim, error)
	ReleaseNotificationActivation(context.Context, ActivationClaim) error
}
type PendingRuleService interface {
	Revalidate(context.Context, monitoring.Actor, string, int64, bool) (RuleView, error)
}
type ActivationWorker struct {
	repository ActivationRepository
	service    PendingRuleService
}

func NewActivationWorker(repository ActivationRepository, service PendingRuleService) (*ActivationWorker, error) {
	if repository == nil || service == nil {
		return nil, ErrInvalid
	}
	return &ActivationWorker{repository, service}, nil
}
func (w *ActivationWorker) Step(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := w.repository.ClaimNotificationActivation(ctx)
	if errors.Is(err, ErrEvaluationAdvanced) {
		return true, nil
	}
	if errors.Is(err, ErrEvaluationEmpty) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer done()
		_ = w.repository.ReleaseNotificationActivation(cleanup, c)
	}()
	// Automatic work may advance a pending scope only. It must never clear a
	// recorded denial or reinterpret a removed scope as newly opted-in intent.
	_, err = w.service.Revalidate(ctx, c.Actor, c.RuleID, c.Revision, false)
	return true, err
}
func (w *ActivationWorker) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				progress, err := w.Step(ctx)
				delay := 5 * time.Second
				if progress {
					delay = 100 * time.Millisecond
				} else if err != nil {
					delay = 15 * time.Second
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	workers.Wait()
}
