package notifications

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/push"
)

type Sender struct {
	repository DeliveryRepository
	authority  EventAuthority
	providers  map[DeviceTopic]PushProvider
	ids        []string
	slots      chan struct{}
}

func NewSender(repository DeliveryRepository, authority EventAuthority, ids []string, providers map[DeviceTopic]PushProvider) (*Sender, error) {
	if repository == nil || authority == nil || len(ids) < 1 || len(ids) > MaximumSources || len(providers) < 1 || len(providers) > 16 {
		return nil, ErrEvaluationInvalid
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !uuid(id) || seen[id] {
			return nil, ErrEvaluationInvalid
		}
		seen[id] = true
	}
	result := &Sender{repository: repository, authority: authority, ids: slices.Clone(ids), providers: map[DeviceTopic]PushProvider{}, slots: make(chan struct{}, 4)}
	for pair, p := range providers {
		if p == nil {
			return nil, ErrEvaluationInvalid
		}
		if _, err := NewDevicePolicy([]DeviceTopic{pair}); err != nil {
			return nil, err
		}
		result.providers[pair] = p
	}
	return result, nil
}

// Step owns one bounded attempt. The global four-slot limit also prevents an
// APNs provider's four slots from accumulating already-authorized queued work.
// SQL preparation is the local handoff fence; no transaction spans Apple I/O.
func (s *Sender) Step(ctx context.Context, id string) (progress bool, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return false, ctx.Err()
	}
	f, err := s.authority.CurrentSourceFence(ctx, id)
	if err != nil {
		return false, err
	}
	c, err := s.repository.ClaimNotificationDelivery(ctx, f)
	if errors.Is(err, ErrEvaluationAdvanced) {
		return true, nil
	}
	if errors.Is(err, ErrEvaluationEmpty) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	resolved := false
	defer func() {
		if !resolved {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer done()
			_ = s.repository.DeferNotificationDelivery(cleanup, c)
		}
	}()
	d, f, err := s.authority.VerifyEvent(ctx, c.Actor, c.Event, c.Fence)
	if err != nil {
		return true, err
	}
	h, err := s.repository.PrepareNotificationDelivery(ctx, c, f, d)
	if errors.Is(err, ErrEvaluationAdvanced) {
		resolved = true
		return true, nil
	}
	if err != nil {
		return true, err
	}
	// Prepared attempt count is part of the exact acknowledgement fence.
	c = h.Claim
	p := s.providers[DeviceTopic{h.Device.Topic, h.Device.Environment}]
	result := push.Result{Outcome: "provider_error", Reason: "provider_unavailable", RetryAfter: 15 * time.Minute}
	if p != nil {
		deadline := h.AuthorizationExpiresAt
		if c.ExpiresAt.Before(deadline) {
			deadline = c.ExpiresAt
		}
		if sourceExpiry := f.VerifiedAt.Add(MaximumSourceFenceAge); sourceExpiry.Before(deadline) {
			deadline = sourceExpiry
		}
		sendCtx, sendDone := context.WithDeadline(ctx, deadline)
		result, err = p.Send(sendCtx, push.Request{DeliveryID: c.ID, InboxID: c.InboxID, Token: h.Device.Token, Topic: h.Device.Topic, Environment: h.Device.Environment, ExpiresAt: c.ExpiresAt})
		sendDone()
		if err != nil {
			result = push.Result{Outcome: "retry", Reason: "transport_unavailable", RetryAfter: time.Minute, Ambiguous: true}
			if errors.Is(err, push.ErrConfiguration) {
				result = push.Result{Outcome: "provider_error", Reason: "provider_unavailable", RetryAfter: 15 * time.Minute}
			}
		}
	}
	// A server shutdown after provider acceptance must still attempt its bounded
	// durable acknowledgement. A crash may leave an unknown attempt and retry;
	// the stable delivery/collapse IDs do not claim exactly-once OS presentation.
	h.Device.Token = ""
	ack, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	err = s.repository.FinishNotificationDelivery(ack, h, result)
	resolved = err == nil
	return true, err
}

func (s *Sender) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for _, id := range s.ids {
		workers.Add(1)
		go func() {
			defer workers.Done()
			failures := 0
			for ctx.Err() == nil {
				progress, err := s.Step(ctx, id)
				delay := 5 * time.Second
				if progress {
					failures = 0
					delay = 100 * time.Millisecond
				} else if err != nil {
					failures++
					delay = time.Duration(1<<min(failures, 6)) * time.Second
				} else {
					failures = 0
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
