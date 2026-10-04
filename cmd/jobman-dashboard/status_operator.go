package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/operations"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

type statusOperatorOptions struct{ config, format string }

func parseStatusOperator(args []string) (statusOperatorOptions, error) {
	var o statusOperatorOptions
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.config, "config", "", "absolute private runtime config path")
	fs.StringVar(&o.format, "format", "json", "json or prometheus")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || !filepath.IsAbs(o.config) || o.format != "json" && o.format != "prometheus" {
		return statusOperatorOptions{}, errors.New("status requires --config with an absolute path and optional --format=json|prometheus")
	}
	return o, nil
}

// Status uses local configuration and the database only. It does not open source,
// identity, signing, encryption, or server TLS key material or mutate the registry.
func runStatusOperator(args []string, output io.Writer) error {
	o, err := parseStatusOperator(args)
	if err != nil {
		return err
	}
	c, err := config.Load(o.config)
	if err != nil {
		return errors.New("operator status configuration is unavailable or invalid")
	}
	dsn, err := databaseSecret(c.DatabaseURLFile)
	if err != nil {
		return errors.New("operator status database material is unavailable")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return operations.ErrStatusUnavailable
	}
	defer db.Close()
	if err = db.CheckSchema(ctx); err != nil {
		return operations.ErrStatusUnavailable
	}
	deployments := make([]operations.Deployment, len(c.Controls))
	for i, source := range c.Controls {
		deployments[i] = operations.Deployment{ID: source.ID, Name: source.Name}
	}
	if err = operations.WriteStatus(ctx, db, deployments, o.format, output); err != nil {
		return operations.ErrStatusUnavailable
	}
	return nil
}
