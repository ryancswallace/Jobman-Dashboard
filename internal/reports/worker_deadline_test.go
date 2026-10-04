package reports

import (
	"context"
	"errors"
	"testing"
	"time"
)

type deadlineClaimQueue struct {
	Queue
	claim func(context.Context) (Claim, error)
}

func (q deadlineClaimQueue) ClaimReport(ctx context.Context) (Claim, error) { return q.claim(ctx) }

func TestReportClaimHasIndependentDeadlineAndCanRetry(t *testing.T) {
	var calls int
	var previous context.Context
	empty := errors.New("synthetic queue empty")
	s := &Service{writer: &ObjectStore{}, queue: deadlineClaimQueue{claim: func(ctx context.Context) (Claim, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second || time.Until(deadline) < 4*time.Second {
			t.Fatal("claim lacks its own five-second budget")
		}
		if calls == 1 {
			previous = ctx
			<-ctx.Done()
			return Claim{}, ctx.Err()
		}
		if previous.Err() == nil || ctx.Err() != nil {
			t.Fatal("claim retry reused a canceled context")
		}
		return Claim{}, empty
	}}}
	start := time.Now()
	if !errors.Is(s.RunOne(t.Context()), context.DeadlineExceeded) || time.Since(start) > 7*time.Second {
		t.Fatal("blocked claim was not canceled")
	}
	if !errors.Is(s.RunOne(t.Context()), empty) || calls != 2 {
		t.Fatal("a claim timeout permanently prevented retry")
	}
	// All other Queue methods are deliberately absent. No lease was returned,
	// so a failed claim must not acknowledge/fail a task it never owned.
}

func TestReportClaimPreservesCancellationAndEarlierDeadline(t *testing.T) {
	ctx, done := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer done()
	expected, _ := ctx.Deadline()
	s := &Service{writer: &ObjectStore{}, queue: deadlineClaimQueue{claim: func(c context.Context) (Claim, error) {
		if deadline, ok := c.Deadline(); !ok || !deadline.Equal(expected) {
			t.Fatal("claim extended parent deadline")
		}
		<-c.Done()
		return Claim{}, c.Err()
	}}}
	if !errors.Is(s.RunOne(ctx), context.DeadlineExceeded) {
		t.Fatal("parent cancellation was ignored")
	}
}
