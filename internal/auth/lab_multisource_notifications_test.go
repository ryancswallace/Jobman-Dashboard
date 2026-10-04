//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

const labMultiNotificationHelperCommit = "367006818e72315b01860f56f971004e17c24886"

var labMultiNotificationReasonCodes = []string{
	"common_command_deadline",
	"common_command_error_bound",
	"common_command_failed",
	"common_command_output_bound",
	"common_directory_identity",
	"common_duplicate_json_field",
	"common_failure",
	"common_file_changed",
	"common_file_identity",
	"common_file_parent_alias",
	"common_immutable_receipt_changed",
	"common_invalid_json_number",
	"failed",
	"invalid_result",
	"os_bad_descriptor",
	"os_error",
	"os_exists",
	"os_interrupted",
	"os_io",
	"os_is_directory",
	"os_no_space",
	"os_not_directory",
	"os_not_found",
	"os_permission_denied",
	"os_process_file_limit",
	"os_quota",
	"os_stale",
	"os_symlink",
	"os_system_file_limit",
	"os_timeout",
	"os_would_block",
}

type labMultiNotificationFixture struct {
	labNotificationFixture
	Profile string `json:"profile,omitempty"`
}

type labMultiNotificationBarrier struct {
	Receipt           string `json:"receipt"`
	Case              string `json:"case"`
	DeploymentID      string `json:"deploymentId"`
	ControlInstanceID string `json:"controlInstanceId"`
	NamespaceID       string `json:"namespaceId"`
	JobID             string `json:"jobId"`
	EventID           string `json:"eventId"`
	Settled           bool   `json:"settled"`
}

// Only a fixed diagnostic vocabulary may cross the subprocess boundary. Never
// print command errors, stderr, decoded values or helper error strings: these
// processes handle private database material. Success DTOs remain unchanged.
func labMultiNotificationFailure(raw []byte) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token := func(want json.Token) bool {
		value, err := decoder.Token()
		return err == nil && value == want
	}
	if !token(json.Delim('{')) || !token("scenarioFailure") || !token(json.Delim('{')) {
		return "", false
	}
	values := map[string]string{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || (name != "stage" && name != "code") || values[name] != "" {
			return "", false
		}
		var value string
		if decoder.Decode(&value) != nil || value == "" {
			return "", false
		}
		values[name] = value
	}
	if !token(json.Delim('}')) || !token(json.Delim('}')) {
		return "", false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", false
	}
	stages := []string{"host_input", "host_transport", "host_decode", "host_validate", "host_receipt", "guest_preflight", "guest_material", "helper_run", "guest_cleanup", "guest_postflight", "guest_barrier"}
	if !slices.Contains(stages, values["stage"]) || !slices.Contains(labMultiNotificationReasonCodes, values["code"]) {
		return "", false
	}
	return "scenario stage=" + values["stage"] + " code=" + values["code"], true
}

func labMultiNotificationResult(runError error, output, diagnostic *labBoundedNotificationOutput, target any) error {
	if output.overflow || diagnostic.overflow {
		return errors.New("scenario output_limit; retain immutable and pending receipts")
	}
	if runError != nil {
		if failure, ok := labMultiNotificationFailure(diagnostic.Bytes()); ok {
			return errors.New(failure + "; retain immutable and pending receipts")
		}
		return errors.New("scenario command_failed; retain immutable and pending receipts")
	}
	if diagnostic.Len() != 0 {
		return errors.New("scenario unexpected_stderr; retain immutable and pending receipts")
	}
	if _, ok := labMultiNotificationFailure(output.Bytes()); ok || json.Unmarshal(output.Bytes(), target) != nil {
		return errors.New("scenario invalid_success_json; retain immutable and pending receipts")
	}
	return nil
}

// Persist intent in this test process before invoking the mutating helper.
// An uncertain response must never make cleanup replay that cancellation.
// The deliberately verified idempotency check remains an explicit separate call.
type labMultiNotificationAttempts map[string]bool

func (attempted labMultiNotificationAttempts) once(key string, call func() error) error {
	if attempted[key] {
		return errors.New("scenario cancellation_already_attempted; retain immutable and pending receipts")
	}
	attempted[key] = true
	return call()
}

func labMultiNotificationSameEvent(a, b labNotificationEvent) bool {
	if !a.RecordedAt.Equal(b.RecordedAt) {
		return false
	}
	a.RecordedAt, b.RecordedAt = time.Time{}, time.Time{}
	return a == b
}

