package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/buildinfo"
	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/control"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
	"github.com/ryancswallace/jobman-dashboard/internal/operations"
	"github.com/ryancswallace/jobman-dashboard/internal/runtimeconfig"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == logs.HelperArgument {
		if logs.RunHelper(os.Stdin, os.Stdout) != nil {
			os.Exit(2)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("log broker stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		return buildinfo.Write(os.Stdout)
	}
	fs := flag.NewFlagSet("jobman-log-broker", flag.ContinueOnError)
	path := fs.String("config", "", "absolute broker JSON configuration path")
	mode := fs.String("mode", "serve", "serve or check-config")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *path == "" || (*mode != "serve" && *mode != "check-config") || fs.NArg() != 0 {
		return errors.New("broker requires --config and serve or check-config mode")
	}
	c, err := config.LoadBroker(*path)
	if err != nil {
		return err
	}
	ids := make([]string, len(c.Controls))
	for i, source := range c.Controls {
		ids[i] = source.ID
	}
	observed, err := runtimeconfig.NewObservations(c.Observability, "broker", c.ConfigurationRevision, ids, "")
	if err != nil {
		return err
	}
	defer observed.Close()
	cert, err := runtimeconfig.Certificate(c.ServerTLS.CertificateFile, c.ServerTLS.KeyFile)
	if err != nil {
		return err
	}
	origin, _ := url.Parse(c.PublicOrigin)
	if cert.Leaf.VerifyHostname(origin.Hostname()) != nil {
		return errors.New("broker TLS certificate does not identify publicOrigin")
	}
	roots, err := runtimeconfig.Roots(c.ClientTrustRootsFile)
	if err != nil {
		return err
	}
	services := make([]auth.BrokerService, 0, len(c.Services))
	for _, entry := range c.Services {
		service, err := registration(entry)
		if err != nil {
			return err
		}
		services = append(services, service)
	}
	verifier, err := auth.NewBrokerVerifier(services)
	if err != nil {
		return err
	}
	sources := make(map[string]logs.ManifestSource)
	workerSources := make(map[string]logs.ManifestSource)
	var pins *operations.SourceIdentities
	if *mode == "serve" {
		pins, err = operations.OpenSourceIdentities(c.StateDirectory)
		if err != nil {
			return err
		}
		defer pins.Close()
	}
	for _, entry := range c.Controls {
		cfg, err := runtimeconfig.Source(entry)
		if err != nil {
			return err
		}
		cfg.Observer = observed.Registry
		if pins != nil {
			cfg.VerifyIdentity = func(ctx context.Context, instance, epoch string) error {
				return pins.Verify(ctx, entry.ID, instance, epoch, c.ConfigurationRevision)
			}
		}
		client, err := control.New(cfg)
		if err != nil {
			return err
		}
		defer client.Close()
		sources[entry.ID] = client
		cfg.ActorMode = auth.DelegationWorker
		workerClient, err := control.New(cfg)
		if err != nil {
			return err
		}
		defer workerClient.Close()
		workerSources[entry.ID] = workerClient
	}
	executable, err := os.Executable()
	if err != nil {
		return errors.New("cannot locate broker reader executable")
	}
	reader, err := logs.NewProcessReader(executable, int(c.ReaderConcurrency), time.Duration(c.ReaderTimeoutMilliseconds)*time.Millisecond)
	if err != nil {
		return err
	}
	mappings := make([]logs.Mapping, 0, len(c.LogRoots))
	for _, m := range c.LogRoots {
		mappings = append(mappings, logs.Mapping{DeploymentID: m.DeploymentID, TargetGenerationID: m.TargetGenerationID, StoreName: m.StoreName, StoreVersion: m.StoreVersion, Root: m.Root})
	}
	local, err := logs.NewLocalChunks(mappings, logs.ObservedReader{Reader: reader, Observer: observed.Registry})
	if err != nil {
		return err
	}
	service, err := logs.NewServiceWithModes(sources, workerSources, local, verifier)
	if err != nil {
		return err
	}
	if *mode == "check-config" {
		slog.Info("broker configuration and trust validated; network, state and log-root accessibility not tested")
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: c.Listen, Handler: observed.Registry.HTTP(service.Handler()), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	if err = observed.Listen(ctx, func(ctx context.Context) error { return ctx.Err() }); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	observed.Started()
	done := make(chan error, 1)
	go func() {
		slog.Info("storage log broker starting", "listen", c.Listen)
		done <- server.ServeTLS(listener, "", "")
	}()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		observed.Draining()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func registration(c config.BrokerRegistration) (auth.BrokerService, error) {
	var result auth.BrokerService
	data, err := config.ReadPublicFile(c.PublicKeyFile, 65536)
	if err != nil {
		return result, err
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return result, errors.New("broker service key requires one Ed25519 PKIX public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return result, errors.New("invalid broker service public key")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return result, errors.New("broker service public key must be Ed25519")
	}
	data, err = config.ReadPublicFile(c.ClientCertificateFile, 1<<20)
	if err != nil {
		return result, err
	}
	block, _ = pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return result, errors.New("broker service requires a leaf client certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || time.Now().Before(cert.NotBefore) || !time.Now().Add(time.Minute).Before(cert.NotAfter) {
		return result, errors.New("broker service certificate is outside its validity period")
	}
	digest := sha256.Sum256(cert.Raw)
	return auth.BrokerService{KeyID: c.KeyID, ServiceID: c.ServiceID, Audience: c.Audience, DeploymentID: c.DeploymentID, CertificateSHA256: base64.RawURLEncoding.EncodeToString(digest[:]), PublicKey: key, NamespaceIDs: c.NamespaceIDs}, nil
}
