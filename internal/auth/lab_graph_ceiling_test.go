//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type labCeilingManifest struct {
	Version            int    `json:"version"`
	Synthetic          bool   `json:"synthetic"`
	Mode               string `json:"mode"`
	DeploymentID       string `json:"deploymentId"`
	InstanceID         string `json:"instanceId"`
	RecoveryEpoch      string `json:"recoveryEpoch"`
	NamespaceID        string `json:"namespaceId"`
	Namespace          string `json:"namespace"`
	TargetID           string `json:"targetId"`
	TargetGenerationID string `json:"targetGenerationId"`
	GraphID            string `json:"graphId"`
	Revision           string `json:"revision"`
	TotalNodes         string `json:"totalNodes"`
	TotalEdges         string `json:"totalEdges"`
	RequestDigest      string `json:"requestDigest"`
	Nodes              []struct {
		Index int    `json:"index"`
		ID    string `json:"id"`
	} `json:"nodes"`
}

func labCeilingDecode(raw []byte, expected string) (labCeilingManifest, error) {
	var m labCeilingManifest
	sum := sha256.Sum256(raw)
	if len(raw) == 0 || len(raw) > 2<<20 || hex.EncodeToString(sum[:]) != expected {
		return m, errors.New("reviewed graph manifest digest or size differs")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, errors.New("graph manifest shape differs")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return m, errors.New("graph manifest has trailing content")
	}
	if m.Version != 1 || !m.Synthetic || m.Mode != "admitted-no-execution" || m.DeploymentID != labDeployment || m.InstanceID != "e633cf92-258d-48ff-965a-fda88d68ef3a" || m.RecoveryEpoch != "1" || m.Namespace != "dashboard-operations" || m.Revision != "1" || m.TotalNodes != "10000" || m.TotalEdges != "100000" || len(m.Nodes) != 10000 {
		return m, errors.New("fixed synthetic graph source or bounds differ")
	}
	if !uuid(m.NamespaceID) || !uuid(m.TargetID) || !uuid(m.TargetGenerationID) || !uuid(m.GraphID) || len(m.RequestDigest) != 71 || m.RequestDigest[:7] != "sha256:" {
		return m, errors.New("graph source identity or digest is invalid")
	}
	if digest, err := hex.DecodeString(m.RequestDigest[7:]); err != nil || len(digest) != 32 {
		return m, errors.New("graph request digest is invalid")
	}
	seen := map[string]bool{}
	for i, n := range m.Nodes {
		if n.Index != i || !uuid(n.ID) || seen[n.ID] {
			return m, errors.New("graph node mapping differs")
		}
		seen[n.ID] = true
	}
	return m, nil
}
func (m labCeilingManifest) scope() api.Scope {
	return api.Scope{DeploymentID: m.DeploymentID, NamespaceID: m.NamespaceID}
}
func (m labCeilingManifest) path() string {
	return labMultiPrefix(m.scope()) + "/workloads/graph/" + m.GraphID
}