func labMultiNotificationPins(profile string) (notifications.NamespaceRef, string, string) {
	if profile == "primary" {
		return notifications.NamespaceRef{DeploymentID: labDeployment, NamespaceID: "4156b832-9be8-40ff-a471-cb3061b6001d"}, "e633cf92-258d-48ff-965a-fda88d68ef3a", ""
	}
	if profile == "secondary" {
		return notifications.NamespaceRef{DeploymentID: labSecondaryDeployment, NamespaceID: "455525f6-5d8a-4d4f-bea3-2d3f1ed4698f"}, labSecondaryInstance, "secondary-v1"
	}
	return notifications.NamespaceRef{}, "", ""
}

func labValidateMultiNotificationFixture(f labMultiNotificationFixture, profile, receipt string) error {
	ref, instance, wireProfile := labMultiNotificationPins(profile)
	if instance == "" || f.Profile != wireProfile || !f.Synthetic || f.FixtureVersion != 1 || f.ObservationMode != "normal-cancel-no-execution" || f.Receipt != receipt || len(receipt) != 32 || f.DeploymentID != ref.DeploymentID || f.NamespaceID != ref.NamespaceID || f.ControlInstanceID != instance || f.RecoveryEpoch != "1" || f.Namespace != "dashboard-research" || f.HelperCommit != labMultiNotificationHelperCommit || len(f.Jobs) != 2 {
		return errors.New("scenario source differs")
	}
	if f.Jobs[0].Case != "first" || f.Jobs[1].Case != "stopped" || !uuid(f.Jobs[0].JobID) || !uuid(f.Jobs[1].JobID) || f.Jobs[0].JobID == f.Jobs[1].JobID {
		return errors.New("scenario jobs differ")
	}
	return nil
}

func labValidateMultiNotificationEvent(e labNotificationEvent, f labMultiNotificationFixture, selected string) error {
	index := 0
	if selected == "stopped" {
		index = 1
	} else if selected != "first" {
		return errors.New("unknown scenario case")
	}
	revision, err := strconv.ParseInt(e.JobRevision, 10, 64)
	if len(f.Jobs) != 2 || err != nil || revision < 1 || strconv.FormatInt(revision, 10) != e.JobRevision || !e.Synthetic || e.Receipt != f.Receipt || e.Case != selected || e.DeploymentID != f.DeploymentID || e.ControlInstanceID != f.ControlInstanceID || e.RecoveryEpoch != f.RecoveryEpoch || e.NamespaceID != f.NamespaceID || e.JobID != f.Jobs[index].JobID || !uuid(e.EventID) || e.Outcome != "cancelled" || e.RecordedAt.IsZero() || e.RunID != "" || e.ExecutionID != "" {
		return errors.New("terminal event source differs")
	}
	return nil
}

func (b labMultiNotificationBarrier) matches(e labNotificationEvent) bool {
	return b.Receipt == e.Receipt && b.Case == e.Case && b.DeploymentID == e.DeploymentID && b.ControlInstanceID == e.ControlInstanceID && b.NamespaceID == e.NamespaceID && b.JobID == e.JobID && b.EventID == e.EventID
}

// Other acceptance runs or users may have their own namespace rules. Preserve
// those matches; require only this test's exact contribution, including absence
// of the other source's rule and the other owner's my-jobs rule.
func labMultiNotificationMatches(item notifications.InboxItem, event labNotificationEvent, owned []string, want map[string]string) error {
	if item.Job != (notifications.JobRef{DeploymentID: event.DeploymentID, NamespaceID: event.NamespaceID, JobID: event.JobID}) || item.ControlInstanceID != event.ControlInstanceID || item.EventID != event.EventID || item.Outcome != event.Outcome || !item.EventAt.Equal(event.RecordedAt) {
		return errors.New("inbox event provenance differs")
	}
	seen := map[string]bool{}
	for _, match := range item.MatchedRules {
		if !slices.Contains(owned, match.RuleID) {
			continue
		}
		revision, ok := want[match.RuleID]
		if !ok || seen[match.RuleID] || revision != match.Revision {
			return errors.New("source, owner or rule version contaminated match")
		}
		seen[match.RuleID] = true
	}
	if len(seen) != len(want) {
		return errors.New("expected test rule absent")
	}
	return nil
}

