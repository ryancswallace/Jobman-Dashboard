package logs

import (
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/observability"
)

func TestLogObservationsReportVerifiedCorruptionWithoutContent(t *testing.T) {
	b, source, reader, actor, request := brokerFixture(t)
	r, err := observability.NewRegistry("api", 1, []string{request.Scope.DeploymentID}, observability.Build{})
	if err != nil {
		t.Fatal(err)
	}
	b.SetObserver(r, "interactive")
	for key := range reader.data {
		reader.data[key] = []byte("private-corrupt-bytes")
	}
	result, err := b.Read(t.Context(), actor, request)
	if err != nil || result.State != "chunk_corrupt" || result.BytesBase64 != "" {
		t.Fatal(result.State, err)
	}
	raw := string(r.Render())
	if !strings.Contains(raw, `outcome="chunk_corrupt"} 1`) {
		t.Fatal(raw)
	}
	for _, private := range []string{request.JobID, source.manifest.NamespaceName, "private-corrupt-bytes"} {
		if strings.Contains(raw, private) {
			t.Fatal("private log detail escaped")
		}
	}
}
