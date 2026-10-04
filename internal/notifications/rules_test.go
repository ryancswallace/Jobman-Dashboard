package notifications

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/events"
)

func fixtureID(kind, n int) string { return fmt.Sprintf("%08d-0000-4000-8000-%012d", kind, n) }
func fixtureTime() time.Time       { return time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC) }
func fixtureRef(source, namespace int) NamespaceRef {
	return NamespaceRef{fixtureID(2, source), fixtureID(4, namespace)}
}
func fixtureInput() RuleInput {
	return RuleInput{Name: "Unsuccessful jobs", Enabled: true, Scope: ScopeNamespaceJobs,
		Namespaces: []NamespaceRef{fixtureRef(1, 1)}, Jobs: []JobRef{}, OutcomeMode: OutcomeSelected,
		Outcomes: []string{"aborted", "failure", "lost", "timed_out"}}
}
func fixtureCheckpoint() events.Checkpoint {
	return events.Checkpoint{DeploymentID: fixtureID(2, 1), ControlInstanceID: fixtureID(3, 1), RecoveryEpoch: "9007199254740993", NamespaceIDs: []string{fixtureID(4, 1)}, AsOf: fixtureTime(), HeadCursor: "opaque-source-head", OldestCursor: "opaque-source-oldest", RetentionSeconds: "2592000", BacklogCount: "0"}
}
func fixtureFeed() events.Feed {
	c := fixtureCheckpoint()
	return events.Feed{DeploymentID: c.DeploymentID, NamespaceIDs: append([]string(nil), c.NamespaceIDs...), Checkpoint: c, Cursor: c.HeadCursor, Status: "active", Generation: 1}
}
func fixtureRule(t *testing.T) Rule {
	t.Helper()
	activation, err := NewActivation(fixtureID(8, 1), fixtureRef(1, 1), fixtureFeed(), fixtureCheckpoint(), fixtureID(7, 1))
	if err != nil {
		t.Fatal(err)
	}
	rule := Rule{ID: fixtureID(1, 1), AccountID: fixtureID(9, 1), Revision: 1, RuleInput: fixtureInput(), Activation: []Activation{activation}, CreatedAt: fixtureTime(), UpdatedAt: fixtureTime()}
	if err := rule.Validate(); err != nil {
		t.Fatal(err)
	}
	return rule
}
func fixtureEvent() events.Event {
	return events.Event{DeploymentID: fixtureID(2, 1), ControlInstanceID: fixtureID(3, 1), EventID: fixtureID(5, 1), Position: "9007199254740999", NamespaceID: fixtureID(4, 1), JobID: fixtureID(6, 1), OwnerPrincipalID: fixtureID(7, 1), OldPhase: "running", NewPhase: "terminal", Outcome: "failure", JobRevision: "9007199254740994", RecordedAt: fixtureTime().Add(time.Second)}
}
func cloneRule(rule Rule) Rule {
	b, _ := json.Marshal(rule)
	var copy Rule
	_ = json.Unmarshal(b, &copy)
	return copy
}
func requireMatch(t *testing.T, rule Rule, event events.Event, expected bool) *Match {
	t.Helper()
	match, err := MatchVersion(rule, event)
	if err != nil || (match != nil) != expected {
		t.Fatalf("match=%v expected=%v error=%v", match != nil, expected, err)
	}
	return match
}