// Run only after an independently reviewed deployment. This test never installs
// binaries, changes source configuration, creates SQL events or resets a feed.
func TestLabTwoControlTerminalNotifications(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_MULTISOURCE_NOTIFICATIONS") != "1" {
		t.Skip("set runtime and two-source notification opt-ins after independent review")
	}
	sessions := []labNativeSession{labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001"), labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")}
	for i, want := range []string{"a09dbdf8-aa70-42e7-b58e-fa724f9e3b0d", "bf296166-4679-441c-b673-71148032d752"} {
		if sessions[i].subject != want {
			t.Fatal("Verified synthetic account differs")
		}
	}
	client := newLabNotificationHTTP(t, sessions[0])
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	helper := filepath.Join(sessions[0].root, "scripts/dashboard-multisource-notification-scenario.py")
	info, err := os.Lstat(helper)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		t.Fatal("Reviewed bounded two-source helper unavailable")
	}
	scenario := func(callCtx context.Context, target any, profile, action, receipt, selected string) error {
		callCtx, done := context.WithTimeout(callCtx, 55*time.Second)
		defer done()
		args := []string{helper, profile, action, receipt}
		if selected != "" {
			args = append(args, selected)
		}
		command := exec.CommandContext(callCtx, "python3", args...)
		var output, diagnostic labBoundedNotificationOutput
		command.Stdout, command.Stderr = &output, &diagnostic
		return labMultiNotificationResult(command.Run(), &output, &diagnostic, target)
	}
	request := func(method, path string, user int, revision string, input, target any, want int) {
		t.Helper()
		status, err := client.call(ctx, method, path, sessions[user].accessToken, revision, input, target)
		if err != nil || status != want {
			t.Fatalf("Two-source notification %s returned HTTP%d, expected%d (transportOrContractFailure=%t)", method, status, want, err != nil)
		}
	}
	profiles := []string{"primary", "secondary"}
	fixtures := make([]labMultiNotificationFixture, 0, 2)
	type ownedRule struct {
		user int
		view notifications.RuleView
	}
	created := []ownedRule{}
	uncertainRuleAdmission := false
	completed := map[string]bool{}
	attempted := labMultiNotificationAttempts{}
	// Delete only admitted test-owned rules, using fresh CAS. If cleanup cannot
	// prove they stopped, leave remaining jobs accepted and report their receipts.
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Minute)
		defer done()
		stopped := !uncertainRuleAdmission
		for _, rule := range created {
			path := "/api/v1/rules/" + rule.view.ID
			var current notifications.RuleView
			status, err := client.call(cleanup, "GET", path, sessions[rule.user].accessToken, "", nil, &current)
			if err == nil && status == 404 {
				continue
			}
			if err != nil || status != 200 || current.ID != rule.view.ID {
				stopped = false
				t.Errorf("Inspect test-owned rule for cleanup: %s", rule.view.ID)
				continue
			}
			status, err = client.call(cleanup, "DELETE", path, sessions[rule.user].accessToken, current.Revision, nil, nil)
			if err != nil || status != 204 {
				stopped = false
				t.Errorf("Delete test-owned rule explicitly: %s", rule.view.ID)
			}
		}
		if !stopped {
			t.Error("Remaining accepted test jobs retained because rule cleanup is unconfirmed")
			return
		}
		for i, f := range fixtures {
			for _, selected := range []string{"first", "stopped"} {
				key := f.Receipt + selected
				if completed[key] {
					continue
				}
				if attempted[key] {
					t.Logf("Uncertain cancellation retained without retry for %s receipt %s case %s", profiles[i], f.Receipt, selected)
					continue
				}
				var event labNotificationEvent
				if err := attempted.once(key, func() error { return scenario(cleanup, &event, profiles[i], "complete", f.Receipt, selected) }); err != nil {
					t.Errorf("Explicit cleanup needed for %s receipt %s case %s: %s", profiles[i], f.Receipt, selected, err)
				} else if labValidateMultiNotificationEvent(event, f, selected) != nil {
					t.Errorf("Cleanup event validation differs for %s receipt %s case %s", profiles[i], f.Receipt, selected)
				}
			}
		}
	})
	for _, profile := range profiles {
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			t.Fatal("Scenario nonce unavailable")
		}
		receipt := hex.EncodeToString(nonce[:])
		t.Logf("Synthetic %s scenario receipt %s (retain on uncertainty)", profile, receipt)
		var f labMultiNotificationFixture
		if err := scenario(ctx, &f, profile, "prepare", receipt, ""); err != nil {
			t.Fatalf("Scenario preparation failed: %s", err)
		}
		if labValidateMultiNotificationFixture(f, profile, receipt) != nil {
			t.Fatal("Scenario fixture validation differs; inspect the logged receipt before any retry")
		}
		fixtures = append(fixtures, f)
	}
	refs := []notifications.NamespaceRef{}
	for _, profile := range profiles {
		ref, _, _ := labMultiNotificationPins(profile)
		refs = append(refs, ref)
	}
	for user := range sessions {
		var bootstrap api.Bootstrap
		request("GET", "/api/v1/bootstrap", user, "", nil, &bootstrap, 200)
		if bootstrap.FixtureMode || bootstrap.Completeness != "complete" {
			t.Fatal("Live discovery must be complete")
		}
		for source, f := range fixtures {
			var detail api.JobDetail
			path := labMultiPrefix(api.Scope{DeploymentID: f.DeploymentID, NamespaceID: f.NamespaceID}) + "/jobs/" + f.Jobs[0].JobID
			request("GET", path, user, "", nil, &detail, 200)
			if detail.Job.ID != f.Jobs[0].JobID || detail.Job.Scope != (api.Scope{DeploymentID: f.DeploymentID, NamespaceID: f.NamespaceID}) || detail.Job.Phase == "terminal" || detail.Job.Owner == nil || detail.Job.Owner.IsCurrentUser != (source == user) {
				t.Fatal("Source-qualified member visibility or inverted ownership differs")
			}
		}
	}
	create := func(user int, input notifications.RuleInput) notifications.RuleView {
		t.Helper()
		var rule notifications.RuleView
		uncertainRuleAdmission = true
		request("POST", "/api/v1/rules", user, "", input, &rule, 201)
		if !uuid(rule.ID) || rule.Revision != "1" || !rule.Enabled {
			t.Fatal("Personal rule admission differs")
		}
		created = append(created, ownedRule{user, rule})
		uncertainRuleAdmission = false
		activate, done := context.WithTimeout(ctx, 80*time.Second)
		defer done()
		for {
			active := rule.Enabled && len(rule.Scopes) == len(input.Namespaces) && rule.InaccessibleScopes == 0 && rule.UnavailableScopes == 0
			for _, ref := range input.Namespaces {
				active = active && slices.ContainsFunc(rule.Scopes, func(scope notifications.RuleScopeView) bool {
					return scope.NamespaceRef == ref && scope.Status == "active" && scope.ActivatedAt != nil
				})
			}
			if active {
				created[len(created)-1].view = rule
				return rule
			}
			select {
			case <-activate.Done():
				t.Fatal("Both source activation boundaries did not become active")
			case <-time.After(time.Second):
			}
			rule = notifications.RuleView{}
			request("GET", "/api/v1/rules/"+created[len(created)-1].view.ID, user, "", nil, &rule, 200)
		}
	}
	var rules [2][3]notifications.RuleView
	for user := range sessions {
		for source := range 2 {
			input := notifications.RuleInput{Name: "Lab two-source " + fixtures[source].Receipt[:8], Enabled: true, Scope: notifications.ScopeNamespaceJobs, Namespaces: []notifications.NamespaceRef{refs[source]}, Jobs: []notifications.JobRef{}, OutcomeMode: notifications.OutcomeSelected, Outcomes: []string{"cancelled"}}
			rules[user][source] = create(user, input)
		}
		input := notifications.RuleInput{Name: "Lab two-source mine " + fixtures[user].Receipt[:8], Enabled: true, Scope: notifications.ScopeMyJobs, Namespaces: refs, Jobs: []notifications.JobRef{}, OutcomeMode: notifications.OutcomeSelected, Outcomes: []string{"cancelled"}}
		rules[user][2] = create(user, input)
	}
	request("GET", "/api/v1/rules/"+rules[0][0].ID, 1, "", nil, nil, 404)
	ownedIDs := []string{}
	for _, rule := range created {
		ownedIDs = append(ownedIDs, rule.view.ID)
	}
	complete := func(source int, selected string) labNotificationEvent {
		t.Helper()
		f := fixtures[source]
		var event, repeat labNotificationEvent
		if err := attempted.once(f.Receipt+selected, func() error { return scenario(ctx, &event, profiles[source], "complete", f.Receipt, selected) }); err != nil {
			t.Fatalf("Normal Control cancellation command failed: %s", err)
		}
		if labValidateMultiNotificationEvent(event, f, selected) != nil {
			t.Fatal("Normal Control cancellation event validation differs")
		}
		completed[f.Receipt+selected] = true
		if err := scenario(ctx, &repeat, profiles[source], "complete", f.Receipt, selected); err != nil {
			t.Fatalf("Idempotent cancellation command failed: %s", err)
		}
		if !labMultiNotificationSameEvent(repeat, event) {
			t.Fatal("Idempotent normal cancellation changed original event identity")
		}
		barrierCtx, done := context.WithTimeout(ctx, 90*time.Second)
		defer done()
		for {
			var barrier labMultiNotificationBarrier
			if err := scenario(barrierCtx, &barrier, profiles[source], "settled", f.Receipt, selected); err != nil {
				t.Fatalf("Exact source event barrier command failed: %s", err)
			}
			if !barrier.matches(event) {
				t.Fatal("Exact source event barrier identity differs")
			}
			if barrier.Settled {
				break
			}
			select {
			case <-barrierCtx.Done():
				t.Fatal("Publisher, ingestion and evaluation did not settle")
			case <-time.After(2 * time.Second):
			}
		}
		for user := range sessions {
			var detail api.JobDetail
			request("GET", labMultiPrefix(api.Scope{DeploymentID: f.DeploymentID, NamespaceID: f.NamespaceID})+"/jobs/"+event.JobID, user, "", nil, &detail, 200)
			if detail.Job.ID != event.JobID || detail.Job.Phase != "terminal" || detail.Job.Outcome != event.Outcome || detail.Job.Revision != event.JobRevision || detail.Job.CurrentRun != nil || detail.Job.Owner == nil || detail.Job.Owner.IsCurrentUser != (source == user) {
				t.Fatal("Actual cancelled job detail or owner differs")
			}
		}
		return event
	}
	list := func(user int, scopes []notifications.NamespaceRef, unread bool) []notifications.InboxItem {
		t.Helper()
		raw, _ := json.Marshal(scopes)
		query := url.Values{"scope": {string(raw)}, "limit": {"50"}, "unread": {strconv.FormatBool(unread)}}
		items := []notifications.InboxItem{}
		seen := map[string]bool{}
		cursors := map[string]bool{}
		for pageNo := 0; pageNo < 20; pageNo++ {
			var page notifications.InboxPage
			request("GET", "/api/v1/inbox?"+query.Encode(), user, "", nil, &page, 200)
			if page.Validate() != nil || page.Completeness != "complete" {
				t.Fatal("Authorized inbox page incomplete")
			}
			for _, item := range page.Items {
				if !slices.Contains(scopes, notifications.NamespaceRef{DeploymentID: item.Job.DeploymentID, NamespaceID: item.Job.NamespaceID}) || seen[item.ID] {
					t.Fatal("Inbox page escaped explicit source scope or duplicated identity")
				}
				seen[item.ID] = true
				items = append(items, item)
			}
			if page.NextCursor == "" {
				return items
			}
			if cursors[page.NextCursor] {
				t.Fatal("Inbox cursor repeated")
			}
			cursors[page.NextCursor] = true
			query.Set("cursor", page.NextCursor)
		}
		t.Fatal("Inbox exceeds bounded 1000-item acceptance traversal")
		return nil
	}
	find := func(items []notifications.InboxItem, event labNotificationEvent) []notifications.InboxItem {
		return slices.DeleteFunc(slices.Clone(items), func(item notifications.InboxItem) bool {
			return item.EventID != event.EventID || item.ControlInstanceID != event.ControlInstanceID || item.Job.DeploymentID != event.DeploymentID
		})
	}
	events := []labNotificationEvent{complete(0, "first"), complete(1, "first")}
	if events[0].EventID == events[1].EventID || events[0].JobID == events[1].JobID {
		t.Fatal("Independent scenario identities unexpectedly collide")
	}
	var retained [2][2]notifications.InboxItem
	for user := range sessions {
		aggregate := list(user, refs, false)
		for source, event := range events {
			matches := find(aggregate, event)
			scoped := find(list(user, refs[source:source+1], false), event)
			if len(matches) != 1 || len(scoped) != 1 || matches[0].ID != scoped[0].ID {
				t.Fatal("Aggregate and individual source inbox identities differ")
			}
			want := map[string]string{rules[user][source].ID: rules[user][source].Revision}
			if source == user {
				want[rules[user][2].ID] = rules[user][2].Revision
			}
			item := matches[0]
			if labMultiNotificationMatches(item, event, ownedIDs, want) != nil || item.Read || item.ReadAt != nil {
				t.Fatal("Original event, owner or exact test rule matching differs")
			}
			retained[user][source] = item
			path := "/api/v1/inbox/" + item.ID
			request("GET", path, 1-user, "", nil, nil, 404)
			request("PATCH", path, 1-user, "", map[string]bool{"read": true}, nil, 404)
			t.Logf("Validated account%d source%s job%s originalEvent%s inbox%s", user, profiles[source], event.JobID, event.EventID, item.ID)
		}
	}
	// Reading Alice's primary event must leave her secondary event and both Bob
	// items unread. The response is decoded into a fresh value on every mutation.
	var read notifications.InboxItem
	request("PATCH", "/api/v1/inbox/"+retained[0][0].ID, 0, "", map[string]bool{"read": true}, &read, 200)
	if !read.Read || read.ReadAt == nil || read.ID != retained[0][0].ID {
		t.Fatal("Authoritative read state differs")
	}
	for user := range sessions {
		items := list(user, refs, true)
		for source, event := range events {
			want := 1
			if user == 0 && source == 0 {
				want = 0
			}
			if len(find(items, event)) != want {
				t.Fatal("Read state leaked across source or account")
			}
		}
	}
	for _, rule := range created {
		var current, stopped notifications.RuleView
		path := "/api/v1/rules/" + rule.view.ID
		request("GET", path, rule.user, "", nil, &current, 200)
		request("PUT", path+"/enabled", rule.user, current.Revision, map[string]bool{"enabled": false}, &stopped, 200)
		if stopped.ID != current.ID || stopped.Enabled || stopped.Revision == current.Revision {
			t.Fatal("Immediate rule stop did not advance revision")
		}
	}
	stoppedEvents := []labNotificationEvent{complete(0, "stopped"), complete(1, "stopped")}
	for user := range sessions {
		items := list(user, refs, false)
		for source, event := range stoppedEvents {
			if event.EventID == events[source].EventID {
				t.Fatal("New cancelled job reused original event")
			}
			for _, item := range find(items, event) {
				if labMultiNotificationMatches(item, event, ownedIDs, map[string]string{}) != nil {
					t.Fatal("Stopped test intent contributed another notification")
				}
			}
			old := find(items, events[source])
			if len(old) != 1 || old[0].ID != retained[user][source].ID {
				t.Fatal("Stopping current rules erased or replaced prior inbox history")
			}
		}
	}
	t.Log("PASS: two independent normal Control terminal events -> current service ingestion -> source-qualified personal rules -> exact original-event inbox/account; inverted owners, aggregate equality, account/read isolation, stopped-rule suppression and scoped cleanup. No APNs or execution claim.")
}