// This independent integer relation is shared only by the test's assertions;
// neither source helper nor client fixture code is imported by this harness.
func labCeilingPair(from, to int) bool {
	return from >= 0 && to >= 0 && from < 10000 && to < 10000 && (from == 0 && to > 0 || from >= 1 && from <= 9000 && to > from && to <= from+10 || from == 9001 && to == 9002)
}
func labCeilingIncoming(index int) int {
	if index == 0 {
		return 0
	}
	total := 1
	low, high := max(1, index-10), min(9000, index-1)
	if high >= low {
		total += high - low + 1
	}
	if index == 9002 {
		total++
	}
	return total
}
func labCeilingChild(m labCeilingManifest, n api.WorkloadChild, index int) bool {
	if index < 0 || index >= len(m.Nodes) {
		return false
	}
	j := n.Job
	want := m.Nodes[index].ID
	name := fmt.Sprintf("node-%05d", index)
	incoming := strconv.Itoa(labCeilingIncoming(index))
	return n.ID == want && n.Index == strconv.Itoa(index) && n.Name == name && n.Disposition == "" && n.TaskIndex == "" &&
		reflect.DeepEqual(n.DependencyCounts, map[string]string{"total": incoming, "waiting": incoming, "satisfied": "0", "unsatisfied": "0"}) &&
		j.ID == want && j.Scope == m.scope() && j.Name == name && j.TargetID == m.TargetID && j.TargetGenerationID == m.TargetGenerationID && j.Backend == "subprocess" &&
		j.Revision == "1" && j.DesiredState == "run" && j.Phase == "accepted" && j.Outcome == "" && j.Confidence == "" && j.Disposition == "" && !j.Imported && j.CurrentRun == nil && j.StartedAt == nil && j.CompletedAt == nil && j.Scheduler == nil &&
		j.Owner != nil && uuid(j.Owner.ID) && j.Owner.IsCurrentUser && j.Group != nil && j.Group.GraphID == m.GraphID && j.Group.GraphIndex != nil && *j.Group.GraphIndex == index && j.Group.CollectionID == "" && j.Group.CollectionIndex == nil &&
		!j.CreatedAt.IsZero() && j.UpdatedAt.Equal(j.CreatedAt) && j.Lifecycle != nil && *j.Lifecycle == (api.Lifecycle{}) && reflect.DeepEqual(j.Labels, map[string]string{"fixture": "synthetic-graph-ceiling-no-execution"})
}
func labCeilingEdge(edge api.GraphEdge, indices map[string]int) (from, to int, valid bool) {
	from, hasFrom := indices[edge.FromJobID]
	to, hasTo := indices[edge.ToJobID]
	return from, to, hasFrom && hasTo && labCeilingPair(from, to) && edge.From == fmt.Sprintf("node-%05d", from) && edge.To == fmt.Sprintf("node-%05d", to) && edge.Predicate == "success" && edge.Outcomes != nil && len(edge.Outcomes) == 0 && edge.UpstreamPhase == "accepted" && edge.UpstreamOutcome == "" && edge.State == "waiting"
}
func labCeilingProvenance(t *testing.T, m labCeilingManifest, complete string, sources []api.SourceStatus, fetched time.Time) {
	t.Helper()
	if complete != "complete" || fetched.IsZero() || len(sources) != 1 || sources[0].Scope != m.scope() || sources[0].Status != "available" || sources[0].AsOf == nil || sources[0].AsOf.IsZero() || sources[0].FetchedAt.IsZero() {
		t.Fatal("Graph page authority/completeness provenance differs")
	}
}
func labCeilingGrant(t *testing.T, read labMultiRead, m labCeilingManifest, allowed bool) {
	t.Helper()
	var b api.Bootstrap
	read("/api/v1/bootstrap", &b, 200)
	if b.FixtureMode || b.APIVersion != api.Version || b.Completeness != "complete" || !uuid(b.Account.ID) {
		t.Fatal("Live signed-account bootstrap required")
	}
	found := false
	for _, d := range b.Deployments {
		if d.ID != m.DeploymentID {
			continue
		}
		if d.Status != "available" {
			t.Fatal("Graph source unavailable")
		}
		for _, n := range d.Namespaces {
			if n.ID == m.NamespaceID {
				if found || n.Name != m.Namespace || !n.AuthorizationExpiresAt.After(time.Now()) || !slices.Contains(n.Capabilities, "groups.read") || !slices.Contains(n.Capabilities, "jobs.read") {
					t.Fatal("Graph namespace proof differs")
				}
				found = true
			}
		}
	}
	if found != allowed {
		t.Fatal("Current source-qualified graph grant differs")
	}
}