func TestRuleCanonicalSelectionsAndEditorModeChanges(t *testing.T) {
	in := fixtureInput()
	in.Name = "  Namespaced work  "
	in.Scope = ScopeWatchedJobs
	in.Namespaces = []NamespaceRef{fixtureRef(2, 1), fixtureRef(1, 2), fixtureRef(1, 1), fixtureRef(2, 1)}
	job := JobRef{fixtureID(2, 1), fixtureID(4, 1), fixtureID(6, 1)}
	in.Jobs = []JobRef{{fixtureID(2, 2), fixtureID(4, 1), fixtureID(6, 1)}, job, job}
	in.Outcomes = []string{"timed_out", "failure", "failure"}
	canonical, err := in.Canonical()
	if err != nil || canonical.Name != "Namespaced work" || len(canonical.Namespaces) != 3 || len(canonical.Jobs) != 2 || !equalJSON(canonical.Outcomes, []string{"failure", "timed_out"}) {
		t.Fatalf("canonical: %+v %v", canonical, err)
	}
	if canonical.Jobs[0] != job || canonical.Namespaces[0] != fixtureRef(1, 1) {
		t.Fatal("source-qualified sort lost identity")
	}
	in.Namespaces[0] = fixtureRef(3, 9)
	in.Jobs[0].JobID = fixtureID(6, 99)
	in.Outcomes[0] = "success"
	if canonical.Namespaces[2] != fixtureRef(2, 1) || canonical.Jobs[1].JobID != fixtureID(6, 1) || canonical.Outcomes[1] != "timed_out" {
		t.Fatal("canonical state aliases caller slices")
	}
	canonical.Scope, canonical.OutcomeMode = ScopeMyJobs, OutcomeAllTerminal
	cleared, err := canonical.Canonical()
	if err != nil || len(cleared.Jobs) != 0 || len(cleared.Outcomes) != 0 || cleared.Jobs == nil || cleared.Outcomes == nil {
		t.Fatal("inactive editor values were not cleared", err)
	}
	if changed, err := MonitoringChanged(cleared, cleared); err != nil || changed {
		t.Fatal("equivalent intent changed monitoring", err)
	}
	cleared.Name = "Renamed"
	if changed, err := MonitoringChanged(canonical, cleared); err != nil || changed {
		t.Fatal("name-only edit changed monitoring", err)
	}
}

func TestRuleInputRejectsMalformedOrUnboundedActiveSelections(t *testing.T) {
	cases := map[string]func(*RuleInput){
		"empty name":        func(in *RuleInput) { in.Name = " \t " },
		"control":           func(in *RuleInput) { in.Name = "work\nother" },
		"utf8":              func(in *RuleInput) { in.Name = string([]byte{0xff}) },
		"name bytes":        func(in *RuleInput) { in.Name = strings.Repeat("é", 61) },
		"scope":             func(in *RuleInput) { in.Scope = "all_jobs" },
		"no namespaces":     func(in *RuleInput) { in.Namespaces = nil },
		"nil uuid":          func(in *RuleInput) { in.Namespaces[0].NamespaceID = "00000000-0000-0000-0000-000000000000" },
		"noncanonical uuid": func(in *RuleInput) { in.Namespaces[0].NamespaceID = "ABCDEFAB-0000-4000-8000-000000000001" },
		"namespace limit":   func(in *RuleInput) { in.Namespaces = make([]NamespaceRef, MaximumNamespaces+1) },
		"source limit": func(in *RuleInput) {
			for i := 2; i <= MaximumSources+1; i++ {
				in.Namespaces = append(in.Namespaces, fixtureRef(i, 1))
			}
		},
		"watched empty": func(in *RuleInput) { in.Scope = ScopeWatchedJobs },
		"watched foreign namespace": func(in *RuleInput) {
			in.Scope = ScopeWatchedJobs
			in.Jobs = []JobRef{{fixtureID(2, 2), fixtureID(4, 1), fixtureID(6, 1)}}
		},
		"watched malformed job": func(in *RuleInput) {
			in.Scope = ScopeWatchedJobs
			in.Jobs = []JobRef{{fixtureID(2, 1), fixtureID(4, 1), "job-name"}}
		},
		"watched limit":      func(in *RuleInput) { in.Jobs = make([]JobRef, MaximumWatchedJobs+1) },
		"empty outcomes":     func(in *RuleInput) { in.Outcomes = nil },
		"outcome mode":       func(in *RuleInput) { in.OutcomeMode = "unsuccessful" },
		"unknown individual": func(in *RuleInput) { in.Outcomes = []string{"future_terminal"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			in := fixtureInput()
			change(&in)
			if _, err := in.Canonical(); err == nil {
				t.Fatal("malformed input accepted")
			}
		})
	}
	in := fixtureInput()
	in.Outcomes = []string{"unknown"}
	if _, err := in.Canonical(); !errors.Is(err, ErrUnsupportedOutcome) {
		t.Fatal("individual unknown outcome lacks compatibility error", err)
	}
}

