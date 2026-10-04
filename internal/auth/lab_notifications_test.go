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
	"net"
	"net/http"
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

type labNotificationFixture struct {
	Synthetic         bool   `json:"synthetic"`
	FixtureVersion    int    `json:"fixtureVersion"`
	ObservationMode   string `json:"observationMode"`
	HelperCommit      string `json:"helperCommit"`
	Receipt           string `json:"receipt"`
	DeploymentID      string `json:"deploymentId"`
	ControlInstanceID string `json:"controlInstanceId"`
	RecoveryEpoch     string `json:"recoveryEpoch"`
	NamespaceID       string `json:"namespaceId"`
	Namespace         string `json:"namespace"`
	Jobs              []struct {
		Case  string `json:"case"`
		JobID string `json:"jobId"`
	} `json:"jobs"`
}
type labNotificationEvent struct {
	Synthetic         bool      `json:"synthetic"`
	Receipt           string    `json:"receipt"`
	Case              string    `json:"case"`
	DeploymentID      string    `json:"deploymentId"`
	ControlInstanceID string    `json:"controlInstanceId"`
	RecoveryEpoch     string    `json:"recoveryEpoch"`
	NamespaceID       string    `json:"namespaceId"`
	JobID             string    `json:"jobId"`
	EventID           string    `json:"eventId"`
	Outcome           string    `json:"outcome"`
	JobRevision       string    `json:"jobRevision"`
	RecordedAt        time.Time `json:"recordedAt"`
	RunID             string    `json:"runId,omitempty"`
	ExecutionID       string    `json:"executionId,omitempty"`
}

type labNotificationHTTP struct{ client *http.Client }