func TestLabMultiNotificationReceiptGuards(t *testing.T) {
	for _, profile := range []string{"primary", "secondary"} {
		ref, instance, wire := labMultiNotificationPins(profile)
		var fixture labMultiNotificationFixture
		raw, _ := json.Marshal(map[string]any{"profile": wire, "synthetic": true, "fixtureVersion": 1, "observationMode": "normal-cancel-no-execution", "helperCommit": labMultiNotificationHelperCommit, "receipt": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "deploymentId": ref.DeploymentID, "controlInstanceId": instance, "recoveryEpoch": "1", "namespaceId": ref.NamespaceID, "namespace": "dashboard-research", "jobs": []map[string]string{{"case": "first", "jobId": "76000000-0000-4000-8000-000000000001"}, {"case": "stopped", "jobId": "76000000-0000-4000-8000-000000000002"}}})
		if json.Unmarshal(raw, &fixture) != nil || labValidateMultiNotificationFixture(fixture, profile, fixture.Receipt) != nil {
			t.Fatal("valid fixture rejected")
		}
		changed := fixture
		changed.DeploymentID = ref.NamespaceID
		if labValidateMultiNotificationFixture(changed, profile, fixture.Receipt) == nil {
			t.Fatal("source relabeling accepted")
		}
		event := labNotificationEvent{Synthetic: true, Receipt: fixture.Receipt, Case: "first", DeploymentID: ref.DeploymentID, ControlInstanceID: instance, RecoveryEpoch: "1", NamespaceID: ref.NamespaceID, JobID: fixture.Jobs[0].JobID, EventID: "77000000-0000-4000-8000-000000000001", Outcome: "cancelled", JobRevision: "2", RecordedAt: time.Unix(1791108000, 0).UTC()}
		if labValidateMultiNotificationEvent(event, fixture, "first") != nil {
			t.Fatal("valid event rejected")
		}
		for _, mutate := range []func(*labNotificationEvent){func(e *labNotificationEvent) { e.ControlInstanceID = ref.NamespaceID }, func(e *labNotificationEvent) { e.JobID = fixture.Jobs[1].JobID }, func(e *labNotificationEvent) { e.RunID = e.JobID }, func(e *labNotificationEvent) { e.JobRevision = "02" }} {
			changed := event
			mutate(&changed)
			if labValidateMultiNotificationEvent(changed, fixture, "first") == nil {
				t.Fatal("changed source/run/revision accepted")
			}
		}
		barrier := labMultiNotificationBarrier{Receipt: event.Receipt, Case: event.Case, DeploymentID: event.DeploymentID, ControlInstanceID: event.ControlInstanceID, NamespaceID: event.NamespaceID, JobID: event.JobID, EventID: event.EventID, Settled: true}
		if !barrier.matches(event) {
			t.Fatal("exact barrier rejected")
		}
		barrier.DeploymentID = ref.NamespaceID
		if barrier.matches(event) {
			t.Fatal("cross-source barrier accepted")
		}
	}
}

