package notifications

import (
	"context"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/push"
)

const DeliveryLease = 60 * time.Second
const MaximumDeliveryAttempts = 128

// DeliveryClaim contains no token. A fresh authorized Prepare call obtains the
// exact current token only after checking the original binding, rule and inbox.
type DeliveryClaim struct {
	ID, InboxID, LeaseToken  string
	Revision, HoldGeneration int64
	Fence                    SourceFence
	Actor                    monitoring.Actor
	Event                    events.Event
	Device                   DeviceCandidate
	ExpiresAt                time.Time
	Claims, Attempts         int64
}
type DeliveryHandoff struct {
	Claim                  DeliveryClaim
	Device                 DeviceHandoff
	AuthorizationExpiresAt time.Time
}
type EventAuthority interface {
	CurrentSourceFence(context.Context, string) (SourceFence, error)
	VerifyEvent(context.Context, monitoring.Actor, events.Event, SourceFence) (EvaluationDecision, SourceFence, error)
}
type PushProvider interface {
	Send(context.Context, push.Request) (push.Result, error)
}
type DeliveryRepository interface {
	ClaimNotificationDelivery(context.Context, SourceFence) (DeliveryClaim, error)
	PrepareNotificationDelivery(context.Context, DeliveryClaim, SourceFence, EvaluationDecision) (DeliveryHandoff, error)
	FinishNotificationDelivery(context.Context, DeliveryHandoff, push.Result) error
	DeferNotificationDelivery(context.Context, DeliveryClaim) error
}

func DeliveryRetryDelay(attempt int64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		attempt = 5
	}
	return min(time.Minute*time.Duration(1<<uint(attempt-1)), 15*time.Minute)
}
