//go:build integration

package auth

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

// This harness measures ordinary authenticated HTTPS and actual delegated
// Control reads. Seeding is a separate reviewed operator action. It performs
// no workload submission, source reconfiguration, or invented LDAP proof.
// The 100,000 retained records are explicitly imported synthetic metadata;
// HTTP timings do not establish client rendering or corporate AD FS latency.
type labScaleUser struct {
	Username    string `json:"username"`
	DirectoryID string `json:"directoryId"`
	Subject     string `json:"subject"`
}

type labScaleNamespace struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ActiveJobID     string `json:"activeJobId"`
	ImportedJobID   string `json:"importedJobId"`
	ActiveJobs      int    `json:"activeJobs"`
	ImportedHistory int    `json:"importedHistory"`
}

type labScaleSource struct {
	DeploymentID  string              `json:"deploymentId"`
	InstanceID    string              `json:"instanceId"`
	RecoveryEpoch string              `json:"recoveryEpoch"`
	CASHA256      string              `json:"caSHA256"`
	Namespaces    []labScaleNamespace `json:"namespaces"`
}

type labScaleFixture struct {
	Version    int              `json:"version"`
	Synthetic  bool             `json:"synthetic"`
	Mode       string           `json:"mode"`
	PreparedAt time.Time        `json:"preparedAt"`
	HistoryAt  time.Time        `json:"historyAt"`
	Users      []labScaleUser   `json:"users"`
	Sources    []labScaleSource `json:"sources"`
}

func (f labScaleFixture) validate() error {
	if f.Version != 1 || !f.Synthetic || f.Mode != "imported-history-no-execution" || len(f.Users) != 25 || len(f.Sources) != 2 || f.PreparedAt.IsZero() || f.HistoryAt.IsZero() || !f.HistoryAt.Before(f.PreparedAt) {
		return errors.New("bounded synthetic scale fixture required")
	}
	seen := map[string]bool{}
	for i, user := range f.Users {
		if user.Username != fmt.Sprintf("scale%02d", i+1) || user.DirectoryID != fmt.Sprintf("74000000-0000-4000-8000-%012d", i+1) || !uuid(user.Subject) || seen[user.Subject] {
			return errors.New("25 distinct immutable synthetic sign-in identities required")
		}
		seen[user.Subject] = true
	}
	seen = map[string]bool{}
	for i, source := range f.Sources {
		count := 10
		if i == 1 {
			count = 5
		}
		deployment := fmt.Sprintf("72000000-0000-4000-8000-%012d", i+1)
		caDigest, err := hex.DecodeString(source.CASHA256)
		if source.DeploymentID != deployment || !uuid(source.InstanceID) || source.RecoveryEpoch != "1" || len(source.Namespaces) != count || seen[source.InstanceID] || err != nil || len(caDigest) != 32 {
			return errors.New("independent pinned scale sources required")
		}
		seen[source.InstanceID] = true
		for n, ns := range source.Namespaces {
			if !uuid(ns.ID) || !uuid(ns.ActiveJobID) || !uuid(ns.ImportedJobID) || ns.ActiveJobID == ns.ImportedJobID || ns.Name != fmt.Sprintf("dashboard-scale-%02d", n+1) || ns.ActiveJobs != 50 || ns.ImportedHistory != 10000 || seen[deployment+ns.ID] {
				return errors.New("namespace retained-history or active-workload envelope differs")
			}
			seen[deployment+ns.ID] = true
		}
	}
	return nil
}

func labScaleReadFile(path string, maximum int64) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, errors.New("scale input must be a real absolute file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("scale input unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("scale input exceeds regular-file bound")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil, errors.New("scale input changed or read failed")
	}
	return raw, nil
}