func TestLabMultiNotificationContributionGuards(t *testing.T) {
	event := labNotificationEvent{DeploymentID: labDeployment, ControlInstanceID: "e633cf92-258d-48ff-965a-fda88d68ef3a", NamespaceID: "4156b832-9be8-40ff-a471-cb3061b6001d", JobID: "76000000-0000-4000-8000-000000000001", EventID: "77000000-0000-4000-8000-000000000001", Outcome: "cancelled", RecordedAt: time.Unix(1791108000, 0).UTC()}
	item := notifications.InboxItem{Job: notifications.JobRef{DeploymentID: event.DeploymentID, NamespaceID: event.NamespaceID, JobID: event.JobID}, ControlInstanceID: event.ControlInstanceID, EventID: event.EventID, Outcome: event.Outcome, EventAt: event.RecordedAt, MatchedRules: []notifications.InboxMatch{{RuleID: "own", Revision: "3"}, {RuleID: "unrelated", Revision: "1"}}}
	owned := []string{"own", "other-source", "other-owner"}
	want := map[string]string{"own": "3"}
	if labMultiNotificationMatches(item, event, owned, want) != nil {
		t.Fatal("unrelated rule must remain permitted")
	}
	for _, bad := range []notifications.InboxMatch{{RuleID: "other-source", Revision: "1"}, {RuleID: "other-owner", Revision: "1"}, {RuleID: "own", Revision: "2"}} {
		changed := item
		changed.MatchedRules = append(slices.Clone(item.MatchedRules), bad)
		if labMultiNotificationMatches(changed, event, owned, want) == nil {
			t.Fatal("contaminated test contribution accepted")
		}
	}
	item.Job.DeploymentID = labSecondaryDeployment
	if labMultiNotificationMatches(item, event, owned, want) == nil {
		t.Fatal("cross-source inbox accepted")
	}
}

