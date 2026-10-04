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

type statusOperatorOptions struct{ config, operatorConfig, format string }

func parseStatusOperator(args []string) (statusOperatorOptions, error) {
	var o statusOperatorOptions
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.config, "config", "", "absolute legacy runtime config path")
	fs.StringVar(&o.operatorConfig, "operator-config", "", "absolute dedicated read-only operator config path")
	fs.StringVar(&o.format, "format", "json", "json or prometheus")
	invalid := errors.New("status requires exactly one absolute --operator-config or legacy --config and optional --format=json|prometheus")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || o.format != "json" && o.format != "prometheus" {
		return statusOperatorOptions{}, invalid
	}
	configFlag, operatorFlag := false, false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configFlag = true
		}
		if f.Name == "operator-config" {
			operatorFlag = true
		}
	})
	if configFlag == operatorFlag || configFlag && !filepath.IsAbs(o.config) || operatorFlag && !filepath.IsAbs(o.operatorConfig) {
		return statusOperatorOptions{}, invalid
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
	configured, err := loadStatusConfiguration(o)
	if err != nil {
		return err
	}
	dsn, err := databaseSecret(configured.databaseURLFile)
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

	if err = operations.WriteStatus(ctx, db, configured.deployments, o.format, output); err != nil {
		return operations.ErrStatusUnavailable
	}
	return nil
}

// Loading this shape does not read credentials or initialize any runtime adapter.
type statusConfiguration struct {
	databaseURLFile string
	deployments     []operations.Deployment
}

func loadStatusConfiguration(o statusOperatorOptions) (statusConfiguration, error) {
	var result statusConfiguration
	invalid := errors.New("operator status configuration is unavailable or invalid")
	if o.operatorConfig != "" {
		c, err := config.LoadOperator(o.operatorConfig)
		if err != nil {
			return result, invalid
		}
		result.databaseURLFile = c.DatabaseURLFile
		result.deployments = make([]operations.Deployment, len(c.Deployments))
		for i, source := range c.Deployments {
			result.deployments[i] = operations.Deployment{ID: source.ID, Name: source.Name}
		}
		return result, nil
	}
	c, err := config.Load(o.config)
	if err != nil {
		return result, invalid
	}
	result.databaseURLFile = c.DatabaseURLFile
	result.deployments = make([]operations.Deployment, len(c.Controls))
	for i, source := range c.Controls {
		result.deployments[i] = operations.Deployment{ID: source.ID, Name: source.Name}
	}
	return result, nil
}
