package monitoring

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type artifactFixture struct {
	*testSource
	noCapability bool
	corrupt      string
	reads        int
}

func (s *artifactFixture) Discover(c context.Context, a Actor) (Discovery, error) {
	d, err := s.testSource.Discover(c, a)
	if !s.noCapability {
		for i := range d.Deployment.Namespaces {
			d.Deployment.Namespaces[i].Capabilities = append(d.Deployment.Namespaces[i].Capabilities, "artifacts.read")
		}
	}
	return d, err
}
func (s *artifactFixture) Artifacts(_ context.Context, _ Actor, q ArtifactQuery) (ArtifactSourcePage, error) {
	s.reads++
	if s.fail {
		return ArtifactSourcePage{}, ErrSource
	}
	start, _ := strconv.Atoi(q.Cursor)
	if start < 0 || start > 7 {
		return ArtifactSourcePage{}, ErrCursor
	}
	end := min(start+q.Limit, 7)
	out := ArtifactSourcePage{Total: "7", AsOf: clock, Items: []api.Artifact{}}
	for i := start; i < end; i++ {
		name := fmt.Sprintf("result-%d", i)
		out.Items = append(out.Items, api.Artifact{ID: "execution/" + name, Name: name, RunID: "run", RunNumber: "1", ExecutionID: "execution", TargetGenerationID: "generation", SizeBytes: "9007199254740993", Checksum: "sha256:test", PublishedAt: clock, Availability: "metadata_only"})
	}
	if end < 7 {
		out.NextCursor = strconv.Itoa(end)
	}
	switch s.corrupt {
	case "empty":
		out.Items = nil
	case "loop":
		out.NextCursor = q.Cursor
	case "order":
		out.Items[0].ID = "broken"
	case "scope":
		out.Items[0].RunNumber = "2"
	case "availability":
		out.Items[0].Availability = "available"
	case "count":
		out.Total = ""
	case "zero_total":
		out.Total = "0"
	case "short_total":
		out.Total = "1"
	case "next_at_total":
		out.Total = strconv.Itoa(len(out.Items))
	}
	if s.hook != nil {
		s.hook()
	}
	return out, nil
}
func newArtifactFixture() *artifactFixture {
	return &artifactFixture{testSource: source("a", 0, time.Minute)}
}
func TestArtifactPagingExactMetadataAndCursorIsolation(t *testing.T) {
	for _, mode := range []string{"normal", "actor", "job", "scope", "run", "limit", "grant", "epoch", "revoked", "capability", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s := newArtifactFixture()
			e := groupEngine(t, s)
			a := actor("one")
			q := ArtifactQuery{Scope: api.Scope{DeploymentID: "a", NamespaceID: "ns"}, JobID: "job", RunNumber: "1", Limit: 2}
			first, err := e.Artifacts(t.Context(), a, q, "")
			if err != nil || len(first.Items) != 2 || first.NextCursor == "" || first.Total != "7" || first.Items[0].SizeBytes != "9007199254740993" {
				t.Fatalf("first %+v %v", first, err)
			}
			switch mode {
			case "actor":
				a = actor("two")
			case "job":
				q.JobID = "other"
			case "scope":
				q.Scope.NamespaceID = "other"
			case "run":
				q.RunNumber = "2"
			case "limit":
				q.Limit = 1
			case "grant":
				s.version = "2"
			case "epoch":
				s.epoch = "2"
			case "revoked":
				s.revoked = true
			case "capability":
				s.noCapability = true
			case "expired":
				e.Now = func() time.Time { return clock.Add(16 * time.Minute) }
			}
			page, err := e.Artifacts(t.Context(), a, q, first.NextCursor)
			if mode != "normal" {
				if err == nil || len(page.Items) != 0 {
					t.Fatalf("cursor isolation %+v %v", page, err)
				}
				return
			}
			if err != nil || page.Items[0].Name != "result-2" {
				t.Fatalf("next %+v %v", page, err)
			}
			replay, err := e.Artifacts(t.Context(), a, q, first.NextCursor)
			if err != nil || replay.Items[0] != page.Items[0] {
				t.Fatal("replay changed immutable items")
			}
			count := len(first.Items) + len(page.Items)
			for page.NextCursor != "" {
				page, err = e.Artifacts(t.Context(), a, q, page.NextCursor)
				if err != nil {
					t.Fatal(err)
				}
				count += len(page.Items)
			}
			if count != 7 {
				t.Fatalf("paging count %d", count)
			}
		})
	}
}
func TestArtifactRevocationAndMalformedSourceNeverReturnMetadata(t *testing.T) {
	for _, mode := range []string{"revocation", "empty", "loop", "order", "scope", "availability", "count", "zero_total", "short_total", "next_at_total"} {
		t.Run(mode, func(t *testing.T) {
			s := newArtifactFixture()
			e := groupEngine(t, s)
			q := ArtifactQuery{Scope: api.Scope{DeploymentID: "a", NamespaceID: "ns"}, JobID: "job", RunNumber: "1", Limit: 2}
			first, err := e.Artifacts(t.Context(), actor("one"), q, "")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "revocation" {
				s.hook = func() { s.version = "2" }
			} else {
				s.corrupt = mode
			}
			out, err := e.Artifacts(t.Context(), actor("one"), q, first.NextCursor)
			if err == nil || len(out.Items) != 0 {
				t.Fatalf("unsafe response %+v %v", out, err)
			}
		})
	}
	s := source("a", 0, time.Minute)
	e := groupEngine(t, s)
	if _, err := e.Artifacts(t.Context(), actor("one"), ArtifactQuery{Scope: api.Scope{DeploymentID: "a", NamespaceID: "ns"}, JobID: "job", Limit: 1}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing capability: %v", err)
	}
}