func TestLabMultiNotificationDiagnostics(t *testing.T) {
	const canary = "private-database-password-canary"
	for _, stage := range []string{"host_input", "host_transport", "host_decode", "host_validate", "host_receipt", "guest_preflight", "guest_material", "helper_run", "guest_cleanup", "guest_postflight", "guest_barrier"} {
		for _, code := range labMultiNotificationReasonCodes {
			frame, _ := json.Marshal(map[string]any{"scenarioFailure": map[string]string{"stage": stage, "code": code}})
			var output, diagnostic labBoundedNotificationOutput
			_, _ = diagnostic.Write(frame)
			err := labMultiNotificationResult(errors.New(canary), &output, &diagnostic, &labNotificationEvent{})
			want := "scenario stage=" + stage + " code=" + code + "; retain immutable and pending receipts"
			if err == nil || err.Error() != want {
				t.Fatal("Fixed failure stage was lost")
			}
		}
	}
	for _, raw := range []string{
		canary,
		`{"scenarioFailure":{"stage":"helper_run","code":"` + canary + `"}}`,
		`{"scenarioFailure":{"stage":"` + canary + `","code":"failed"}}`,
		`{"scenarioFailure":{"stage":"helper_run","code":"failed","secret":"` + canary + `"}}`,
		`{"scenarioFailure":{"stage":"helper_run","code":"failed"},"secret":"` + canary + `"}`,
		`{"scenarioFailure":{"stage":"helper_run","stage":"host_receipt","code":"failed"}}`,
		`{"scenarioFailure":{"stage":"helper_run","code":null}}`,
		`{"scenarioFailure":{"stage":"helper_run","code":"failed"}} {}`,
	} {
		var output, diagnostic labBoundedNotificationOutput
		_, _ = diagnostic.Write([]byte(raw))
		err := labMultiNotificationResult(errors.New(canary), &output, &diagnostic, &labNotificationEvent{})
		if err == nil || err.Error() != "scenario command_failed; retain immutable and pending receipts" {
			t.Fatal("Unrecognized diagnostic escaped its fixed failure code")
		}
	}
	for _, stderr := range []bool{false, true} {
		var output, diagnostic labBoundedNotificationOutput
		if stderr {
			diagnostic.overflow = true
		} else {
			output.overflow = true
		}
		err := labMultiNotificationResult(errors.New(canary), &output, &diagnostic, &labNotificationEvent{})
		if err == nil || err.Error() != "scenario output_limit; retain immutable and pending receipts" {
			t.Fatal("Bounded output failure was lost")
		}
	}
	var output, diagnostic labBoundedNotificationOutput
	_, _ = output.Write([]byte(`{"receipt":"success-unchanged"}`))
	var event labNotificationEvent
	if labMultiNotificationResult(nil, &output, &diagnostic, &event) != nil || event.Receipt != "success-unchanged" {
		t.Fatal("Success DTO changed")
	}
	_, _ = diagnostic.Write([]byte(canary))
	if err := labMultiNotificationResult(nil, &output, &diagnostic, &event); err == nil || err.Error() != "scenario unexpected_stderr; retain immutable and pending receipts" {
		t.Fatal("Unexpected stderr was exposed or ignored")
	}
	diagnostic.Reset()
	for _, raw := range []string{canary, `{"scenarioFailure":{"stage":"helper_run","code":"failed"}}`} {
		output.Reset()
		_, _ = output.Write([]byte(raw))
		if err := labMultiNotificationResult(nil, &output, &diagnostic, &event); err == nil || err.Error() != "scenario invalid_success_json; retain immutable and pending receipts" {
			t.Fatal("Failure payload admitted as success")
		}
	}
}