func newLabNotificationHTTP(t *testing.T, session labNativeSession) labNotificationHTTP {
	t.Helper()
	transport := session.transport.Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "dashboard.lab.test:8443" {
			return nil, ErrUnauthenticated
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "10.77.0.10:8443")
	}
	t.Cleanup(transport.CloseIdleConnections)
	return labNotificationHTTP{&http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c labNotificationHTTP) call(ctx context.Context, method, path, token, revision string, input, target any) (int, error) {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil || len(body) > 64<<10 {
			return 0, errors.New("invalid bounded synthetic request")
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, "https://dashboard.lab.test:8443"+path, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("invalid synthetic endpoint")
	}
	r.Header.Set("Authorization", "Bearer "+token)
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if revision != "" {
		r.Header.Set("If-Match", `"`+revision+`"`)
	}
	response, err := c.client.Do(r)
	if err != nil {
		return 0, errors.New("deployed notification request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return response.StatusCode, errors.New("deployed notification response exceeded bound")
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && target != nil && json.Unmarshal(data, target) != nil {
		return response.StatusCode, errors.New("public notification response differs from contract")
	}
	return response.StatusCode, nil
}

// No source or Dashboard SQL is issued by this API harness. The separately
// reviewed Lab helper creates only two named jobs through normal Store methods
// and reads exact event tuples for the processing barrier. Source observations
// remain synthetic; this test does not establish APNs or workload execution.
func TestLabDeployedNotificationsFromControlTerminalEvents(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_RUNTIME") != "1" || os.Getenv("JOBMAN_DASHBOARD_LAB_NOTIFICATIONS") != "1" {
		t.Skip("set runtime and notifications Lab opt-ins after reviewed deployment")
	}
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	client := newLabNotificationHTTP(t, alice)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	request := func(method, path, token, revision string, input, target any, want int) {
		t.Helper()
		status, err := client.call(ctx, method, path, token, revision, input, target)
		if err != nil || status != want {
			t.Fatalf("Notification API %s returned HTTP%d, expected%d (transportOrContractFailure=%t)", method, status, want, err != nil)
		}
	}
	helper := filepath.Join(alice.root, "scripts/dashboard-notification-scenario.py")
	info, err := os.Lstat(helper)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		t.Fatal("Reviewed synthetic notification helper unavailable")
	}
	scenario := func(target any, args ...string) {
		t.Helper()
		callCtx, done := context.WithTimeout(ctx, 55*time.Second)
		defer done()
		command := exec.CommandContext(callCtx, "python3", append([]string{helper}, args...)...)
		var output labBoundedNotificationOutput
		command.Stdout = &output
		if err := command.Run(); err != nil || output.overflow || json.Unmarshal(output.Bytes(), target) != nil {
			t.Fatal("Reviewed notification scenario failed; preserve its private pending and public evidence receipts")
		}
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal("Scenario identity unavailable")
	}
	receipt := hex.EncodeToString(nonce[:])
	var fixture labNotificationFixture
	scenario(&fixture, "prepare", "--receipt", receipt)
	if !fixture.Synthetic || fixture.FixtureVersion != 1 || fixture.ObservationMode != "normal-cancel-no-execution" || fixture.Receipt != receipt || fixture.DeploymentID != labDeployment || !uuid(fixture.ControlInstanceID) || !uuid(fixture.NamespaceID) || fixture.Namespace != "dashboard-research" || len(fixture.HelperCommit) != 40 || len(fixture.Jobs) != 2 || fixture.Jobs[0].Case != "first" || fixture.Jobs[1].Case != "stopped" || !uuid(fixture.Jobs[0].JobID) || !uuid(fixture.Jobs[1].JobID) || fixture.Jobs[0].JobID == fixture.Jobs[1].JobID {
		t.Fatal("Scenario did not return exact bounded source-qualified jobs")
	}
	for _, session := range []labNativeSession{alice, bob} {
		var bootstrap api.Bootstrap
		request("GET", "/api/v1/bootstrap", session.accessToken, "", nil, &bootstrap, 200)
		if bootstrap.FixtureMode || bootstrap.Completeness != "complete" {
			t.Fatal("Actual authenticated source discovery is not complete")
		}
		var job api.JobDetail
		request("GET", "/api/v1/deployments/"+labDeployment+"/namespaces/"+fixture.NamespaceID+"/jobs/"+fixture.Jobs[0].JobID, session.accessToken, "", nil, &job, 200)
		if job.Job.Phase == "terminal" || job.Job.Owner == nil || job.Job.Owner.IsCurrentUser != (session.accessToken == alice.accessToken) {
			t.Fatal("Prepared jobs must be nonterminal with exact represented-user ownership")
		}
	}
	ref := notifications.NamespaceRef{DeploymentID: labDeployment, NamespaceID: fixture.NamespaceID}
	base := notifications.RuleInput{Name: "Lab notification " + receipt[:8], Enabled: true, Scope: notifications.ScopeNamespaceJobs, Namespaces: []notifications.NamespaceRef{ref}, Jobs: []notifications.JobRef{}, OutcomeMode: notifications.OutcomeAllTerminal, Outcomes: []string{}}
	type ownedRule struct{ id, token string }
	created := []ownedRule{}
	// Cleanup stops only personal rules created by this run. Source events and
	// receipts remain as acceptance evidence; no pre-existing rule is changed.
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 45*time.Second)
		defer done()
		for _, rule := range created {
			var current notifications.RuleView
			path := "/api/v1/rules/" + rule.id
			status, err := client.call(cleanup, "GET", path, rule.token, "", nil, &current)
			if err == nil && status == 404 {
				continue
			}
			if err != nil || status != 200 || current.ID != rule.id {
				t.Errorf("Cannot inspect test-owned rule during cleanup: %s", rule.id)
				continue
			}
			if status, err = client.call(cleanup, "DELETE", path, rule.token, current.Revision, nil, nil); err != nil || status != 204 {
				t.Errorf("Test-owned rule requires explicit cleanup: %s", rule.id)
			}
		}
	})
	create := func(token string, input notifications.RuleInput) notifications.RuleView {
		t.Helper()
		var rule notifications.RuleView
		request("POST", "/api/v1/rules", token, "", input, &rule, 201)
		if !uuid(rule.ID) || rule.Revision != "1" || !rule.Enabled {
			t.Fatal("Rule admission differs")
		}
		created = append(created, ownedRule{rule.ID, token})
		deadline := time.NewTimer(80 * time.Second)
		defer deadline.Stop()
		for {
			if len(rule.Scopes) == 1 && rule.Scopes[0].NamespaceRef == ref && rule.Scopes[0].Status == "active" && rule.Scopes[0].ActivatedAt != nil {
				return rule
			}
			select {
			case <-ctx.Done():
				t.Fatal("Rule activation context expired")
			case <-deadline.C:
				t.Fatal("Normal pending-rule worker did not activate source boundary")
			case <-time.After(time.Second):
			}
			request("GET", "/api/v1/rules/"+rule.ID, token, "", nil, &rule, 200)
		}
	}
	aliceAll := create(alice.accessToken, base)
	watch := base
	watch.Scope, watch.OutcomeMode, watch.Outcomes = notifications.ScopeWatchedJobs, notifications.OutcomeSelected, []string{"cancelled"}
	watch.Jobs = []notifications.JobRef{{DeploymentID: labDeployment, NamespaceID: fixture.NamespaceID, JobID: fixture.Jobs[0].JobID}}
	aliceWatch := create(alice.accessToken, watch)
	mine := base
	mine.Scope, mine.OutcomeMode, mine.Outcomes = notifications.ScopeMyJobs, notifications.OutcomeSelected, []string{"cancelled"}
	aliceMine := create(alice.accessToken, mine)
	watch.Outcomes = []string{"success"}
	aliceOtherOutcome := create(alice.accessToken, watch)
	bobAll := create(bob.accessToken, base)
	bobMine := create(bob.accessToken, mine)
	request("GET", "/api/v1/rules/"+aliceAll.ID, bob.accessToken, "", nil, nil, 404)
	complete := func(selected string) labNotificationEvent {
		t.Helper()
		var event labNotificationEvent
		scenario(&event, "complete", receipt, selected)
		if !event.Synthetic || event.Receipt != receipt || event.Case != selected || event.DeploymentID != fixture.DeploymentID || event.ControlInstanceID != fixture.ControlInstanceID || event.RecoveryEpoch != fixture.RecoveryEpoch || event.NamespaceID != fixture.NamespaceID || !uuid(event.EventID) || event.Outcome != "cancelled" || event.RecordedAt.IsZero() {
			t.Fatal("Terminal event does not preserve exact prepared source identity")
		}
		var replay labNotificationEvent
		scenario(&replay, "complete", receipt, selected)
		if replay != event {
			t.Fatal("Idempotent terminal retry changed original event identity")
		}
		deadline := time.NewTimer(90 * time.Second)
		defer deadline.Stop()
		for {
			var barrier struct {
				Receipt string `json:"receipt"`
				Case    string `json:"case"`
				EventID string `json:"eventId"`
				Settled bool   `json:"settled"`
			}
			scenario(&barrier, "settled", receipt, selected)
			if barrier.Receipt != receipt || barrier.Case != selected || barrier.EventID != event.EventID {
				t.Fatal("Processing barrier referenced another source event")
			}
			if barrier.Settled {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("Durable processing context expired")
			case <-deadline.C:
				t.Fatal("Actual source publisher/ingestion/evaluation did not settle within bound")
			case <-time.After(2 * time.Second):
			}
		}
		var detail api.JobDetail
		request("GET", "/api/v1/deployments/"+labDeployment+"/namespaces/"+fixture.NamespaceID+"/jobs/"+event.JobID, alice.accessToken, "", nil, &detail, 200)
		if detail.Job.Phase != "terminal" || detail.Job.Outcome != event.Outcome || detail.Job.Revision != event.JobRevision || event.RunID != "" && (detail.Job.CurrentRun == nil || detail.Job.CurrentRun.ID != event.RunID || detail.Job.CurrentRun.ExecutionID != event.ExecutionID) || event.RunID == "" && detail.Job.CurrentRun != nil {
			t.Fatal("Actual source detail differs from original terminal observation")
		}
		return event
	}
	scopes, _ := json.Marshal([]notifications.NamespaceRef{ref})
	query := url.Values{"scope": {string(scopes)}, "limit": {"50"}}
	find := func(token string, event labNotificationEvent, unread bool) []notifications.InboxItem {
		t.Helper()
		params := url.Values{"scope": {query.Get("scope")}, "limit": {query.Get("limit")}}
		params.Set("unread", strconv.FormatBool(unread))
		found := []notifications.InboxItem{}
		for pageNo := 0; pageNo < 20; pageNo++ {
			var page notifications.InboxPage
			request("GET", "/api/v1/inbox?"+params.Encode(), token, "", nil, &page, 200)
			if page.Validate() != nil || page.Completeness != "complete" {
				t.Fatal("Current authorized inbox page is incomplete")
			}
			for _, item := range page.Items {
				if item.EventID == event.EventID {
					found = append(found, item)
				}
			}
			if page.NextCursor == "" {
				return found
			}
			params.Set("cursor", page.NextCursor)
		}
		t.Fatal("Lab inbox exceeds explicit 1000-item acceptance bound")
		return nil
	}
	first := complete("first")
	for _, test := range []struct {
		token string
		want  []string
		deny  []string
	}{{alice.accessToken, []string{aliceAll.ID, aliceWatch.ID, aliceMine.ID}, []string{aliceOtherOutcome.ID}}, {bob.accessToken, []string{bobAll.ID}, []string{bobMine.ID}}} {
		found := find(test.token, first, false)
		if len(found) != 1 {
			t.Fatal("Overlapping rules did not produce exactly one original-event inbox item")
		}
		item := found[0]
		if item.Job != (notifications.JobRef{DeploymentID: fixture.DeploymentID, NamespaceID: fixture.NamespaceID, JobID: first.JobID}) || item.ControlInstanceID != first.ControlInstanceID || item.Outcome != "cancelled" || !item.EventAt.Equal(first.RecordedAt) || item.Read || len(item.MatchedRules) != len(test.want) || item.Delivery.ByState["accepted"] != "" && item.Delivery.ByState["accepted"] != "0" {
			t.Fatal("Public inbox lost provenance or fabricated provider delivery")
		}
		for _, want := range test.want {
			if !slices.ContainsFunc(item.MatchedRules, func(m notifications.InboxMatch) bool { return m.RuleID == want }) {
				t.Fatal("Matching rule context absent")
			}
		}
		for _, deny := range test.deny {
			if slices.ContainsFunc(item.MatchedRules, func(m notifications.InboxMatch) bool { return m.RuleID == deny }) {
				t.Fatal("Owner or selected-outcome mismatch contributed an alert")
			}
		}
		other := alice.accessToken
		if test.token == other {
			other = bob.accessToken
		}
		path := "/api/v1/inbox/" + item.ID
		request("GET", path, other, "", nil, nil, 404)
		request("PATCH", path, other, "", map[string]bool{"read": true}, nil, 404)
		var changed notifications.InboxItem
		request("PATCH", path, test.token, "", map[string]bool{"read": true}, &changed, 200)
		if !changed.Read || changed.ReadAt == nil || len(find(test.token, first, true)) != 0 {
			t.Fatal("Read state did not update authoritative unread selection")
		}
		changed = notifications.InboxItem{}
		request("PATCH", path, test.token, "", map[string]bool{"read": false}, &changed, 200)
		if changed.Read || changed.ReadAt != nil || len(find(test.token, first, true)) != 1 {
			t.Fatal("Unread state could not be restored")
		}
	}
	for _, test := range []struct {
		token string
		rule  notifications.RuleView
	}{{alice.accessToken, aliceAll}, {alice.accessToken, aliceMine}, {bob.accessToken, bobAll}} {
		var stopped notifications.RuleView
		request("PUT", "/api/v1/rules/"+test.rule.ID+"/enabled", test.token, test.rule.Revision, map[string]bool{"enabled": false}, &stopped, 200)
		if stopped.Enabled || stopped.Revision == test.rule.Revision {
			t.Fatal("Stop did not advance durable rule intent")
		}
	}
	second := complete("stopped")
	if second.EventID == first.EventID || second.JobID == first.JobID || len(find(alice.accessToken, second, false)) != 0 || len(find(bob.accessToken, second, false)) != 0 {
		t.Fatal("Stopped intent or owner-mismatched rule produced a new alert after durable processing")
	}
	if len(find(alice.accessToken, first, false)) != 1 || len(find(bob.accessToken, first, false)) != 1 {
		t.Fatal("Stopping current intent erased retained authorized inbox history")
	}
	t.Log("PASS: normal synthetic Control cancellation -> original durable event -> actual service-only ingestion -> active rule matching -> one inbox/account, owner/outcome filters, read/unread, account isolation and immediate stop; no APNs provider or workload execution asserted")
}

type labBoundedNotificationOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *labBoundedNotificationOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8192 {
		b.overflow = true
		return 0, errors.New("synthetic helper output exceeded bound")
	}
	return b.Buffer.Write(p)
}