func labScaleSourceIdentity(ctx context.Context, root string, source labScaleSource) error {
	port, caName := "18443", "control-primary-ca.crt"
	if source.DeploymentID != labDeployment {
		port, caName = "28443", "control-secondary-ca.crt"
	}
	raw, err := labScaleReadFile(filepath.Join(root, ".lab/dashboard/scale", caName), 65536)
	sum := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(sum[:]) != source.CASHA256 {
		return errors.New("scale source public CA differs from receipt")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return errors.New("invalid scale source public CA")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 32 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://10.77.0.21:"+port+"/v1/capabilities", nil)
	response, err := client.Do(req)
	if err != nil {
		return errors.New("scale source verified TLS request failed")
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	var envelope struct {
		Capabilities struct {
			InstanceID    string `json:"instanceId"`
			RecoveryEpoch string `json:"recoveryEpoch"`
		} `json:"capabilities"`
	}
	if err != nil || len(raw) > 64<<10 || response.StatusCode != 200 || json.Unmarshal(raw, &envelope) != nil || envelope.Capabilities.InstanceID != source.InstanceID || envelope.Capabilities.RecoveryEpoch != source.RecoveryEpoch {
		return errors.New("live scale source instance/epoch differs")
	}
	return nil
}

type labScaleReader struct {
	client *http.Client
	token  string
}

func newLabScaleReader(t *testing.T, session labNativeSession) labScaleReader {
	t.Helper()
	transport := session.transport.Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "dashboard.lab.test:8443" {
			return nil, ErrUnauthenticated
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "10.77.0.10:8443")
	}
	t.Cleanup(transport.CloseIdleConnections)
	return labScaleReader{client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, token: session.accessToken}
}

func (r labScaleReader) read(ctx context.Context, path string, target any) (int, int, error) {
	if !strings.HasPrefix(path, "/api/v1/") || strings.ContainsAny(path, "\r\n") {
		return 0, 0, errors.New("invalid bounded scale endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://dashboard.lab.test:8443"+path, nil)
	if err != nil {
		return 0, 0, errors.New("invalid scale request")
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	response, err := r.client.Do(req)
	if err != nil {
		return 0, 0, errors.New("scale HTTPS request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return response.StatusCode, len(raw), errors.New("scale response exceeds bound or is incomplete")
	}
	if response.StatusCode != 200 {
		// Neither arbitrary remote errors nor identity tokens enter test output.
		return response.StatusCode, len(raw), fmt.Errorf("scale response HTTP%d", response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		return response.StatusCode, len(raw), errors.New("scale response did not prohibit caching")
	}
	if json.Unmarshal(raw, target) != nil {
		return response.StatusCode, len(raw), errors.New("scale response differs from contract")
	}
	return response.StatusCode, len(raw), nil
}

func labScaleScopes(f labScaleFixture, twoSources bool) ([]api.Scope, []labScaleNamespace) {
	scopes := []api.Scope{}
	namespaces := []labScaleNamespace{}
	for i, source := range f.Sources {
		if i == 1 && !twoSources {
			break
		}
		count := len(source.Namespaces)
		if twoSources {
			count = 5
		}
		for _, ns := range source.Namespaces[:count] {
			scopes = append(scopes, api.Scope{DeploymentID: source.DeploymentID, NamespaceID: ns.ID})
			namespaces = append(namespaces, ns)
		}
	}
	return scopes, namespaces
}

func labScaleQuery(scopes []api.Scope) url.Values {
	raw, _ := json.Marshal(scopes)
	return url.Values{"scope": {string(raw)}, "limit": {"50"}}
}

func labScaleSourceStatus(sources []api.SourceStatus, scopes []api.Scope) bool {
	if len(sources) != len(scopes) {
		return false
	}
	seen := map[api.Scope]bool{}
	for _, source := range sources {
		if !slices.Contains(scopes, source.Scope) || seen[source.Scope] || source.Status != "available" || source.AsOf == nil || source.AsOf.IsZero() || source.FetchedAt.IsZero() {
			return false
		}
		seen[source.Scope] = true
	}
	return true
}

func labScalePage(page api.Page[api.Job], scopes []api.Scope, want int) error {
	if len(page.Items) != want || page.NextCursor == "" || page.Completeness != "complete" || page.FetchedAt.IsZero() || !labScaleSourceStatus(page.Sources, scopes) {
		return errors.New("scale list is incomplete, stale or outside bounds")
	}
	seen := map[string]bool{}
	for i, job := range page.Items {
		key := job.DeploymentID + "/" + job.NamespaceID + "/" + job.ID
		if !slices.Contains(scopes, job.Scope) || !uuid(job.ID) || seen[key] || job.CreatedAt.IsZero() || i > 0 && job.CreatedAt.After(page.Items[i-1].CreatedAt) {
			return errors.New("scale list duplicated, reordered or crossed scope")
		}
		seen[key] = true
	}
	return nil
}

type labScaleSample struct {
	Operation string
	Duration  time.Duration
	Bytes     int
	Err       error
}

type labScaleStatistic struct {
	Samples    int     `json:"samples"`
	Errors     int     `json:"errors"`
	P50MS      float64 `json:"p50Ms"`
	P95MS      float64 `json:"p95Ms"`
	MaxMS      float64 `json:"maxMs"`
	MaxBytes   int     `json:"maxBytes"`
	FirstError string  `json:"firstError,omitempty"`
}

func labScaleStatistics(samples []labScaleSample) map[string]labScaleStatistic {
	byOperation := map[string][]labScaleSample{}
	for _, sample := range samples {
		byOperation[sample.Operation] = append(byOperation[sample.Operation], sample)
	}
	result := map[string]labScaleStatistic{}
	for operation, values := range byOperation {
		slices.SortFunc(values, func(a, b labScaleSample) int { return cmp.Compare(a.Duration, b.Duration) })
		stat := labScaleStatistic{Samples: len(values), P50MS: float64(values[(len(values)+1)/2-1].Duration) / float64(time.Millisecond), P95MS: float64(values[(len(values)*95+99)/100-1].Duration) / float64(time.Millisecond), MaxMS: float64(values[len(values)-1].Duration) / float64(time.Millisecond)}
		for _, v := range values {
			stat.MaxBytes = max(stat.MaxBytes, v.Bytes)
			if v.Err != nil {
				stat.Errors++
				if stat.FirstError == "" {
					stat.FirstError = v.Err.Error()
				}
			}
		}
		result[operation] = stat
	}
	return result
}

func TestLabDeployedAcceptedScale(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_SCALE") != "1" {
		t.Skip("explicit reviewed deployed scale fixture opt-in required")
	}
	root, output := os.Getenv("JOBMAN_DASHBOARD_LAB_ROOT"), os.Getenv("JOBMAN_DASHBOARD_LAB_SCALE_RESULT")
	if !filepath.IsAbs(root) || !filepath.IsAbs(output) {
		t.Fatal("absolute Lab root and unique public result path required")
	}
	raw, err := labScaleReadFile(filepath.Join(root, ".lab/dashboard/scale-fixture.json"), 128<<10)
	var fixture labScaleFixture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || decoder.Decode(&fixture) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) || fixture.validate() != nil {
		t.Fatal("reviewed bounded public scale fixture is unavailable")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil || parent != filepath.Dir(output) || filepath.Clean(output) != output {
		t.Fatal("scale result requires a real parent directory")
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal("result path must be new; preserve earlier acceptance evidence")
	}
	digest := sha256.Sum256(raw)
	result := struct {
		FixtureSHA256 string                                  `json:"fixtureSHA256"`
		StartedAt     time.Time                               `json:"startedAt"`
		CompletedAt   time.Time                               `json:"completedAt"`
		Passed        bool                                    `json:"passed"`
		Viewers       int                                     `json:"distinctSignedInViewers"`
		Notes         string                                  `json:"notes"`
		Modes         map[string]map[string]labScaleStatistic `json:"modes"`
		TraversedJobs map[string]int                          `json:"traversedJobs"`
	}{FixtureSHA256: hex.EncodeToString(digest[:]), StartedAt: time.Now().UTC(), Notes: "Deployed authenticated HTTPS only; imported synthetic history; identity-provider latency excluded; browser/iPhone rendering and actual AD FS not measured.", Modes: map[string]map[string]labScaleStatistic{}, TraversedJobs: map[string]int{}}
	defer func() {
		result.CompletedAt = time.Now().UTC()
		result.Passed = !t.Failed() && len(result.Modes) == 2 && len(result.TraversedJobs) == 2
		if json.NewEncoder(file).Encode(result) != nil || file.Sync() != nil {
			t.Error("could not persist bounded public scale result")
		}
		if file.Close() != nil {
			t.Error("could not close scale result")
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	checkSources := func() {
		t.Helper()
		for _, source := range fixture.Sources {
			if err := labScaleSourceIdentity(ctx, root, source); err != nil {
				t.Fatal(err)
			}
		}
	}
	checkSources()
	readers := make([]labScaleReader, 0, 25)
	accounts := map[string]bool{}
	for _, user := range fixture.Users {
		session := labNativeSignIn(t, user.Username, user.DirectoryID)
		if session.subject != user.Subject {
			t.Fatal("signed scale subject differs from the directory fixture")
		}
		reader := newLabScaleReader(t, session)
		var bootstrap api.Bootstrap
		if _, _, err = reader.read(ctx, "/api/v1/bootstrap", &bootstrap); err != nil || bootstrap.FixtureMode || bootstrap.APIVersion != api.Version || bootstrap.Account.ID == "" || accounts[bootstrap.Account.ID] || bootstrap.Completeness != "complete" {
			t.Fatal("distinct real current scale account bootstrap failed")
		}
		accounts[bootstrap.Account.ID] = true
		for _, source := range fixture.Sources {
			deploymentIndex := slices.IndexFunc(bootstrap.Deployments, func(d api.Deployment) bool { return d.ID == source.DeploymentID })
			if deploymentIndex < 0 {
				t.Fatal("scale deployment is not authorized")
			}
			for _, ns := range source.Namespaces {
				index := slices.IndexFunc(bootstrap.Deployments[deploymentIndex].Namespaces, func(n api.Namespace) bool {
					return n.ID == ns.ID && n.Name == ns.Name && slices.Equal(n.Roles, []string{"viewer"}) && n.AuthorizationExpiresAt.After(time.Now())
				})
				if index < 0 {
					t.Fatal("scale viewer lacks a fresh direct-directory read grant")
				}
			}
		}
		readers = append(readers, reader)
		result.Viewers = len(readers)
	}
	for _, mode := range []string{"one-control", "two-controls"} {
		checkSources()
		scopes, namespaces := labScaleScopes(fixture, mode == "two-controls")
		query := labScaleQuery(scopes)
		summaryQuery := labScaleQuery(scopes)
		summaryQuery.Del("limit")
		summaryQuery.Set("completedFrom", fixture.HistoryAt.Add(-time.Second).Format(time.RFC3339Nano))
		summaryQuery.Set("completedTo", fixture.PreparedAt.Add(time.Second).Format(time.RFC3339Nano))
		start := make(chan struct{})
		samples := make(chan labScaleSample, 25*8*3)
		var group sync.WaitGroup
		for viewer, reader := range readers {
			group.Go(func() {
				<-start
				for iteration := range 8 {
					began := time.Now()
					var page api.Page[api.Job]
					_, size, readErr := reader.read(ctx, "/api/v1/jobs?"+query.Encode(), &page)
					if readErr == nil {
						readErr = labScalePage(page, scopes, 50)
					}
					samples <- labScaleSample{"list", time.Since(began), size, readErr}
					i := (viewer + iteration) % len(scopes)
					jobID := namespaces[i].ActiveJobID
					imported := iteration%2 != 0
					if imported {
						jobID = namespaces[i].ImportedJobID
					}
					began = time.Now()
					var detail api.JobDetail
					_, size, readErr = reader.read(ctx, "/api/v1/deployments/"+scopes[i].DeploymentID+"/namespaces/"+scopes[i].NamespaceID+"/jobs/"+jobID, &detail)
					if readErr == nil && (detail.Job.ID != jobID || detail.Job.Scope != scopes[i] || detail.Job.Imported != imported || detail.FetchedAt.IsZero()) {
						readErr = errors.New("scale detail crossed source identity or imported provenance")
					}
					samples <- labScaleSample{"detail", time.Since(began), size, readErr}
					began = time.Now()
					var overview api.Overview
					_, size, readErr = reader.read(ctx, "/api/v1/overview?"+summaryQuery.Encode(), &overview)
					if readErr == nil && (overview.Completeness != "complete" || !labScaleSourceStatus(overview.Sources, scopes) || overview.Active == nil || *overview.Active != 500 || overview.AwaitingExecution == nil || *overview.AwaitingExecution != 500 || overview.Terminal["success"] == nil || *overview.Terminal["success"] != 100000 || overview.MissingCompletionTime == nil || *overview.MissingCompletionTime != 0) {
						readErr = errors.New("scale aggregate does not account for the full accepted dataset")
					}
					samples <- labScaleSample{"overview", time.Since(began), size, readErr}
				}
			})
		}
		close(start)
		group.Wait()
		close(samples)
		all := make([]labScaleSample, 0, 600)
		for sample := range samples {
			all = append(all, sample)
		}
		statistics := labScaleStatistics(all)
		result.Modes[mode] = statistics
		for _, operation := range []string{"list", "detail", "overview"} {
			stat := statistics[operation]
			t.Logf("%s %s: viewers=25 scopes=10 retained=100000 active=500 samples=%d errors=%d p50Ms=%.3f p95Ms=%.3f maxMs=%.3f maxBytes=%d", mode, operation, stat.Samples, stat.Errors, stat.P50MS, stat.P95MS, stat.MaxMS, stat.MaxBytes)
			if stat.Samples != 200 || stat.Errors != 0 || stat.P95MS > 2000 {
				t.Errorf("%s %s failed the complete HTTP response/2s p95 budget: %s", mode, operation, stat.FirstError)
			}
		}
		checkSources()
		if t.Failed() {
			// Do not keep applying load after a demonstrated failure. The partial
			// result remains available without tokens or source response payloads.
			return
		}
	}
	// Exercise the Dashboard cursor across a complete retained namespace in
	// each real source. These sequential correctness reads are outside the
	// latency sample, with independent account/cursor boundaries per source.
	for sourceIndex, source := range fixture.Sources {
		ns := source.Namespaces[0]
		scope := api.Scope{DeploymentID: source.DeploymentID, NamespaceID: ns.ID}
		query := labScaleQuery([]api.Scope{scope})
		query.Set("limit", "200")
		seen := map[string]bool{}
		cursor := ""
		var previous *api.Job
		imported, active := 0, 0
		for pageIndex := range 51 {
			if cursor != "" {
				query.Set("cursor", cursor)
			}
			var page api.Page[api.Job]
			if _, _, err := readers[sourceIndex].read(ctx, "/api/v1/jobs?"+query.Encode(), &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Items) < 1 || len(page.Items) > 200 || page.Completeness != "complete" || page.FetchedAt.IsZero() || !labScaleSourceStatus(page.Sources, []api.Scope{scope}) {
				t.Fatal("full retained-history page is incomplete or unbounded")
			}
			for _, job := range page.Items {
				if !uuid(job.ID) || seen[job.ID] || job.Scope != scope || job.CreatedAt.IsZero() || previous != nil && (job.CreatedAt.After(previous.CreatedAt) || job.CreatedAt.Equal(previous.CreatedAt) && job.ID >= previous.ID) {
					t.Fatal("full retained-history cursor duplicated, reordered or crossed scope")
				}
				seen[job.ID] = true
				if job.Imported && job.Phase == "terminal" && job.Outcome == "success" && job.CurrentRun == nil {
					imported++
				} else if !job.Imported && job.Phase == "accepted" && job.CurrentRun == nil {
					active++
				} else {
					t.Fatal("history fixture invented a run or changed pending/imported provenance")
				}
				copy := job
				previous = &copy
			}
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
			if pageIndex == 50 {
				t.Fatal("retained-history cursor exceeded its declared dataset")
			}
		}
		if cursor != "" || len(seen) != 10050 || imported != 10000 || active != 50 || !seen[ns.ActiveJobID] || !seen[ns.ImportedJobID] {
			t.Fatal("complete retained-history traversal did not account for all declared jobs")
		}
		result.TraversedJobs[source.DeploymentID] = len(seen)
	}
	checkSources()
}

func TestLabScaleGuardsAndStatistics(t *testing.T) {
	fixture := labScaleFixture{Version: 1, Synthetic: true, Mode: "imported-history-no-execution", PreparedAt: time.Now(), HistoryAt: time.Now().Add(-time.Hour)}
	for i := range 25 {
		fixture.Users = append(fixture.Users, labScaleUser{Username: fmt.Sprintf("scale%02d", i+1), DirectoryID: fmt.Sprintf("74000000-0000-4000-8000-%012d", i+1), Subject: fmt.Sprintf("75000000-0000-4000-8000-%012d", i+1)})
	}
	for i, count := range []int{10, 5} {
		source := labScaleSource{DeploymentID: fmt.Sprintf("72000000-0000-4000-8000-%012d", i+1), InstanceID: fmt.Sprintf("76000000-0000-4000-8000-%012d", i+1), RecoveryEpoch: "1", CASHA256: strings.Repeat("a", 64)}
		for n := range count {
			source.Namespaces = append(source.Namespaces, labScaleNamespace{ID: fmt.Sprintf("77000000-0000-4000-8000-%012d", n+1), Name: fmt.Sprintf("dashboard-scale-%02d", n+1), ActiveJobID: fmt.Sprintf("78000000-0000-4000-8000-%012d", n+1), ImportedJobID: fmt.Sprintf("79000000-0000-4000-8000-%012d", n+1), ActiveJobs: 50, ImportedHistory: 10000})
		}
		fixture.Sources = append(fixture.Sources, source)
	}
	if fixture.validate() != nil {
		t.Fatal("valid isolated source-qualified fixture rejected")
	}
	for _, two := range []bool{false, true} {
		scopes, namespaces := labScaleScopes(fixture, two)
		if len(scopes) != 10 || len(namespaces) != 10 || (scopes[9].DeploymentID != scopes[0].DeploymentID) != two {
			t.Fatal("source-qualified accepted-scale selection differs")
		}
	}
	fixture.Users[24].Subject = fixture.Users[0].Subject
	if fixture.validate() == nil {
		t.Fatal("duplicate viewer was accepted")
	}
	samples := make([]labScaleSample, 20)
	for i := range samples {
		samples[i] = labScaleSample{Operation: "list", Duration: time.Duration(i+1) * time.Millisecond, Bytes: i + 100}
	}
	samples[19].Err = errors.New("synthetic failure must stay in percentile denominator")
	stat := labScaleStatistics(samples)["list"]
	if stat.Samples != 20 || stat.Errors != 1 || stat.P50MS != 10 || stat.P95MS != 19 || stat.MaxMS != 20 || stat.MaxBytes != 119 {
		t.Fatal("percentile/count accounting excluded failed requests")
	}
}