// GET-only real HTTP acceptance. Fixture admission and its source/row receipts
// are separately reviewed Lab operations; this test never seeds or executes jobs.
func TestLabDeployedGraphCeiling(t *testing.T) {
	if os.Getenv("JOBMAN_DASHBOARD_LAB_GRAPH_CEILING") != "1" {
		t.Skip("reviewed inert graph admission and explicit HTTP opt-in required")
	}
	raw := labExecutionFile(t, os.Getenv("JOBMAN_DASHBOARD_LAB_GRAPH_MANIFEST"), 2<<20)
	m, err := labCeilingDecode(raw, os.Getenv("JOBMAN_DASHBOARD_LAB_GRAPH_MANIFEST_SHA256"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	alice := labNativeSignIn(t, "alice", "71000000-0000-4000-8000-000000000001")
	bob := labNativeSignIn(t, "bob", "71000000-0000-4000-8000-000000000002")
	reader := func(session labNativeSession) labMultiRead {
		request := labReportClient(t, ctx, session)
		return func(path string, target any, status int) {
			t.Helper()
			response := request("GET", path, session.accessToken, "", nil, target, status)
			if len(response) > 2<<20 {
				t.Fatal("Graph response exceeded the client bound")
			}
		}
	}
	read, denied := reader(alice), reader(bob)
	source := labExecutionControl(t, ctx, alice)
	checkIdentity := func() {
		var caps struct {
			Capabilities struct {
				InstanceID    string `json:"instanceId"`
				RecoveryEpoch string `json:"recoveryEpoch"`
			} `json:"capabilities"`
		}
		source("GET", "/v1/capabilities", "", nil, &caps)
		if caps.Capabilities.InstanceID != m.InstanceID || caps.Capabilities.RecoveryEpoch != m.RecoveryEpoch {
			t.Fatal("Current graph source instance/epoch differs")
		}
	}
	checkIdentity()
	labCeilingGrant(t, read, m, true)
	labCeilingGrant(t, denied, m, false)
	indices := make(map[string]int, 10000)
	for _, n := range m.Nodes {
		indices[n.ID] = n.Index
	}
	cursor, firstChild := "", ""
	seenCursors := map[string]bool{}
	count := 0
	for pageNumber := 0; pageNumber < 200; pageNumber++ {
		if seenCursors[cursor] {
			t.Fatal("Child pagination repeated a cursor")
		}
		seenCursors[cursor] = true
		q := url.Values{"limit": {"50"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page api.WorkloadDetail
		read(m.path()+"?"+q.Encode(), &page, 200)
		labCeilingProvenance(t, m, page.Completeness, page.Sources, page.FetchedAt)
		w := page.Workload
		if page.Total != "10000" || len(page.Children) != 50 || w.Scope != m.scope() || w.ID != m.GraphID || w.Kind != "graph" || w.Name != "synthetic-ceiling-graph-v1" || w.Revision != "1" || w.TotalChildren != "10000" || w.Phase != "accepted" || w.Outcome != "" || w.Concurrency != "1" || w.UnsatisfiedPolicy != "skip" || w.AsOf.IsZero() || !reflect.DeepEqual(w.Counts, map[string]string{"active": "0", "terminal": "0", "success": "0", "failure": "0", "cancelled": "0", "waiting": "10000", "skipped": "0", "blocked": "0"}) {
			t.Fatal("Graph wrapper or complete child total differs")
		}
		for _, child := range page.Children {
			if !labCeilingChild(m, child, count) {
				t.Fatal("Graph child identity, ordering, dependency facts or execution state differs")
			}
			count++
		}
		cursor = page.NextCursor
		if pageNumber == 0 {
			firstChild = cursor
		}
		if (cursor == "") != (pageNumber == 199) {
			t.Fatal("Graph child pagination ended early or continued past total")
		}
	}
	cursor, firstEdge := "", ""
	seenCursors = map[string]bool{}
	edges := map[[2]int]bool{}
	previous := ""
	for pageNumber := 0; pageNumber < 1000; pageNumber++ {
		if seenCursors[cursor] {
			t.Fatal("Dependency pagination repeated a cursor")
		}
		seenCursors[cursor] = true
		q := url.Values{"limit": {"100"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page api.GraphEdgePage
		read(m.path()+"/dependencies?"+q.Encode(), &page, 200)
		labCeilingProvenance(t, m, page.Completeness, page.Sources, page.FetchedAt)
		if page.Total != "100000" || len(page.Items) != 100 {
			t.Fatal("Graph dependency bound/total differs")
		}
		for _, edge := range page.Items {
			from, to, valid := labCeilingEdge(edge, indices)
			pair := [2]int{from, to}
			order := edge.FromJobID + "/" + edge.ToJobID
			if !valid || edges[pair] || order <= previous {
				t.Fatal("Graph dependency identity, order, predicate or readiness differs")
			}
			edges[pair] = true
			previous = order
		}
		cursor = page.NextCursor
		if pageNumber == 0 {
			firstEdge = cursor
		}
		if (cursor == "") != (pageNumber == 999) {
			t.Fatal("Graph dependency pagination ended early or continued past total")
		}
	}
	if count != 10000 || len(edges) != 100000 || firstChild == "" || firstEdge == "" {
		t.Fatal("Complete graph traversal coverage differs")
	}
	for _, test := range []struct{ center, nodes, edges int }{{0, 10000, 100000}, {1, 12, 66}, {9999, 2, 1}} {
		for _, bounds := range [][2]int{{200, 500}, {3, 2}} {
			q := url.Values{"nodeId": {m.Nodes[test.center].ID}, "maxNodes": {strconv.Itoa(bounds[0])}, "maxEdges": {strconv.Itoa(bounds[1])}}
			var page api.GraphNeighborhood
			read(m.path()+"/neighborhood?"+q.Encode(), &page, 200)
			labCeilingProvenance(t, m, page.Completeness, page.Sources, page.FetchedAt)
			if page.CenterID != m.Nodes[test.center].ID || page.TotalNodes != strconv.Itoa(test.nodes) || page.TotalEdges != strconv.Itoa(test.edges) || len(page.Nodes) != min(bounds[0], test.nodes) || len(page.Edges) > bounds[1] || page.OmittedNodes != strconv.Itoa(test.nodes-len(page.Nodes)) || page.OmittedEdges != strconv.Itoa(test.edges-len(page.Edges)) {
				t.Fatal("Graph neighborhood scope, bounds or omission totals differ")
			}
			members := map[string]bool{}
			for _, node := range page.Nodes {
				index, ok := indices[node.ID]
				if !ok || members[node.ID] || !labCeilingChild(m, node, index) || index != test.center && !labCeilingPair(index, test.center) && !labCeilingPair(test.center, index) {
					t.Fatal("Neighborhood node is duplicated or outside one hop")
				}
				members[node.ID] = true
			}
			if !members[page.CenterID] {
				t.Fatal("Neighborhood omitted its selected center")
			}
			pairs := map[[2]int]bool{}
			for _, edge := range page.Edges {
				from, to, valid := labCeilingEdge(edge, indices)
				pair := [2]int{from, to}
				if !valid || pairs[pair] || !members[edge.FromJobID] || !members[edge.ToJobID] {
					t.Fatal("Neighborhood edge escaped loaded induced relation")
				}
				pairs[pair] = true
			}
			if test.nodes <= bounds[0] && test.edges <= bounds[1] && len(pairs) != test.edges {
				t.Fatal("Complete small neighborhood lost induced edges")
			}
		}
	}
	for _, selected := range []struct {
		direction string
		total     int
	}{{"", 11}, {"incoming", 1}, {"outgoing", 10}} {
		q := url.Values{"nodeId": {m.Nodes[1].ID}, "limit": {"100"}}
		if selected.direction != "" {
			q.Set("direction", selected.direction)
		}
		var page api.GraphEdgePage
		read(m.path()+"/dependencies?"+q.Encode(), &page, 200)
		labCeilingProvenance(t, m, page.Completeness, page.Sources, page.FetchedAt)
		if page.Total != strconv.Itoa(selected.total) || len(page.Items) != selected.total || page.NextCursor != "" {
			t.Fatal("Selected-node dependency view lost its complete bounded relation")
		}
		seen := map[[2]int]bool{}
		for _, edge := range page.Items {
			from, to, valid := labCeilingEdge(edge, indices)
			pair := [2]int{from, to}
			if !valid || seen[pair] || from != 1 && to != 1 || selected.direction == "incoming" && to != 1 || selected.direction == "outgoing" && from != 1 {
				t.Fatal("Selected-node dependency direction or endpoint differs")
			}
			seen[pair] = true
		}
	}
	denied(m.path(), nil, 403)
	denied(m.path()+"/children?limit=50&cursor="+url.QueryEscape(firstChild), nil, 403)
	denied(m.path()+"/dependencies?limit=100&cursor="+url.QueryEscape(firstEdge), nil, 403)
	read(m.path()+"/children?limit=51&cursor="+url.QueryEscape(firstChild), nil, 409)
	read(m.path()+"/dependencies?limit=100&nodeId="+m.Nodes[0].ID+"&cursor="+url.QueryEscape(firstEdge), nil, 409)
	other := labReadMultiFixture(t, alice.root+"/.lab/dashboard/fixture-info.json", false)
	for _, ns := range other.Namespaces {
		if ns.Name == "dashboard-research" {
			path := labMultiPrefix(api.Scope{DeploymentID: m.DeploymentID, NamespaceID: ns.ID}) + "/workloads/graph/" + ns.GraphID
			read(path+"/children?limit=50&cursor="+url.QueryEscape(firstChild), nil, 409)
		}
	}
	checkIdentity()
	labCeilingGrant(t, read, m, true)
	labCeilingGrant(t, denied, m, false)
	t.Log("PASS: real verified-TLS source-qualified graph HTTP navigation, 200 child pages/1000 dependency pages, exact 10000/100000 relation, bounded induced neighborhoods and account/query/scope cursor denial. Synthetic admitted metadata; no executor, rendered browser, simulator or physical-device claim.")
}

func TestLabGraphCeilingIndependentTopology(t *testing.T) {
	total := 0
	incoming := make([]int, 10000)
	// Enumerate the independent relation, not the source fixture's edge builder.
	for from := 0; from < 10000; from++ {
		for to := from + 1; to < 10000; to++ {
			if labCeilingPair(from, to) {
				total++
				incoming[to]++
			}
		}
	}
	if total != 100000 {
		t.Fatal("Independent graph relation has the wrong size")
	}
	for index, n := range incoming {
		if n != labCeilingIncoming(index) {
			t.Fatal("Independent incoming count differs")
		}
	}
	for _, test := range []struct{ center, nodes, edges int }{{1, 12, 66}, {9999, 2, 1}} {
		members := []int{}
		for i := 0; i < 10000; i++ {
			if i == test.center || labCeilingPair(i, test.center) || labCeilingPair(test.center, i) {
				members = append(members, i)
			}
		}
		edges := 0
		for _, from := range members {
			for _, to := range members {
				if labCeilingPair(from, to) {
					edges++
				}
			}
		}
		if len(members) != test.nodes || edges != test.edges {
			t.Fatal("Independent induced neighborhood differs")
		}
	}
}

func labCeilingTestManifest() labCeilingManifest {
	m := labCeilingManifest{Version: 1, Synthetic: true, Mode: "admitted-no-execution", DeploymentID: labDeployment, InstanceID: "e633cf92-258d-48ff-965a-fda88d68ef3a", RecoveryEpoch: "1", NamespaceID: "76000000-0000-4000-8000-000000000001", Namespace: "dashboard-operations", TargetID: "76000000-0000-4000-8000-000000000002", TargetGenerationID: "76000000-0000-4000-8000-000000000003", GraphID: "76000000-0000-4000-8000-000000000004", Revision: "1", TotalNodes: "10000", TotalEdges: "100000", RequestDigest: "sha256:" + fmt.Sprintf("%064x", 1)}
	for i := 0; i < 10000; i++ {
		m.Nodes = append(m.Nodes, struct {
			Index int    `json:"index"`
			ID    string `json:"id"`
		}{i, fmt.Sprintf("77000000-0000-4000-8000-%012d", i+1)})
	}
	return m
}
func TestLabGraphCeilingManifestRejectsDrift(t *testing.T) {
	check := func(m labCeilingManifest) error {
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		_, err = labCeilingDecode(raw, hex.EncodeToString(sum[:]))
		return err
	}
	if err := check(labCeilingTestManifest()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*labCeilingManifest){
		"other source":     func(m *labCeilingManifest) { m.DeploymentID = labSecondaryDeployment },
		"other instance":   func(m *labCeilingManifest) { m.InstanceID = labSecondaryInstance },
		"restore":          func(m *labCeilingManifest) { m.RecoveryEpoch = "2" },
		"execution claim":  func(m *labCeilingManifest) { m.Mode = "executed" },
		"missing node":     func(m *labCeilingManifest) { m.Nodes = m.Nodes[:9999] },
		"duplicate node":   func(m *labCeilingManifest) { m.Nodes[9999].ID = m.Nodes[0].ID },
		"wrong index":      func(m *labCeilingManifest) { m.Nodes[1].Index = 2 },
		"rounded count":    func(m *labCeilingManifest) { m.TotalEdges = "100001" },
		"malformed digest": func(m *labCeilingManifest) { m.RequestDigest = "sha256:" + fmt.Sprintf("%064s", "x") },
	} {
		t.Run(name, func(t *testing.T) {
			m := labCeilingTestManifest()
			mutate(&m)
			if check(m) == nil {
				t.Fatal("Changed reviewed graph identity accepted")
			}
		})
	}
	raw, _ := json.Marshal(labCeilingTestManifest())
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	raw = append(raw, ' ')
	if _, err := labCeilingDecode(raw, digest); err == nil {
		t.Fatal("Changed manifest bytes accepted under prior digest")
	}
	for _, suffix := range []string{"{}", " null"} {
		bad := append(slices.Clone(raw), suffix...)
		hash := sha256.Sum256(bad)
		if _, err := labCeilingDecode(bad, hex.EncodeToString(hash[:])); err == nil {
			t.Fatal("Trailing manifest document accepted")
		}
	}
}
func TestLabGraphCeilingRejectsExecutionAndWrongRelation(t *testing.T) {
	m := labCeilingTestManifest()
	index := 11
	when := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	child := func() api.WorkloadChild {
		return api.WorkloadChild{ID: m.Nodes[index].ID, Name: "node-00011", Index: "11", DependencyCounts: map[string]string{"total": "11", "waiting": "11", "satisfied": "0", "unsatisfied": "0"}, Job: api.Job{Scope: m.scope(), ID: m.Nodes[index].ID, Name: "node-00011", TargetID: m.TargetID, TargetGenerationID: m.TargetGenerationID, Backend: "subprocess", Revision: "1", DesiredState: "run", Phase: "accepted", CreatedAt: when, UpdatedAt: when, Lifecycle: &api.Lifecycle{}, Group: &api.GroupReference{GraphID: m.GraphID, GraphIndex: &index}, Owner: &api.Owner{ID: "76000000-0000-4000-8000-000000000005", IsCurrentUser: true}, Labels: map[string]string{"fixture": "synthetic-graph-ceiling-no-execution"}}}
	}
	if !labCeilingChild(m, child(), index) {
		t.Fatal("Valid source child rejected")
	}
	for name, mutate := range map[string]func(*api.WorkloadChild){
		"source":            func(c *api.WorkloadChild) { c.Job.DeploymentID = labSecondaryDeployment },
		"run":               func(c *api.WorkloadChild) { c.Job.CurrentRun = &api.RunReference{ID: m.GraphID, Number: "1"} },
		"imported":          func(c *api.WorkloadChild) { c.Job.Imported = true },
		"started":           func(c *api.WorkloadChild) { c.Job.StartedAt = &when },
		"target generation": func(c *api.WorkloadChild) { c.Job.TargetGenerationID = m.TargetID },
		"satisfied":         func(c *api.WorkloadChild) { c.DependencyCounts["satisfied"] = "1" },
		"disposition":       func(c *api.WorkloadChild) { c.Disposition = "ready" },
		"missing lifecycle": func(c *api.WorkloadChild) { c.Job.Lifecycle = nil },
	} {
		t.Run(name, func(t *testing.T) {
			value := child()
			mutate(&value)
			if labCeilingChild(m, value, index) {
				t.Fatal("Fabricated or misattributed graph state accepted")
			}
		})
	}
	edge := api.GraphEdge{From: "node-00001", To: "node-00011", FromJobID: m.Nodes[1].ID, ToJobID: m.Nodes[11].ID, Predicate: "success", Outcomes: []string{}, UpstreamPhase: "accepted", State: "waiting"}
	indices := map[string]int{m.Nodes[1].ID: 1, m.Nodes[11].ID: 11, m.Nodes[12].ID: 12}
	if _, _, ok := labCeilingEdge(edge, indices); !ok {
		t.Fatal("Valid graph edge rejected")
	}
	for name, mutate := range map[string]func(*api.GraphEdge){
		"outside adjacency": func(e *api.GraphEdge) { e.ToJobID = m.Nodes[12].ID; e.To = "node-00012" },
		"display name":      func(e *api.GraphEdge) { e.From = "node-00000" },
		"predicate":         func(e *api.GraphEdge) { e.Predicate = "terminal" },
		"missing outcomes":  func(e *api.GraphEdge) { e.Outcomes = nil },
		"readiness":         func(e *api.GraphEdge) { e.State = "satisfied" },
		"terminal":          func(e *api.GraphEdge) { e.UpstreamPhase = "terminal"; e.UpstreamOutcome = "success" },
	} {
		t.Run(name, func(t *testing.T) {
			value := edge
			mutate(&value)
			if _, _, ok := labCeilingEdge(value, indices); ok {
				t.Fatal("Wrong graph relation or evidence accepted")
			}
		})
	}
}
