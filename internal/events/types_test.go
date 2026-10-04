package events

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testCheckpoint() Checkpoint {
	return Checkpoint{DeploymentID: "10000000-0000-4000-8000-000000000001", ControlInstanceID: "20000000-0000-4000-8000-000000000001", RecoveryEpoch: "9007199254740993", NamespaceIDs: []string{"30000000-0000-4000-8000-000000000001"}, AsOf: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), HeadCursor: "opaque.head", OldestCursor: "opaque.oldest", RetentionSeconds: "2592000", BacklogCount: "0"}
}
func testEvent() Event {
	c := testCheckpoint()
	return Event{DeploymentID: c.DeploymentID, ControlInstanceID: c.ControlInstanceID, EventID: "40000000-0000-4000-8000-000000000001", Position: "9007199254740993", NamespaceID: c.NamespaceIDs[0], JobID: "50000000-0000-4000-8000-000000000001", OwnerPrincipalID: "60000000-0000-4000-8000-000000000001", OldPhase: "future_nonterminal", NewPhase: "terminal", Outcome: "future-outcome", JobRevision: "9223372036854775807", RecordedAt: c.AsOf, Imported: true, Reconciliation: true}
}

func TestEventValidationPreservesDecimalFactsAndRejectsMalformedRows(t *testing.T) {
	if err := testEvent().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Event){
		"overflow":           func(e *Event) { e.Position = "9223372036854775808" },
		"leading-zero":       func(e *Event) { e.JobRevision = "01" },
		"numeric-sign":       func(e *Event) { e.Position = "+1" },
		"zero":               func(e *Event) { e.JobRevision = "0" },
		"zero-id":            func(e *Event) { e.EventID = "00000000-0000-0000-0000-000000000000" },
		"run-without-number": func(e *Event) { e.RunID = e.JobID },
		"number-without-run": func(e *Event) { e.RunNumber = "1" },
		"terminal-repeat":    func(e *Event) { e.OldPhase = "terminal" },
		"nonterminal":        func(e *Event) { e.NewPhase = "running" },
		"long-outcome":       func(e *Event) { e.Outcome = strings.Repeat("a", 65) },
		"freeform-outcome":   func(e *Event) { e.Outcome = "private detail\n" },
		"missing-time":       func(e *Event) { e.RecordedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			e := testEvent()
			mutate(&e)
			if e.Validate() == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
	e := testEvent()
	e.RunID, e.RunNumber = e.JobID, "9223372036854775807"
	if e.Validate() != nil {
		t.Fatal("actual large run number rejected")
	}
}

func TestCheckpointAndPageValidationBoundsIdentityAndOrdering(t *testing.T) {
	c := testCheckpoint()
	if c.Validate() != nil {
		t.Fatal("valid checkpoint rejected")
	}
	for _, value := range []string{"", "0", "01", "-1", "9223372036854775808"} {
		bad := c
		bad.RecoveryEpoch = value
		if bad.Validate() == nil {
			t.Fatal("invalid recovery epoch accepted")
		}
	}
	bad := c
	bad.BacklogCount = "1"
	if bad.Validate() == nil {
		t.Fatal("backlog omitted oldest time")
	}
	bad.OldestUnpublishedRecordedAt = &bad.AsOf
	if bad.Validate() != nil {
		t.Fatal("valid backlog rejected")
	}
	bad = c
	bad.NamespaceIDs = []string{c.NamespaceIDs[0], c.NamespaceIDs[0]}
	if bad.Validate() == nil {
		t.Fatal("repeated configured scope accepted")
	}
	page := Page{Checkpoint: c, Items: []Event{testEvent()}, NextCursor: c.HeadCursor}
	if page.Validate() != nil {
		t.Fatal("valid page rejected")
	}
	for name, mutate := range map[string]func(*Page){
		"duplicate-event": func(p *Page) { next := p.Items[0]; next.Position = "9007199254740994"; p.Items = append(p.Items, next) },
		"reordered": func(p *Page) {
			next := p.Items[0]
			next.EventID = next.JobID
			next.Position = "1"
			p.Items = append(p.Items, next)
		},
		"other-source":    func(p *Page) { p.Items[0].DeploymentID = p.Items[0].JobID },
		"other-instance":  func(p *Page) { p.Items[0].ControlInstanceID = p.Items[0].JobID },
		"other-namespace": func(p *Page) { p.Items[0].NamespaceID = p.Items[0].JobID },
		"missing-items":   func(p *Page) { p.Items = nil },
		"unbounded":       func(p *Page) { p.Items = make([]Event, 201) },
		"incorrect-head":  func(p *Page) { p.NextCursor = "elsewhere" },
		"empty-more":      func(p *Page) { p.Items = []Event{}; p.HasMore = true; p.NextCursor = "more" },
	} {
		t.Run(name, func(t *testing.T) {
			p := Page{Checkpoint: c, Items: []Event{testEvent()}, NextCursor: c.HeadCursor}
			mutate(&p)
			if p.Validate() == nil {
				t.Fatal("invalid page accepted")
			}
		})
	}
	if !errors.Is(&RecoveryError{Reason: ScopeChanged}, ErrRecoveryRequired) {
		t.Fatal("recovery error not classifiable")
	}
	for _, cursor := range []string{"", "has a space", "newline\n", strings.Repeat("a", 1025)} {
		if ValidCursor(cursor) {
			t.Fatal("unbounded cursor accepted")
		}
	}
}
