//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

// No source restart or fixture mutation: the reviewed driver pauses and resumes
// only the exact primary process, with an independently armed guest watchdog.
func TestLabDependencyControl(t *testing.T) { labFaultRun(t, "control") }

type labControlCheck func(context.Context) error

// The limit covers ALL fault HTTP work, including healthy-scope and liveness
// probes. Every worker is joined before restoration; no request is detached from
// the caller's bounded fault context. Inputs are fixed by this harness.
func labControlChecks(ctx context.Context, checks []labControlCheck) error {
	if len(checks) == 0 || len(checks) > 32 {
		return errors.New("Control fault check count invalid")
	}
	work := make(chan int)
	results := make([]error, len(checks))
	var wg sync.WaitGroup
	for range min(4, len(checks)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				if ctx.Err() != nil {
					results[i] = errors.New("Control fault observation deadline elapsed")
					continue
				}
				call, cancel := context.WithTimeout(ctx, 10*time.Second)
				results[i] = checks[i](call)
				if results[i] == nil && call.Err() != nil {
					results[i] = errors.New("Control fault check completed after its deadline")
				}
				cancel()
			}
		}()
	}
	for i := range checks {
		work <- i
	}
	close(work)
	wg.Wait()
	return errors.Join(append(results, ctx.Err())...)
}

func labControlSources(sources []api.SourceStatus, primary, secondary api.Scope) error {
	if len(sources) != 2 {
		return errors.New("partial source contribution count differs")
	}
	seen := map[api.Scope]bool{}
	for _, source := range sources {
		if seen[source.Scope] || source.FetchedAt.IsZero() {
			return errors.New("partial source proof duplicated or missing")
		}
		seen[source.Scope] = true
		switch source.Scope {
		case primary:
			if source.Status != "authorization_unavailable" || source.AsOf != nil || source.Message != "This source contribution is unavailable. Refresh to retry." {
				return errors.New("paused primary contribution was not explicitly unavailable")
			}
		case secondary:
			if source.Status != "available" || source.AsOf == nil || source.AsOf.IsZero() || source.Message != "" {
				return errors.New("healthy secondary provenance unavailable")
			}
		default:
			return errors.New("unselected source contribution appeared")
		}
	}
	return nil
}

func labControlJobs(value, secondary api.Page[api.Job], primaryScope, secondaryScope api.Scope) error {
	if value.Completeness != "partial" || secondary.Completeness != "complete" || len(value.Items) == 0 || !reflect.DeepEqual(value.Items, secondary.Items) {
		return errors.New("partial jobs lost secondary facts or included unavailable primary rows")
	}
	for _, job := range value.Items {
		if job.Scope != secondaryScope {
			return errors.New("unavailable primary job bytes appeared")
		}
	}
	return labControlSources(value.Sources, primaryScope, secondaryScope)
}

func labControlOverview(value, secondary api.Overview, primaryScope, secondaryScope api.Scope) error {
	if value.Completeness != "partial" || secondary.Completeness != "complete" || value.Window != secondary.Window {
		return errors.New("partial overview or canonical window differs")
	}
	if err := labControlSources(value.Sources, primaryScope, secondaryScope); err != nil {
		return err
	}
	counts := func(v api.Overview) []*int64 {
		return []*int64{v.Active, v.AwaitingExecution, v.Running, v.EvidenceAttention, v.MissingCompletionTime}
	}
	for i, n := range counts(value) {
		if n == nil || counts(secondary)[i] == nil || *n != *counts(secondary)[i] {
			return errors.New("overview did not retain only the healthy subtotal")
		}
	}
	if len(value.Terminal) != 7 || !reflect.DeepEqual(value.Terminal, secondary.Terminal) {
		return errors.New("terminal overview includes another source or omits facts")
	}
	for _, key := range []string{"success", "failure", "cancelled", "timed_out", "aborted", "lost", "unknown"} {
		if value.Terminal[key] == nil {
			return errors.New("healthy terminal subtotal missing")
		}
	}
	return nil
}