func TestActivationRequiresDurablePinnedFeedAndSourceClock(t *testing.T) {
	feed, checkpoint := fixtureFeed(), fixtureCheckpoint()
	checkpoint.AsOf = checkpoint.AsOf.Add(time.Minute).In(time.FixedZone("source-zone", -5*3600))
	checkpoint.HeadCursor = "new-opaque-head"
	activation, err := NewActivation(fixtureID(8, 1), fixtureRef(1, 1), feed, checkpoint, fixtureID(7, 1))
	if err != nil || !activation.Boundary.NotBefore.Equal(checkpoint.AsOf) || activation.Boundary.NotBefore.Location() != time.UTC || activation.Boundary.HeadCursor != checkpoint.HeadCursor || activation.Boundary.RecoveryEpoch != "9007199254740993" {
		t.Fatal("activation did not preserve source boundary", err)
	}
	for _, field := range []string{"initializing", "paused", "generation", "cursor", "instance", "epoch", "deployment", "namespace", "configured namespace"} {
		t.Run(field, func(t *testing.T) {
			feed, checkpoint := fixtureFeed(), fixtureCheckpoint()
			ref := fixtureRef(1, 1)
			switch field {
			case "initializing", "paused":
				feed.Status = field
			case "generation":
				feed.Generation = 0
			case "cursor":
				feed.Cursor = ""
			case "instance":
				checkpoint.ControlInstanceID = fixtureID(3, 2)
			case "epoch":
				checkpoint.RecoveryEpoch = "9007199254740994"
			case "deployment":
				ref.DeploymentID = fixtureID(2, 2)
			case "namespace":
				ref.NamespaceID = fixtureID(4, 2)
			case "configured namespace":
				feed.NamespaceIDs = []string{fixtureID(4, 2)}
			}
			if _, err := NewActivation(fixtureID(8, 1), ref, feed, checkpoint, fixtureID(7, 1)); !errors.Is(err, ErrInactiveFeed) {
				t.Fatal("uninitialized or mismatched feed activated", err)
			}
		})
	}
}

func TestMatchingUsesRecordedBoundaryNotObservedCompletionOrPublicationPosition(t *testing.T) {
	rule := fixtureRule(t)
	event := fixtureEvent()
	observed := fixtureTime().Add(-24 * time.Hour)
	event.ObservedCompletedAt = &observed
	match := requireMatch(t, rule, event, true)
	if match.EventID != event.EventID || match.ActivationID != rule.Activation[0].ID || match.AccountID != rule.AccountID {
		t.Fatal("match lost source event or personal interval identity")
	}
	for _, recorded := range []time.Time{fixtureTime().Add(-time.Hour), fixtureTime()} {
		event.RecordedAt = recorded
		event.Position = "9223372036854775807"
		requireMatch(t, rule, event, false)
	}
	event = fixtureEvent()
	event.RecordedAt = fixtureTime().Add(time.Nanosecond)
	event.Position = "1"
	requireMatch(t, rule, event, true)
	for _, flag := range []string{"imported", "reconciliation"} {
		copy := event
		copy.Imported = flag == "imported"
		copy.Reconciliation = flag == "reconciliation"
		requireMatch(t, rule, copy, false)
	}
}

func TestApprovedRecoveryCanRetainOriginalIntervalWhileAbandoningGapStartsFresh(t *testing.T) {
	old := fixtureRule(t)
	event := fixtureEvent()
	match := requireMatch(t, old, event, true)
	feed, checkpoint := fixtureFeed(), fixtureCheckpoint()
	checkpoint.RecoveryEpoch = "9007199254740994"
	checkpoint.AsOf = fixtureTime().Add(time.Hour)
	checkpoint.HeadCursor = "new-epoch-opaque-head"
	feed.Checkpoint, feed.Cursor = checkpoint, checkpoint.HeadCursor
	// This is a new activation only if the operator chooses to abandon the old
	// gap. A completed reconciliation can instead preserve the old interval.
	fresh, err := NewActivation(fixtureID(8, 2), fixtureRef(1, 1), feed, checkpoint, fixtureID(7, 1))
	if err != nil {
		t.Fatal(err)
	}
	retained := cloneRule(old)
	retained.Revision++
	if err := ValidateTransition(old, retained); err != nil {
		t.Fatal(err)
	}
	if eligible, err := StillApplicable(*match, retained); err != nil || !eligible {
		t.Fatal("approved retained interval lost authority to propose a candidate", err)
	}
	event.Position = "1"
	requireMatch(t, retained, event, true)
	restarted := cloneRule(retained)
	restarted.Revision++
	restarted.Activation[0] = fresh
	if err := ValidateTransition(retained, restarted); err != nil {
		t.Fatal(err)
	}
	requireMatch(t, restarted, event, false)
	if eligible, err := StillApplicable(*match, restarted); err != nil || eligible {
		t.Fatal("new interval resurrected old candidate", err)
	}
}

