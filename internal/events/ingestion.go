package events

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

var (
	ErrLease    = errors.New("event ingestion lease is unavailable")
	ErrPaused   = errors.New("event ingestion requires explicit recovery")
	ErrCapacity = errors.New("event ingestion retained-event capacity reached")
	ErrConflict = errors.New("event identity conflicts with retained facts")
)

const (
	FeedLease                      = 30 * time.Second
	IngestionTimeout               = 20 * time.Second
	MaximumRetainedEventsPerSource = 1000000
)

// Feed is private worker state. Neither its cursor nor service-authorized event
// data is an end-user authorization grant. Every user read/match needs a fresh
// represented-user check independently of this ingestion lease.
type Feed struct {
	DeploymentID string
	NamespaceIDs []string
	Checkpoint   Checkpoint
	Cursor       string
	LastPosition int64
	Status       string
	Generation   int64
	LeaseToken   string
}

// Journal commits original event identities and continuation in one transaction.
// Network work must finish before entering these persistence operations.
type Journal interface {
	ClaimFeed(context.Context, string, []string) (Feed, error)
	InitializeFeed(context.Context, Feed, Checkpoint) error
	AppendFeed(context.Context, Feed, Page) error
	PauseFeed(context.Context, Feed, string) error
	ReleaseFeed(context.Context, Feed, string) error
}

type Ingestor struct {
	sources []Source
	journal Journal
	slots   chan struct{}
}

func NewIngestor(sources []Source, journal Journal) (*Ingestor, error) {
	if len(sources) < 1 || len(sources) > 32 || journal == nil {
		return nil, errors.New("ingestion requires one to thirty-two sources and a journal")
	}
	seen := map[string]bool{}
	for _, source := range sources {
		if source == nil || source.SourceID() == "" || seen[source.SourceID()] || len(source.NamespaceIDs()) == 0 || len(source.NamespaceIDs()) > 320 {
			return nil, errors.New("invalid or repeated ingestion source")
		}
		seen[source.SourceID()] = true
	}
	return &Ingestor{sources: slices.Clone(sources), journal: journal, slots: make(chan struct{}, 4)}, nil
}

// Step does at most one bounded source page. A failed commit leaves the old
// cursor durable, so retry replays the same original event identities.
func (i *Ingestor) Step(ctx context.Context, source Source) (more bool, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, IngestionTimeout)
	defer cancel()
	select {
	case i.slots <- struct{}{}:
		defer func() { <-i.slots }()
	case <-ctx.Done():
		return false, ctx.Err()
	}
	feed, err := i.journal.ClaimFeed(ctx, source.SourceID(), source.NamespaceIDs())
	if err != nil {
		return false, err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer done()
		code := ""
		if resultErr != nil {
			code = "source_unavailable"
		}
		_ = i.journal.ReleaseFeed(cleanup, feed, code)
	}()
	if feed.Status == "initializing" {
		checkpoint, err := source.Checkpoint(ctx)
		if err != nil {
			return false, err
		}
		return false, i.journal.InitializeFeed(ctx, feed, checkpoint)
	}
	page, err := source.Read(ctx, feed.Cursor, 200)
	if err != nil {
		var recovery *RecoveryError
		if errors.As(err, &recovery) {
			if pauseErr := i.journal.PauseFeed(ctx, feed, string(recovery.Reason)); pauseErr != nil {
				return false, pauseErr
			}
		}
		return false, err
	}
	if err = i.journal.AppendFeed(ctx, feed, page); err != nil {
		reason := ""
		if errors.Is(err, ErrConflict) {
			reason = "event_conflict"
		}
		if errors.Is(err, ErrCapacity) {
			reason = "capacity"
		}
		if errors.Is(err, ErrRecoveryRequired) {
			reason = string(SourceChanged)
		}
		if reason != "" {
			if pauseErr := i.journal.PauseFeed(ctx, feed, reason); pauseErr != nil {
				return false, pauseErr
			}
		}
		return false, err
	}
	return page.HasMore, nil
}

// Run gives each configured source independent progress and backoff while four
// total worker slots bound network/DB concurrency. No user or APNs outage can
// hold a source-feed transaction open. Shutdown joins every fixed worker.
func (i *Ingestor) Run(ctx context.Context) {
	var workers sync.WaitGroup
	for _, source := range i.sources {
		workers.Add(1)
		go func() {
			defer workers.Done()
			failures := 0
			for ctx.Err() == nil {
				more, err := i.Step(ctx, source)
				delay := 5 * time.Second
				if err != nil {
					failures++
					delay = time.Duration(1<<min(failures, 6)) * time.Second
				} else {
					failures = 0
					if more {
						delay = time.Second
					}
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