func (s *labFaultState) control() {
	// Snapshot the same source facts using the same projection before/after. The
	// wrapper independently pins all source material, process and authority state.
	var original api.JobDetail
	s.must(s.alice.read(s.ctx, "control-baseline-original-job", s.jobPath, 200, "", &original))
	var secondary api.Page[api.Job]
	s.must(s.alice.read(s.ctx, "control-baseline-secondary-jobs", "/api/v1/jobs?"+labMultiQuery([]api.Scope{s.secondary}, 20, ""), 200, "", &secondary))
	if secondary.Completeness != "complete" || len(secondary.Items) == 0 || s.logs.NextCursor == "" {
		s.t.Fatal("healthy source facts and retained log cursor required")
	}
	window := api.Window{From: time.Now().UTC().Truncate(time.Microsecond).Add(-24 * time.Hour), To: time.Now().UTC().Truncate(time.Microsecond)}
	overviewQuery := func(scopes []api.Scope) string {
		return "/api/v1/overview?" + labMultiQuery(scopes, 0, "") + "&from=" + url.QueryEscape(window.From.Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(window.To.Format(time.RFC3339Nano))
	}
	var secondaryOverview api.Overview
	s.must(s.alice.read(s.ctx, "control-baseline-secondary-overview", overviewQuery([]api.Scope{s.secondary}), 200, "", &secondaryOverview))
	if secondaryOverview.Completeness != "complete" || secondaryOverview.Window != window {
		s.t.Fatal("healthy exact-window subtotal required")
	}
	s.begin("control_pause", 100*time.Second)
	began := time.Now()
	fault, cancel := context.WithTimeout(s.ctx, 25*time.Second)
	defer cancel()
	checks := []labControlCheck{}
	// Discovery in the monitoring engine is1.5s; its missing-current-authority
	// result is distinct from the direct Control adapter's transport failure.
	paths := []struct{ label, path, code string }{
		{"metadata", s.jobPath, "authorization_unavailable"},
		{"log", s.jobPath + "/logs?stream=stderr&cursor=" + url.QueryEscape(s.logs.NextCursor), "source_unavailable"},
		{"report", s.reportPath, "source_unavailable"},
		{"citation", s.citationPath, "source_unavailable"},
		{"inbox-item", "/api/v1/inbox/" + s.inbox.ID, "authorization_unavailable"},
		{"inbox-list", "/api/v1/inbox?" + labMultiQuery([]api.Scope{s.primary}, 20, ""), "authorization_unavailable"},
	}
	for identity, c := range map[string]labFaultHTTP{"native": s.alice, "web": s.web} {
		for _, route := range paths {
			checks = append(checks, func(ctx context.Context) error {
				return c.read(ctx, "control-paused-"+identity+"-"+route.label, route.path, 503, route.code, nil)
			})
		}
	}
	checks = append(checks, func(ctx context.Context) error {
		return s.bob.read(ctx, "control-paused-bob-primary", s.primaryJob, 503, "authorization_unavailable", nil)
	})
	for _, c := range []labFaultHTTP{s.alice, s.bob, s.web} {
		checks = append(checks, func(ctx context.Context) error {
			return c.read(ctx, "control-paused-healthy-secondary", s.secondaryJob, 200, "", nil)
		})
	}
	for _, c := range []labFaultHTTP{s.alice, s.web} {
		checks = append(checks, func(ctx context.Context) error {
			var page api.Page[api.Job]
			if err := c.read(ctx, "control-paused-partial-jobs", "/api/v1/jobs?"+labMultiQuery([]api.Scope{s.primary, s.secondary}, 20, ""), 200, "", &page); err != nil {
				return err
			}
			return labControlJobs(page, secondary, s.primary, s.secondary)
		}, func(ctx context.Context) error {
			var overview api.Overview
			if err := c.read(ctx, "control-paused-partial-overview", overviewQuery([]api.Scope{s.primary, s.secondary}), 200, "", &overview); err != nil {
				return err
			}
			return labControlOverview(overview, secondaryOverview, s.primary, s.secondary)
		})
	}
	checks = append(checks, func(ctx context.Context) error {
		return s.web.read(ctx, "control-paused-liveness", "/healthz", 200, "", nil)
	}, func(ctx context.Context) error {
		var value struct {
			Observations []struct {
				Endpoint, State string
				Status          int
				Milliseconds    float64
			}
		}
		if err := s.driver.call(ctx, "observe-api", "", &value); err != nil {
			return err
		}
		if len(value.Observations) != 2 || value.Observations[0].Endpoint != "livez" || value.Observations[0].State != "alive" || value.Observations[0].Status != 200 || value.Observations[1].Endpoint != "readyz" || value.Observations[1].State != "ready" || value.Observations[1].Status != 200 {
			return errors.New("process/SQL readiness incorrectly included paused external Control")
		}
		return nil
	})
	s.must(labControlChecks(fault, checks))
	// A timer-triggered early restoration cannot make healthy responses count as
	// fault evidence. This proof is local only; it does not query paused Control.
	var paused struct {
		Fault  string
		Paused bool
	}
	s.must(s.driver.call(fault, "status", "control_pause", &paused))
	if !paused.Paused || paused.Fault != "control_pause" || time.Since(began) > 25*time.Second {
		s.t.Fatal("exact Control did not remain paused for bounded observations")
	}
	cancel()
	s.recover()
	var restored api.JobDetail
	s.must(s.alice.read(s.ctx, "control-restored-original-job", s.jobPath, 200, "", &restored))
	if !reflect.DeepEqual(original.Job, restored.Job) {
		s.t.Fatal("Control pause changed original job facts")
	}
	for _, c := range []labFaultHTTP{s.alice, s.web} {
		var reread api.LogRange
		s.must(c.recoverLog(s.ctx, s.jobPath+"/logs?stream=stderr", &reread))
		var log labMixedLog
		s.must(log.accept(reread, labSlurmFailureLog, s.run))
		if reread.BytesBase64 != s.logs.BytesBase64 || reread.StartOffset != s.logs.StartOffset || reread.EndOffset != s.logs.EndOffset {
			s.t.Fatal("Control recovery changed original log range")
		}
		var follow api.LogRange
		s.must(c.read(s.ctx, "control-restored-retained-cursor", s.jobPath+"/logs?stream=stderr&cursor="+url.QueryEscape(s.logs.NextCursor), 200, "", &follow))
		s.must(log.accept(follow, labSlurmFailureLog, s.run))
		var item notifications.InboxItem
		s.must(c.read(s.ctx, "control-restored-inbox", "/api/v1/inbox/"+s.inbox.ID, 200, "", &item))
		if !reflect.DeepEqual(item, s.inbox) {
			s.t.Fatal("Control recovery changed original inbox identity, content or read state")
		}
	}
	// The shared finish checks same native identities/grants, same web session,
	// sealed report/citation bytes and final source/config/database preservation.
}

func TestLabControlChecksBoundsConcurrencyAndJoinsCancellation(t *testing.T) {
	var active, maximum, finished atomic.Int32
	started := make(chan struct{}, 4)
	checks := make([]labControlCheck, 24)
	for i := range checks {
		checks[i] = func(ctx context.Context) error {
			n := active.Add(1)
			defer active.Add(-1)
			defer finished.Add(1)
			for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
			}
			started <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	complete := make(chan error, 1)
	go func() { complete <- labControlChecks(ctx, checks) }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("four bounded workers did not start")
		}
	}
	cancel()
	if err := <-complete; err == nil || active.Load() != 0 || maximum.Load() != 4 || finished.Load() != 4 {
		t.Fatal("concurrency/cancellation bound or join failed")
	}
	if labControlChecks(t.Context(), nil) == nil || labControlChecks(t.Context(), make([]labControlCheck, 33)) == nil {
		t.Fatal("unbounded request plan admitted")
	}
}