func TestLabMultiNotificationEventTimeEquality(t *testing.T) {
	event := labNotificationEvent{EventID: "77000000-0000-4000-8000-000000000001", JobRevision: "2", RecordedAt: time.Unix(1791108000, 123456000).UTC()}
	repeat := event
	repeat.RecordedAt = repeat.RecordedAt.In(time.FixedZone("synthetic offset", 2*60*60))
	if repeat == event || !labMultiNotificationSameEvent(event, repeat) {
		t.Fatal("Equivalent timestamp instants must compare independently of location pointers")
	}
	repeat.RecordedAt = repeat.RecordedAt.Add(time.Nanosecond)
	if labMultiNotificationSameEvent(event, repeat) {
		t.Fatal("Changed original event time accepted")
	}
	repeat = event
	repeat.JobRevision = "3"
	if labMultiNotificationSameEvent(event, repeat) {
		t.Fatal("Changed original event revision accepted")
	}
}

func TestLabMultiNotificationUncertainCancellationNeverRetries(t *testing.T) {
	attempted := labMultiNotificationAttempts{}
	calls := 0
	uncertain := errors.New("synthetic lost response")
	call := func() error { calls++; return uncertain }
	if err := attempted.once("primary-first", func() error {
		if !attempted["primary-first"] {
			t.Fatal("Cancellation was invoked before recording intent")
		}
		return call()
	}); err != uncertain || !attempted["primary-first"] || calls != 1 {
		t.Fatal("Cancellation attempt was not retained before uncertain result")
	}
	if err := attempted.once("primary-first", call); err == nil || calls != 1 {
		t.Fatal("Uncertain cancellation could be replayed by cleanup")
	}
	if err := attempted.once("primary-stopped", call); err != uncertain || calls != 2 || !attempted["primary-stopped"] {
		t.Fatal("Never-attempted cleanup case did not get exactly one attempt")
	}
	if err := attempted.once("primary-stopped", call); err == nil || calls != 2 {
		t.Fatal("Uncertain cleanup cancellation was repeated")
	}
	if err := attempted.once("secondary-first", func() error { calls++; return nil }); err != nil || !attempted["secondary-first"] || calls != 3 {
		t.Fatal("Successful response lost the original attempted fence")
	}
	if err := attempted.once("secondary-first", call); err == nil || calls != 3 {
		t.Fatal("Validation failure after successful command could permit cleanup replay")
	}
}
