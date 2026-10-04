package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

type modeTestVerifier struct{ mode auth.DelegationMode }

func (v modeTestVerifier) AuthenticateDelegation(*http.Request, api.Scope) (auth.VerifiedDelegation, error) {
	return auth.VerifiedDelegation{Mode: v.mode}, nil
}

type modeTestSource struct{ calls int }

func (s *modeTestSource) Manifest(context.Context, monitoring.Actor, ManifestQuery) (Manifest, error) {
	s.calls++
	return Manifest{}, monitoring.ErrForbidden
}

type modeTestReader struct{}

func (modeTestReader) Read(context.Context, FileRequest) FileResult {
	return FileResult{State: "invalid_manifest"}
}

func TestBrokerModeSelectionDoesNotFallbackOrReadRequestOverride(t *testing.T) {
	const deployment = "11111111-1111-4111-8111-111111111111"
	const namespace = "22222222-2222-4222-8222-222222222222"
	local, e := NewLocalChunks([]Mapping{{DeploymentID: deployment, StoreName: "logs", StoreVersion: "1", Root: "/private/synthetic"}}, modeTestReader{})
	if e != nil {
		t.Fatal(e)
	}
	body, e := json.Marshal(ChunkRequest{Scope: api.Scope{DeploymentID: deployment, NamespaceID: namespace}, JobID: deployment, RunNumber: "1", ExecutionID: namespace, Stream: "stdout", Sequence: "1"})
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []auth.DelegationMode{"", auth.DelegationInteractive, auth.DelegationWorker, "unknown"} {
		t.Run(string(mode), func(t *testing.T) {
			interactive, worker := &modeTestSource{}, &modeTestSource{}
			service, e := NewServiceWithModes(map[string]ManifestSource{deployment: interactive}, map[string]ManifestSource{deployment: worker}, local, modeTestVerifier{mode})
			if e != nil {
				t.Fatal(e)
			}
			request := httptest.NewRequest(http.MethodPost, "https://broker.invalid/v1/chunks/read", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Actor-Mode", "worker")
			response := httptest.NewRecorder()
			service.Handler().ServeHTTP(response, request)
			switch mode {
			case auth.DelegationInteractive:
				if interactive.calls != 1 || worker.calls != 0 {
					t.Fatal("interactive mode was overridden")
				}
			case auth.DelegationWorker:
				if worker.calls != 1 || interactive.calls != 0 {
					t.Fatal("worker fell back")
				}
			default:
				if interactive.calls+worker.calls != 0 || response.Code != http.StatusNotFound {
					t.Fatal("unknown verified mode admitted")
				}
			}
		})
	}
	interactive := &modeTestSource{}
	legacy, e := NewService(map[string]ManifestSource{deployment: interactive}, local, modeTestVerifier{auth.DelegationWorker})
	if e != nil {
		t.Fatal(e)
	}
	request := httptest.NewRequest(http.MethodPost, "https://broker.invalid/v1/chunks/read", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	legacy.Handler().ServeHTTP(response, request)
	if interactive.calls != 0 || response.Code != http.StatusNotFound {
		t.Fatal("legacy constructor accepted worker by implicit fallback")
	}
}