func TestLabControlChecksRejectsSuccessfulReturnAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if labControlChecks(ctx, []labControlCheck{func(context.Context) error { cancel(); return nil }}) == nil {
		t.Fatal("late success hid cancelled fault observation")
	}
}

func TestLabControlPartialProofRejectsPrimaryBytesAndHiddenSubtotals(t *testing.T) {
	now := time.Now().UTC()
	primary, secondary := api.Scope{DeploymentID: "primary", NamespaceID: "research"}, api.Scope{DeploymentID: "secondary", NamespaceID: "research"}
	sources := []api.SourceStatus{{Scope: primary, Status: "authorization_unavailable", FetchedAt: now, Message: "This source contribution is unavailable. Refresh to retry."}, {Scope: secondary, Status: "available", FetchedAt: now, AsOf: &now}}
	page := api.Page[api.Job]{Items: []api.Job{{Scope: secondary, ID: "secondary-fact"}}, Sources: sources, Completeness: "partial"}
	baseline := page
	baseline.Completeness = "complete"
	if labControlJobs(page, baseline, primary, secondary) != nil {
		t.Fatal("truthful partial page rejected")
	}
	clone := func() api.Page[api.Job] {
		raw, _ := json.Marshal(page)
		var v api.Page[api.Job]
		_ = json.Unmarshal(raw, &v)
		return v
	}
	for _, change := range []func(*api.Page[api.Job]){
		func(v *api.Page[api.Job]) { v.Items[0].Scope = primary }, func(v *api.Page[api.Job]) { v.Items = nil }, func(v *api.Page[api.Job]) { v.Sources = v.Sources[1:] },
		func(v *api.Page[api.Job]) { v.Sources[0].Status = "available" }, func(v *api.Page[api.Job]) { v.Sources[0].AsOf = &now }, func(v *api.Page[api.Job]) { v.Sources[1] = v.Sources[0] },
	} {
		bad := clone()
		change(&bad)
		if labControlJobs(bad, baseline, primary, secondary) == nil {
			t.Fatal("unavailable rows or hidden provenance accepted")
		}
	}
	n := int64(3)
	terminal := map[string]*int64{}
	for _, k := range []string{"success", "failure", "cancelled", "timed_out", "aborted", "lost", "unknown"} {
		v := int64(1)
		terminal[k] = &v
	}
	overview := api.Overview{Completeness: "partial", Sources: sources, Window: api.Window{From: now.Add(-time.Hour), To: now}, Active: &n, AwaitingExecution: &n, Running: &n, EvidenceAttention: &n, MissingCompletionTime: &n, Terminal: terminal}
	reference := overview
	reference.Completeness = "complete"
	if labControlOverview(overview, reference, primary, secondary) != nil {
		t.Fatal("healthy exact subtotal rejected")
	}
	for _, change := range []func(*api.Overview){func(v *api.Overview) { extra := int64(4); v.Active = &extra }, func(v *api.Overview) { v.Active = nil }, func(v *api.Overview) { v.Window.To = v.Window.To.Add(time.Microsecond) }, func(v *api.Overview) { v.Completeness = "complete" }, func(v *api.Overview) { delete(v.Terminal, "unknown") }} {
		raw, _ := json.Marshal(overview)
		var bad api.Overview
		_ = json.Unmarshal(raw, &bad)
		change(&bad)
		if labControlOverview(bad, reference, primary, secondary) == nil {
			t.Fatal("invented/hidden totals or window accepted")
		}
	}
}