func TestPendingActivationNeverMatchesAndCannotMutateIntoActiveBehindItsID(t *testing.T) {
	active := fixtureRule(t)
	pending := cloneRule(active)
	pending.Activation[0].Status, pending.Activation[0].Boundary = ActivationPending, nil
	requireMatch(t, pending, fixtureEvent(), false)
	active.Revision++
	if err := ValidateTransition(pending, active); !errors.Is(err, ErrTransition) {
		t.Fatal("pending interval changed behind existing ID", err)
	}
	active.Activation[0].ID = fixtureID(8, 2)
	if err := ValidateTransition(pending, active); err != nil {
		t.Fatal(err)
	}
	requireMatch(t, active, fixtureEvent(), true)
	for _, field := range []string{"empty boundary", "bad principal", "bad epoch", "oversized cursor", "empty time", "inactive boundary"} {
		t.Run(field, func(t *testing.T) {
			bad := cloneRule(active).Activation[0]
			switch field {
			case "empty boundary":
				bad.Boundary = nil
			case "bad principal":
				bad.Boundary.PrincipalID = "display-name"
			case "bad epoch":
				bad.Boundary.RecoveryEpoch = "01"
			case "oversized cursor":
				bad.Boundary.HeadCursor = strings.Repeat("a", 1025)
			case "empty time":
				bad.Boundary.NotBefore = time.Time{}
			case "inactive boundary":
				bad.Status = ActivationInaccessible
			}
			if err := bad.Validate(); err == nil {
				t.Fatal("invalid activation accepted")
			}
		})
	}
}

func TestMyJobsUsesEachControlsVerifiedPrincipalRatherThanDashboardAccount(t *testing.T) {
	rule := fixtureRule(t)
	rule.Scope = ScopeMyJobs
	rule.Namespaces = append(rule.Namespaces, fixtureRef(2, 1))
	other := cloneRule(rule).Activation[0]
	other.ID, other.DeploymentID = fixtureID(8, 2), fixtureID(2, 2)
	other.Boundary.ControlInstanceID, other.Boundary.PrincipalID = fixtureID(3, 2), fixtureID(7, 2)
	rule.Activation = append(rule.Activation, other)
	event := fixtureEvent()
	requireMatch(t, rule, event, true)
	event.DeploymentID, event.ControlInstanceID = fixtureID(2, 2), fixtureID(3, 2)
	requireMatch(t, rule, event, false)
	event.OwnerPrincipalID = rule.AccountID
	requireMatch(t, rule, event, false)
	event.OwnerPrincipalID = fixtureID(7, 2)
	requireMatch(t, rule, event, true)
}

