package monitoring

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
)

type overviewRecoverySource struct {
	*testSource
	initialError error
	finalChange  func(*Discovery)
	discoveries  atomic.Int32
	summaries    atomic.Int32
}

func (s *overviewRecoverySource) Discover(ctx context.Context, a Actor) (Discovery, error) {
	call := s.discoveries.Add(1)
	if call == 1 && s.initialError != nil {
		return Discovery{}, s.initialError
	}
	d, err := s.testSource.Discover(ctx, a)
	if call > 1 && err == nil && s.finalChange != nil {
		s.finalChange(&d)
	}
	return d, err
}

func (s *overviewRecoverySource) Summary(ctx context.Context, a Actor, scope api.Scope, window api.Window) (Counts, error) {
	s.summaries.Add(1)
	return s.testSource.Summary(ctx, a, scope, window)
}

func TestOverviewInitialDiscoveryRecoveryDoesNotInventRevocationOrContribution(t *testing.T) {
	for _, initialError := range []error{ErrSource, ErrAuthority} {
		for _, withHealthy := range []bool{false, true} {
			t.Run(initialError.Error()+"/healthy="+map[bool]string{false: "no", true: "yes"}[withHealthy], func(t *testing.T) {
				recovered := &overviewRecoverySource{testSource: source("recovering", 7, time.Second), initialError: initialError}
				sources := []Source{recovered}
				scopes := []api.Scope{{DeploymentID: recovered.ID(), NamespaceID: "ns"}}
				if withHealthy {
					sources = append(sources, source("healthy", 3, time.Second))
					scopes = append(scopes, api.Scope{DeploymentID: "healthy", NamespaceID: "ns"})
				}
				e, err := New(sources, NewMemoryCursors())
				if err != nil {
					t.Fatal(err)
				}
				e.Now = func() time.Time { return clock }
				got, err := e.Overview(context.Background(), actor("one"), Query{Scopes: scopes}, api.Window{From: clock.Add(-time.Hour), To: clock})
				if recovered.discoveries.Load() != 2 || recovered.summaries.Load() != 0 {
					t.Fatalf("recovered source must be checked twice without reading counts: discoveries=%d summaries=%d", recovered.discoveries.Load(), recovered.summaries.Load())
				}
				if withHealthy {
					if err != nil || got.Completeness != "partial" || got.Active == nil || *got.Active != 3 || got.Terminal["unknown"] == nil || *got.Terminal["unknown"] != 2 {
						t.Fatalf("recovery corrupted the healthy subtotal: overview=%+v err=%v", got, err)
					}
				} else if !errors.Is(err, ErrSource) || got.Active != nil || got.Terminal["unknown"] != nil {
					t.Fatalf("no successful contribution must remain unavailable with no fabricated counts: overview=%+v err=%v", got, err)
				}
				for _, status := range got.Sources {
					if status.DeploymentID == recovered.ID() && status.Status == "authorization_unavailable" {
						return
					}
				}
				t.Fatal("recovered source lost its unavailable contribution status")
			})
		}
	}
}

func TestOverviewEstablishedAuthorityChangesStillFailClosed(t *testing.T) {
	changes := map[string]func(*Discovery){
		"version": func(d *Discovery) { d.Deployment.Namespaces[0].AuthorizationVersion = "2" },
		"epoch":   func(d *Discovery) { d.RecoveryEpoch = "2" },
		"instance": func(d *Discovery) {
			d.InstanceID = "replacement"
		},
		"removed": func(d *Discovery) { d.Deployment.Namespaces = nil },
		"expired": func(d *Discovery) { d.Deployment.Namespaces[0].AuthorizationExpiresAt = clock },
		"capability": func(d *Discovery) {
			d.Deployment.Namespaces[0].Capabilities = []string{"namespace.read"}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			s := &overviewRecoverySource{testSource: source("source", 7, time.Second), finalChange: change}
			e, err := New([]Source{s}, NewMemoryCursors())
			if err != nil {
				t.Fatal(err)
			}
			e.Now = func() time.Time { return clock }
			got, err := e.Overview(context.Background(), actor("one"), Query{Scopes: []api.Scope{{DeploymentID: s.ID(), NamespaceID: "ns"}}}, api.Window{From: clock.Add(-time.Hour), To: clock})
			if !errors.Is(err, ErrForbidden) || got.Active != nil || got.Terminal["unknown"] != nil || s.summaries.Load() != 1 {
				t.Fatalf("established authority change published counts: overview=%+v err=%v reads=%d", got, err, s.summaries.Load())
			}
		})
	}
}