func TestLabFaultCommandFiniteDiagnosticsWithRealSubprocess(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python required for local subprocess regression")
	}
	line := "Dependency fault phase stopped (host_file); preserve receipts; do not repeat an uncertain begin.\n"
	cases := []struct{ name, program, want string }{
		{"success", "import sys;sys.stdout.write('{\"ok\":true}')", ""},
		{"finite", fmt.Sprintf("import sys;sys.stderr.write(%q);sys.exit(1)", line), "phase=begin category=exit code=host_file"},
		{"receipt identity", fmt.Sprintf("import sys;sys.stderr.write(%q);sys.exit(1)", strings.Replace(line, "host_file", "operation_identity", 1)), "phase=begin category=exit code=operation_identity"},
		{"hostile", "import sys;sys.stderr.write('private-password\\n');sys.exit(1)", "category=exit code=withheld"},
		{"unknown finite", fmt.Sprintf("import sys;sys.stderr.write(%q);sys.exit(1)", strings.Replace(line, "host_file", "private_secret", 1)), "category=exit code=withheld"},
		{"extra line", fmt.Sprintf("import sys;sys.stderr.write(%q);sys.exit(1)", line+"private-secret\n"), "category=exit code=withheld"},
		{"json", "print('{\"private-secret\":')", "category=json code=withheld"},
		{"unexpected stderr", "import sys;print('{}');sys.stderr.write('private-secret')", "category=unexpected_stderr code=withheld"},
		{"stdout bound", "print('private-secret'*4000)", "category=output_bound code=withheld"},
		{"stderr bound", "import sys;sys.stderr.write('private-secret'*6000);sys.exit(1)", "category=output_bound code=withheld"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			var result map[string]bool
			err := labFaultCommand(ctx, exec.CommandContext(ctx, "python3", "-c", tt.program), "begin", &result)
			if tt.want == "" {
				if err != nil || !result["ok"] {
					t.Fatal("valid subprocess result rejected")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "private_") {
				t.Fatal("finite diagnostics leaked or misclassified child output")
			}
		})
	}
}

