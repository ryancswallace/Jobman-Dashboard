package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/fixtures"
	"github.com/ryancswallace/jobman-dashboard/internal/httpapi"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
)

func main() {
	if err := run(); err != nil {
		slog.Error("dashboard stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	fs := flag.NewFlagSet("jobman-dashboard", flag.ContinueOnError)
	fixture := fs.Bool("fixture", false, "serve synthetic development data, loopback only")
	configPath := fs.String("config", "", "absolute production JSON configuration path")
	mode := fs.String("mode", "serve", "serve, check-config, or migrate")
	migrationURL := fs.String("migration-database-url-file", "", "private migration identity database URL file (migrate mode only)")
	listen := fs.String("listen", "127.0.0.1:8088", "HTTP bind address")
	web := fs.String("web", "web/dist", "compiled static web directory")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if !*fixture {
		if *configPath == "" {
			return fmt.Errorf("production requires --config; synthetic development requires explicit --fixture")
		}
		return runConfigured(*configPath, *mode, *migrationURL)
	}
	if *configPath != "" || *mode != "serve" || *migrationURL != "" {
		return fmt.Errorf("fixture mode cannot be combined with production configuration or migrations")
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return fmt.Errorf("invalid listen address")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("fixture mode requires an explicit loopback IP address")
	}
	engine, err := monitoring.New(fixtures.Sources(time.Now().UTC()), monitoring.NewMemoryCursors())
	if err != nil {
		return err
	}
	app := &httpapi.Server{Engine: engine, FixtureMode: true, Static: os.DirFS(*web), Auth: httpapi.AuthFunc(func(*http.Request) (monitoring.Actor, error) {
		return monitoring.Actor{Account: api.Account{ID: fixtures.AccountID, DisplayName: "Lab Alice · synthetic fixture"}}, nil
	})}
	server := &http.Server{Addr: *listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { slog.Info("synthetic fixture server", "listen", *listen); done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
