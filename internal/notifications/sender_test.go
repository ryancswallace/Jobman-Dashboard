package notifications

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/push"
)

type senderFixture struct {
	claim                                               DeliveryClaim
	fence                                               SourceFence
	decision                                            EvaluationDecision
	handoff                                             DeliveryHandoff
	result                                              push.Result
	claimErr, verifyErr, prepareErr, sendErr, finishErr error
	steps                                               []string
	deferred                                            []DeliveryClaim
	sendHook                                            func(context.Context, push.Request)
	finishHook                                          func(context.Context, DeliveryHandoff, push.Result)
}

func (f *senderFixture) CurrentSourceFence(context.Context, string) (SourceFence, error) {
	f.steps = append(f.steps, "fence")
	return f.fence, nil
}
func (f *senderFixture) ClaimNotificationDelivery(context.Context, SourceFence) (DeliveryClaim, error) {
	f.steps = append(f.steps, "claim")
	return f.claim, f.claimErr
}
func (f *senderFixture) VerifyEvent(context.Context, monitoring.Actor, events.Event, SourceFence) (EvaluationDecision, SourceFence, error) {
	f.steps = append(f.steps, "verify")
	return f.decision, f.fence, f.verifyErr
}
func (f *senderFixture) PrepareNotificationDelivery(context.Context, DeliveryClaim, SourceFence, EvaluationDecision) (DeliveryHandoff, error) {
	f.steps = append(f.steps, "prepare")
	return f.handoff, f.prepareErr
}
func (f *senderFixture) Send(ctx context.Context, r push.Request) (push.Result, error) {
	f.steps = append(f.steps, "send")
	if f.sendHook != nil {
		f.sendHook(ctx, r)
	}
	return f.result, f.sendErr
}
func (f *senderFixture) FinishNotificationDelivery(ctx context.Context, h DeliveryHandoff, r push.Result) error {
	f.steps = append(f.steps, "finish")
	if f.finishHook != nil {
		f.finishHook(ctx, h, r)
	}
	return f.finishErr
}
func (f *senderFixture) DeferNotificationDelivery(ctx context.Context, c DeliveryClaim) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	f.steps = append(f.steps, "defer")
	f.deferred = append(f.deferred, c)
	return nil
}
func newSenderFixture(t *testing.T) (*Sender, *senderFixture) {
	t.Helper()
	now := time.Now().UTC()
	cp := fixtureCheckpoint()
	cp.AsOf = now
	f := &senderFixture{fence: SourceFence{Checkpoint: cp, FeedGeneration: 1, ConfigurationRevision: 1, VerifiedAt: now}}
	f.claim = DeliveryClaim{ID: fixtureID(50, 1), InboxID: fixtureID(51, 1), LeaseToken: fixtureID(52, 1), Revision: 1, HoldGeneration: 1, Fence: f.fence, Actor: monitoring.Actor{}, Event: fixtureEvent(), Device: DeviceCandidate{InstallationID: fixtureID(53, 1), BindingID: fixtureID(54, 1), AccountID: fixtureID(55, 1), TokenVersion: 1}, ExpiresAt: now.Add(time.Hour), Claims: 1}
	f.handoff = DeliveryHandoff{Claim: f.claim, Device: DeviceHandoff{DeviceCandidate: f.claim.Device, Topic: "test.jobman.dashboard", Environment: "sandbox", Token: "abcd"}, AuthorizationExpiresAt: now.Add(20 * time.Second)}
	f.handoff.Claim.Attempts = 1
	f.decision = EvaluationDecision{Outcome: EvaluationAuthorized}
	f.result = push.Result{Outcome: "accepted", ProviderID: f.claim.ID}
	s, err := NewSender(f, f, []string{cp.DeploymentID}, map[DeviceTopic]PushProvider{{Topic: "test.jobman.dashboard", Environment: "sandbox"}: f})
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestSenderFreshFenceBeforeProviderAndBoundedAcknowledgementAfterCancellation(t *testing.T) {
	s, f := newSenderFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.sendHook = func(ctx context.Context, r push.Request) {
		deadline, ok := ctx.Deadline()
		if !ok || deadline.After(f.handoff.AuthorizationExpiresAt) {
			t.Fatal("handoff outlives actual proof")
		}
		if r.DeliveryID != f.claim.ID || r.InboxID != f.claim.InboxID || r.Token != "abcd" {
			t.Fatal("provider identity changed")
		}
		cancel()
	}
	f.finishHook = func(ctx context.Context, h DeliveryHandoff, r push.Result) {
		if ctx.Err() != nil || h.Device.Token != "" || h.Claim.Attempts != 1 || r.Outcome != "accepted" {
			t.Fatal("shutdown lost ack or retained plaintext")
		}
		if d, ok := ctx.Deadline(); !ok || d.After(time.Now().Add(5*time.Second)) {
			t.Fatal("unbounded ack")
		}
	}
	progress, err := s.Step(ctx, f.fence.Checkpoint.DeploymentID)
	if err != nil || !progress || len(f.deferred) != 0 || !reflect.DeepEqual(f.steps, []string{"fence", "claim", "verify", "prepare", "send", "finish"}) {
		t.Fatal(progress, err, f.steps)
	}
}

func TestSenderNeverHandsOffFailedAuthorityOrLocalFence(t *testing.T) {
	for _, mode := range []string{"authority", "revoked", "hold", "lease", "missing-token-key"} {
		t.Run(mode, func(t *testing.T) {
			s, f := newSenderFixture(t)
			switch mode {
			case "authority":
				f.verifyErr = monitoring.ErrAuthority
			case "revoked":
				f.prepareErr = ErrEvaluationAdvanced
			case "hold":
				f.prepareErr = ErrEvaluationHeld
			case "lease":
				f.prepareErr = ErrEvaluationLease
			case "missing-token-key":
				f.prepareErr = ErrDeviceUnavailable
			}
			_, _ = s.Step(context.Background(), f.fence.Checkpoint.DeploymentID)
			for _, step := range f.steps {
				if step == "send" || step == "finish" {
					t.Fatal("ineligible handoff", f.steps)
				}
			}
			if mode == "revoked" && len(f.deferred) != 0 || mode != "revoked" && len(f.deferred) != 1 {
				t.Fatal("incorrect lease disposition", f.steps)
			}
		})
	}
}

func TestSenderAmbiguousProviderFailureAndLostAcknowledgementRemainRetryable(t *testing.T) {
	for _, mode := range []string{"network-error", "configuration-error", "ack-error"} {
		t.Run(mode, func(t *testing.T) {
			s, f := newSenderFixture(t)
			if mode == "configuration-error" {
				f.sendErr = push.ErrConfiguration
			} else if mode == "network-error" {
				f.sendErr = errors.New("private transport error must not persist")
			} else {
				f.finishErr = ErrEvaluationLease
			}
			f.finishHook = func(_ context.Context, _ DeliveryHandoff, r push.Result) {
				if mode == "network-error" && (r.Outcome != "retry" || !r.Ambiguous || r.Reason != "transport_unavailable") {
					t.Fatal("ambiguity lost", r)
				}
				if mode == "configuration-error" && (r.Outcome != "provider_error" || r.Reason != "provider_unavailable") {
					t.Fatal("configuration not surfaced", r)
				}
			}
			_, _ = s.Step(context.Background(), f.fence.Checkpoint.DeploymentID)
			if mode == "ack-error" {
				if len(f.deferred) != 1 || f.deferred[0].Attempts != 1 {
					t.Fatal("failed ack discarded updated attempt fence")
				}
			} else if len(f.deferred) != 0 {
				t.Fatal("durable retry was deferred twice")
			}
		})
	}
}

func TestSenderBoundsRetryAndStopsCancelledWorkers(t *testing.T) {
	for _, n := range []int64{-1, 0, 1, 5, 128, 1 << 62} {
		d := DeliveryRetryDelay(n)
		if d < time.Minute || d > 15*time.Minute {
			t.Fatal(n, d)
		}
	}
	s, _ := newSenderFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sender did not join")
	}
}