func TestLabFaultCommandDeadlineAndInvalidPhase(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python required for local subprocess regression")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	began := time.Now()
	var result any
	err := labFaultCommand(ctx, exec.CommandContext(ctx, "python3", "-c", "import time;time.sleep(10)"), "status", &result)
	if err == nil || !strings.Contains(err.Error(), "phase=status category=deadline code=withheld") || time.Since(began) > time.Second {
		t.Fatal("bounded child deadline diagnostics differ")
	}
	command := exec.CommandContext(t.Context(), "python3", "-c", "raise AssertionError('must not start')")
	if labFaultCommand(t.Context(), command, "private-phase", &result) == nil || command.Process != nil {
		t.Fatal("unknown phase started subprocess")
	}
	var sink labFaultOutput
	sink.limit = 4
	_, _ = sink.Write([]byte("123456"))
	_, _ = sink.Write([]byte("789"))
	if !sink.overflow || !bytes.Equal(sink.buffer.Bytes(), []byte("1234")) {
		t.Fatal("bounded diagnostic pipe retained excess data")
	}
}

func TestLabControlSensitiveErrorsNeverReturnSuccessOrCredentials(t *testing.T) {
	for _, code := range []string{"source_unavailable", "authorization_unavailable"} {
		for _, status := range []int{200, 401, 403, 503} {
			t.Run(fmt.Sprintf("%s-%d", code, status), func(t *testing.T) {
				message := "The source is unavailable. Retry when private connectivity is restored."
				if code == "authorization_unavailable" {
					message = "Current source authorization could not be verified."
				}
				raw, _ := json.Marshal(api.Error{Code: code, Message: message, RequestID: strings.Repeat("a", 32)})
				var mu sync.Mutex
				samples := []labFaultSample{}
				client := &http.Client{Transport: labMixedRoundTripper(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: status, Header: http.Header{"Cache-Control": {"no-store"}}, Body: io.NopCloser(bytes.NewReader(raw))}, nil
				})}
				c := labFaultHTTP{client: client, mu: &mu, samples: &samples}
				if err := c.read(t.Context(), "paused-fixture", "/api/v1/private-fixture", 503, code, nil); (err == nil) != (status == 503) {
					t.Fatal("source fault admitted success, authentication denial or permission removal")
				}
			})
		}
	}
}