func TestMatchingScopesOwnersOutcomesAndSourceIsolation(t *testing.T) {
	rule := fixtureRule(t)
	event := fixtureEvent()
	// Namespace monitoring includes another user's jobs.
	event.OwnerPrincipalID = fixtureID(7, 2)
	requireMatch(t, rule, event, true)
	rule.Scope = ScopeMyJobs
	requireMatch(t, rule, event, false)
	event.OwnerPrincipalID = rule.Activation[0].Boundary.PrincipalID
	event.JobID = fixtureID(6, 99) // Future jobs require no enumerated snapshot.
	requireMatch(t, rule, event, true)
	rule.Scope = ScopeWatchedJobs
	rule.Jobs = []JobRef{{event.DeploymentID, event.NamespaceID, event.JobID}}
	event.OwnerPrincipalID = fixtureID(7, 2)
	requireMatch(t, rule, event, true)
	event.JobID = fixtureID(6, 100)
	requireMatch(t, rule, event, false)
	rule.Scope, rule.Jobs = ScopeNamespaceJobs, []JobRef{}
	for _, field := range []string{"deployment", "namespace", "instance"} {
		copy := event
		switch field {
		case "deployment":
			copy.DeploymentID = fixtureID(2, 2)
		case "namespace":
			copy.NamespaceID = fixtureID(4, 2)
		case "instance":
			copy.ControlInstanceID = fixtureID(3, 2)
		}
		requireMatch(t, rule, copy, false)
	}
	for _, outcome := range []string{"failure", "timed_out", "aborted", "lost", "cancelled", "success", "future_terminal", "unknown"} {
		event.Outcome = outcome
		requireMatch(t, rule, event, outcome == "failure" || outcome == "timed_out" || outcome == "aborted" || outcome == "lost")
		all := cloneRule(rule)
		all.OutcomeMode, all.Outcomes = OutcomeAllTerminal, []string{}
		requireMatch(t, all, event, true)
	}
	for _, outcome := range []string{"success", "cancelled"} {
		rule.Outcomes, event.Outcome = []string{outcome}, outcome
		requireMatch(t, rule, event, true)
	}
}

func TestNamespaceLossSuppressesOnlyItsIntervalAndRegrantCannotResurrectIt(t *testing.T) {
	old := fixtureRule(t)
	old.Namespaces = append(old.Namespaces, fixtureRef(1, 2))
	other := old.Activation[0]
	other.ID, other.NamespaceID = fixtureID(8, 2), fixtureID(4, 2)
	old.Activation = append(old.Activation, other)
	event := fixtureEvent()
	first := requireMatch(t, old, event, true)
	event.NamespaceID = fixtureID(4, 2)
	second := requireMatch(t, old, event, true)
	current := cloneRule(old)
	current.Revision++
	current.Activation[0].ID, current.Activation[0].Status, current.Activation[0].Boundary = fixtureID(8, 3), ActivationInaccessible, nil
	if err := ValidateTransition(old, current); err != nil {
		t.Fatal("isolated namespace revocation rejected", err)
	}
	if eligible, err := StillApplicable(*first, current); err != nil || eligible {
		t.Fatal("revoked candidate survives", err)
	}
	if eligible, err := StillApplicable(*second, current); err != nil || !eligible {
		t.Fatal("sibling namespace was suppressed", err)
	}
	reactivated := cloneRule(current)
	reactivated.Revision++
	reactivated.Activation[0] = cloneRule(old).Activation[0]
	reactivated.Activation[0].ID = fixtureID(8, 4)
	reactivated.Activation[0].Boundary.NotBefore = fixtureTime().Add(time.Hour)
	if err := ValidateTransition(current, reactivated); err != nil {
		t.Fatal(err)
	}
	if eligible, err := StillApplicable(*first, reactivated); err != nil || eligible {
		t.Fatal("regrant resurrected revoked candidate", err)
	}
	requireMatch(t, reactivated, fixtureEvent(), false)
}

