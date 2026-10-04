package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/observability"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

func TestObservationOptionalAndPrivateBoundaries(t *testing.T) {
	p, err := NewObservations(nil, "api", 1, nil, "")
	if err != nil || p.Registry != nil {
		t.Fatal(err)
	}
	p.Close()
	root, err := os.MkdirTemp("/tmp", "jd-o-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(root, "web")
	private := filepath.Join(root, "private")
	for _, dir := range []string{web, private} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{"20000000-0000-4000-8000-000000000001"}
	dsnPath := filepath.Join(private, "database-url")
	if err = os.WriteFile(dsnPath, []byte("postgres://synthetic@127.0.0.1:1/dashboard?sslmode=verify-full"), 0600); err != nil {
		t.Fatal(err)
	}
	operator := config.OperatorConfig{SchemaVersion: 1, DatabaseURLFile: dsnPath, Deployments: []config.OperatorDeployment{{ID: ids[0]}}}
	operatorPath := filepath.Join(private, "operator.json")
	write := func() {
		raw, _ := json.Marshal(operator)
		if err = os.WriteFile(operatorPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	cfg := &config.Observability{SocketPath: filepath.Join(private, "operator.sock"), OperatorConfigFile: operatorPath}
	p, err = NewObservations(cfg, "api", 1, ids, web)
	if err != nil {
		t.Fatal("optional DB connection must be lazy", err)
	}
	p.Close()
	operator.Deployments[0].ID = "20000000-0000-4000-8000-000000000002"
	write()
	if p, err = NewObservations(cfg, "api", 1, ids, web); err == nil {
		p.Close()
		t.Fatal("observation scope broadened")
	}
	operator.Deployments[0].ID = ids[0]
	public := filepath.Join(web, "public-secret")
	if err = os.WriteFile(public, []byte("secret-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(private, "alias")
	if err = os.Symlink(public, alias); err != nil {
		t.Fatal(err)
	}
	operator.DatabaseURLFile = alias
	write()
	if p, err = NewObservations(cfg, "api", 1, ids, web); err == nil {
		p.Close()
		t.Fatal("nested operator secret web alias accepted")
	}
	cfg.OperatorConfigFile = ""
	cfg.SocketPath = filepath.Join(web, "operator.sock")
	if p, err = NewObservations(cfg, "api", 1, ids, web); err == nil {
		p.Close()
		t.Fatal("socket parent in web root accepted")
	}
}

func TestSchemaObservationDoesNotMislabelOutages(t *testing.T) {
	r, err := observability.NewRegistry("api", 1, nil, observability.Build{})
	if err != nil {
		t.Fatal(err)
	}
	observeSchemaError(r, context.DeadlineExceeded)
	observeSchemaError(r, errors.New("private-database-permission"))
	if !strings.Contains(string(r.Render()), `kind="schema"} 0`) {
		t.Fatal("unavailability counted as version mismatch")
	}
	observeSchemaError(r, fmt.Errorf("wrapped: %w", store.ErrSchemaMismatch))
	if !strings.Contains(string(r.Render()), `kind="schema"} 1`) {
		t.Fatal("typed incompatibility not counted")
	}
}
