package runtimeconfig

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryancswallace/jobman-dashboard/internal/buildinfo"
	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/observability"
	"github.com/ryancswallace/jobman-dashboard/internal/operations"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

// ProcessObservations owns only optional, read-only operator connections. Its
// sampler never borrows the runtime DB identity or performs source requests.
type ProcessObservations struct {
	Registry *observability.Registry
	options  observability.Options
	pool     *pgxpool.Pool
	listener *observability.Listener
}

func NewObservations(c *config.Observability, role string, revision int64, ids []string, webRoot string) (*ProcessObservations, error) {
	p := &ProcessObservations{}
	if c == nil {
		return p, nil
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	info := buildinfo.Snapshot()
	r, err := observability.NewRegistry(role, revision, ids, observability.Build{Version: info.Version, Revision: info.Revision, GoVersion: info.GoVersion, OS: info.OS, Architecture: info.Architecture})
	if err != nil {
		return nil, err
	}
	p.Registry = r
	p.options = observability.Options{SocketPath: c.SocketPath, Registry: r}
	if webRoot != "" {
		if err = outsideWeb(webRoot, filepath.Dir(c.SocketPath)); err != nil {
			return nil, err
		}
	}
	if c.OperatorConfigFile == "" {
		return p, nil
	}
	operator, err := config.LoadOperator(c.OperatorConfigFile)
	if err != nil {
		return nil, errors.New("operator observation configuration unavailable")
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	deployments := make([]operations.Deployment, 0, len(operator.Deployments))
	selected := make([]string, 0, len(operator.Deployments))
	for _, d := range operator.Deployments {
		if !allowed[d.ID] {
			return nil, errors.New("operator observation source is outside this process configuration")
		}
		deployments = append(deployments, operations.Deployment{ID: d.ID, Name: d.Name})
		selected = append(selected, d.ID)
	}
	if webRoot != "" {
		for _, path := range []string{c.OperatorConfigFile, operator.DatabaseURLFile} {
			if err = outsideWeb(webRoot, path); err != nil {
				return nil, err
			}
		}
	}
	raw, err := config.ReadSecret(operator.DatabaseURLFile, 16384)
	if err != nil {
		return nil, errors.New("operator database credential unavailable")
	}
	dsn := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	u, err := url.Parse(dsn)
	if err != nil || strings.ContainsAny(dsn, "\r\n\x00") || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Fragment != "" || u.Path == "" || u.Path == "/" || u.Query().Get("sslmode") != "verify-full" {
		return nil, errors.New("operator database requires a named TLS verify-full connection")
	}
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("operator database configuration invalid")
	}
	pc.MaxConns = 2
	pc.MinConns = 0
	pc.ConnConfig.ConnectTimeout = 2 * time.Second
	pc.MaxConnIdleTime = time.Minute
	// Lazy pool creation preserves live process readiness when optional aggregate
	// observation is unavailable. A failed sample is explicitly unavailable.
	p.pool, err = pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		return nil, errors.New("operator observation pool unavailable")
	}
	db := &store.Store{Pool: p.pool}
	p.options.Sample = func(ctx context.Context) ([]byte, error) {
		if err := db.CheckSchema(ctx); err != nil {
			observeSchemaError(r, err)
			return nil, err
		}
		var out bytes.Buffer
		if err := operations.WriteStatus(ctx, db, deployments, "prometheus", &out); err != nil {
			return nil, err
		}
		pressure, err := db.OperationalReportPressure(ctx, selected)
		if err != nil {
			return nil, err
		}
		if err = operations.WriteReportPressure(&out, pressure); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
	return p, nil
}
func outsideWeb(web, path string) error {
	w, e := filepath.EvalSymlinks(web)
	if e != nil {
		return errors.New("observation boundary web root unavailable")
	}
	p, e := filepath.EvalSymlinks(path)
	if e != nil || config.ContainsPath(w, p) || config.ContainsPath(p, w) {
		return errors.New("operator observation files and socket parent must resolve outside webRoot")
	}
	return nil
}
func (p *ProcessObservations) Listen(ctx context.Context, ready func(context.Context) error) error {
	if p.Registry == nil {
		return nil
	}
	p.options.Ready = ready
	l, e := observability.Listen(ctx, p.options)
	if e != nil {
		return e
	}
	p.listener = l
	return nil
}
func (p *ProcessObservations) Started() {
	if p != nil && p.listener != nil {
		p.listener.Server.Started()
	}
}
func (p *ProcessObservations) Draining() {
	if p != nil && p.listener != nil {
		p.listener.Server.Draining()
	}
}
func (p *ProcessObservations) Close() {
	if p == nil {
		return
	}
	if p.listener != nil {
		p.listener.Close()
	}
	if p.pool != nil {
		p.pool.Close()
	}
}
func (p *ProcessObservations) Database(db *store.Store) {
	if p.Registry == nil {
		return
	}
	p.Registry.SetPoolGauges(func() map[string]float64 {
		s := db.Pool.Stat()
		return map[string]float64{"acquired": float64(s.AcquiredConns()), "idle": float64(s.IdleConns()), "total": float64(s.TotalConns()), "maximum": float64(s.MaxConns()), "acquire_count": float64(s.AcquireCount()), "acquire_wait_seconds": s.AcquireDuration().Seconds(), "empty_acquire_count": float64(s.EmptyAcquireCount()), "cancelled_acquire_count": float64(s.CanceledAcquireCount())}
	})
}
func (p *ProcessObservations) Readiness(db *store.Store) func(context.Context) error {
	return func(ctx context.Context) error {
		if e := db.Pool.Ping(ctx); e != nil {
			return e
		}
		if e := db.CheckSchema(ctx); e != nil {
			observeSchemaError(p.Registry, e)
			return e
		}
		return nil
	}
}

func observeSchemaError(r *observability.Registry, err error) {
	if errors.Is(err, store.ErrSchemaMismatch) {
		r.Mismatch("schema")
	}
}