func TestRuleTransitionsFenceEditsDisableDeleteAndReusedIntervals(t *testing.T) {
	old := fixtureRule(t)
	match := requireMatch(t, old, fixtureEvent(), true)
	renamed := cloneRule(old)
	renamed.Revision++
	renamed.Name = "New label"
	if err := ValidateTransition(old, renamed); err != nil {
		t.Fatal(err)
	}
	if eligible, err := StillApplicable(*match, renamed); err != nil || !eligible {
		t.Fatal("rename suppressed same monitoring interval", err)
	}
	for _, field := range []string{"enabled", "outcomes", "scope", "boundary", "status", "namespace", "account", "revision", "created", "clock", "deleted"} {
		t.Run(field, func(t *testing.T) {
			next := cloneRule(renamed)
			switch field {
			case "enabled":
				next.Enabled = false
				next.Activation[0].Status, next.Activation[0].Boundary = ActivationDisabled, nil
			case "outcomes":
				next.Outcomes = []string{"success"}
			case "scope":
				next.Scope = ScopeMyJobs
			case "boundary":
				next.Activation[0].Boundary.PrincipalID = fixtureID(7, 2)
			case "status":
				next.Activation[0].Status, next.Activation[0].Boundary = ActivationPending, nil
			case "namespace":
				next.Namespaces[0].NamespaceID, next.Activation[0].NamespaceID = fixtureID(4, 2), fixtureID(4, 2)
			case "account":
				next.AccountID = fixtureID(9, 2)
			case "revision":
				next.Revision = old.Revision
			case "created":
				next.CreatedAt = fixtureTime().Add(-time.Minute)
			case "clock":
				next.UpdatedAt = fixtureTime().Add(-time.Minute)
			case "deleted":
				next.DeletedAt = &next.UpdatedAt
			}
			if err := ValidateTransition(old, next); !errors.Is(err, ErrTransition) {
				t.Fatal("invalid transition accepted", err)
			}
		})
	}
	for _, mode := range []string{"edit", "disable", "delete"} {
		next := cloneRule(old)
		next.Revision++
		next.Activation[0].ID = fixtureID(8, 2)
		if mode == "edit" {
			next.Outcomes = []string{"success"}
		} else {
			next.Enabled = false
			next.Activation[0].Status, next.Activation[0].Boundary = ActivationDisabled, nil
			if mode == "delete" {
				next.DeletedAt = &next.UpdatedAt
			}
		}
		if err := ValidateTransition(old, next); err != nil {
			t.Fatal(mode, err)
		}
		if eligible, err := StillApplicable(*match, next); err != nil || eligible {
			t.Fatal(mode, "kept old delivery", err)
		}
	}
	old.Revision = math.MaxInt64
	next := cloneRule(old)
	next.Revision = math.MinInt64
	if err := ValidateTransition(old, next); !errors.Is(err, ErrTransition) {
		t.Fatal("revision overflow", err)
	}
}

func TestRuleBoundsIncludeAllNamespaceActivationsAndExactWireRevision(t *testing.T) {
	rule := fixtureRule(t)
	rule.Revision = 9007199254740993
	rule.Namespaces, rule.Activation = nil, nil
	for i := 1; i <= MaximumNamespaces; i++ {
		ref := fixtureRef(1+(i-1)/10, i)
		rule.Namespaces = append(rule.Namespaces, ref)
		rule.Activation = append(rule.Activation, Activation{ID: fixtureID(8, i), DeploymentID: ref.DeploymentID, NamespaceID: ref.NamespaceID, Status: ActivationActive, Boundary: &Boundary{ControlInstanceID: fixtureID(3, 1+(i-1)/10), RecoveryEpoch: "9223372036854775807", PrincipalID: fixtureID(7, i), HeadCursor: strings.Repeat("a", 1024), NotBefore: fixtureTime()}})
	}
	if err := rule.Validate(); err != nil {
		t.Fatal("legal maximum rejected", err)
	}
	encoded, err := json.Marshal(rule)
	if err != nil || len(encoded) > MaximumRuleBytes || !strings.Contains(string(encoded), `"revision":"9007199254740993"`) {
		t.Fatal("wire bounds/revision", err)
	}
	rule.Activation[1].ID = rule.Activation[0].ID
	if err := rule.Validate(); err == nil {
		t.Fatal("duplicate activation identity accepted")
	}
}

func TestOverlappingRulesKeepDistinctMatchContextWithoutInventingEventIdentity(t *testing.T) {
	first := fixtureRule(t)
	second := cloneRule(first)
	second.ID, second.Activation[0].ID, second.Scope = fixtureID(1, 2), fixtureID(8, 2), ScopeMyJobs
	event := fixtureEvent()
	a, b := requireMatch(t, first, event, true), requireMatch(t, second, event, true)
	if a.EventID != b.EventID || a.DeploymentID != b.DeploymentID || a.ControlInstanceID != b.ControlInstanceID || a.AccountID != b.AccountID || a.RuleID == b.RuleID {
		t.Fatal("overlap cannot be grouped by original source identity")
	}
	first.Enabled = false
	first.Activation[0] = Activation{ID: fixtureID(8, 3), DeploymentID: event.DeploymentID, NamespaceID: event.NamespaceID, Status: ActivationDisabled}
	if eligible, err := StillApplicable(*a, first); err != nil || eligible {
		t.Fatal(err)
	}
	if eligible, err := StillApplicable(*b, second); err != nil || !eligible {
		t.Fatal("other valid match suppressed", err)
	}
}
