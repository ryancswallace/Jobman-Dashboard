package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/control"
	"github.com/ryancswallace/jobman-dashboard/internal/httpapi"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/store"
)

type runtimeSecrets struct {
	tls            tls.Certificate
	databaseURL    string
	identityClient *http.Client
	identity       auth.OIDCOptions
	sources        []control.Config
}

func textSecret(path string) (string, error) {
	data, err := config.ReadSecret(path, 16384)
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("text secret must contain one nonempty line")
	}
	return value, nil
}
func rootsFile(path string) (*x509.CertPool, error) {
	data, err := config.ReadPublicFile(path, 1<<20)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return nil, errors.New("trust file contains no valid PEM certificates")
	}
	return roots, nil
}
func certificateFiles(certPath, keyPath string) (tls.Certificate, error) {
	cert, err := config.ReadPublicFile(certPath, 1<<20)
	if err != nil {
		return tls.Certificate{}, err
	}
	key, err := config.ReadSecret(keyPath, 65536)
	if err != nil {
		return tls.Certificate{}, err
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return tls.Certificate{}, errors.New("TLS certificate and private key do not form a valid pair")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Add(time.Minute).Before(leaf.NotAfter) {
		return tls.Certificate{}, errors.New("TLS leaf certificate is outside its validity period")
	}
	pair.Leaf = leaf
	return pair, nil
}
func databaseSecret(path string) (string, error) {
	value, err := textSecret(path)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Fragment != "" || u.Query().Get("sslmode") != "verify-full" || u.Path == "" || u.Path == "/" {
		return "", errors.New("production database URL must name a database and use sslmode=verify-full")
	}
	return value, nil
}
func loadRuntime(c config.Config) (runtimeSecrets, error) {
	var result runtimeSecrets
	var err error
	result.tls, err = certificateFiles(c.ServerTLS.CertificateFile, c.ServerTLS.KeyFile)
	if err != nil {
		return result, fmt.Errorf("Dashboard TLS: %w", err)
	}
	publicURL, _ := url.Parse(c.PublicOrigin)
	if result.tls.Leaf.VerifyHostname(publicURL.Hostname()) != nil {
		return result, errors.New("Dashboard TLS certificate does not identify publicOrigin")
	}
	result.databaseURL, err = databaseSecret(c.DatabaseURLFile)
	if err != nil {
		return result, fmt.Errorf("runtime database: %w", err)
	}
	identityRoots, err := rootsFile(c.OIDC.TrustRootsFile)
	if err != nil {
		return result, fmt.Errorf("identity trust: %w", err)
	}
	result.identityClient, err = auth.PinnedIdentityHTTPClient(c.OIDC.Issuer, identityRoots)
	if err != nil {
		return result, err
	}
	secret, err := textSecret(c.OIDC.WebClientSecretFile)
	if err != nil {
		return result, fmt.Errorf("web client secret: %w", err)
	}
	key, err := config.ReadSecret(c.Encryption.KeyFile, 32)
	if err != nil || len(key) != 32 {
		return result, errors.New("encryption key file must contain exactly 32 private raw bytes")
	}
	result.identity = auth.OIDCOptions{Issuer: c.OIDC.Issuer, Audience: c.OIDC.APIAudience, WebClientID: c.OIDC.WebClientID, WebClientSecret: secret, NativeClientID: c.OIDC.NativeClientID, NativeRedirectURI: c.OIDC.NativeRedirectURI, DirectoryIDClaim: c.OIDC.DirectoryIDClaim, ClientIDClaim: c.OIDC.ClientIDClaim, PublicOrigin: c.PublicOrigin, Scopes: c.OIDC.Scopes, EncryptionKey: key, EncryptionKeyID: c.Encryption.KeyID, HTTPClient: result.identityClient}
	for _, source := range c.Controls {
		roots, err := rootsFile(source.TrustRootsFile)
		if err != nil {
			return result, fmt.Errorf("Control trust: %w", err)
		}
		certificate, err := certificateFiles(source.ClientCertificateFile, source.ClientKeyFile)
		if err != nil {
			return result, fmt.Errorf("Control client certificate: %w", err)
		}
		encoded, err := config.ReadSecret(source.DelegationKeyFile, 65536)
		if err != nil {
			return result, fmt.Errorf("Control signing key: %w", err)
		}
		block, rest := pem.Decode(encoded)
		if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
			return result, errors.New("Control signing key requires one PKCS8 Ed25519 PEM block")
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return result, errors.New("Control signing key is not PKCS8")
		}
		signingKey, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return result, errors.New("Control delegation requires an Ed25519 key")
		}
		signer, err := auth.NewDelegationSigner(signingKey, source.DelegationKeyID, source.ServiceID, source.Audience, certificate.Certificate[0], source.NamespaceIDs)
		if err != nil {
			return result, err
		}
		result.sources = append(result.sources, control.Config{DeploymentID: source.ID, Name: source.Name, Endpoint: source.Origin, InstanceID: source.ExpectedInstanceID, NamespaceIDs: source.NamespaceIDs, Roots: roots, Certificate: certificate, Signer: signer})
	}
	if stat, err := os.Stat(c.WebRoot); err != nil || !stat.IsDir() {
		return result, errors.New("webRoot must contain the compiled web application")
	}
	if stat, err := os.Stat(c.WebRoot + "/index.html"); err != nil || !stat.Mode().IsRegular() {
		return result, errors.New("webRoot has no compiled index.html")
	}
	return result, nil
}

func runConfigured(path, mode, migrationURLFile string) error {
	if mode != "serve" && mode != "check-config" && mode != "migrate" {
		return errors.New("mode must be serve, check-config, or migrate")
	}
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if mode == "migrate" {
		if migrationURLFile == "" {
			return errors.New("migrate requires --migration-database-url-file with a separate DDL identity")
		}
		dsn, err := databaseSecret(migrationURLFile)
		if err != nil {
			return err
		}
		db, err := store.Open(ctx, dsn)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := db.Migrate(ctx); err != nil {
			return errors.New("Dashboard migration failed; inspect the database migration ledger")
		}
		slog.Info("Dashboard migrations applied")
		return nil
	}
	if migrationURLFile != "" {
		return errors.New("migration identity may be supplied only in migrate mode")
	}
	loaded, err := loadRuntime(c)
	if err != nil {
		return err
	}
	if mode == "check-config" {
		slog.Info("configuration and local key material validated; network and source authorization not tested")
		return nil
	}
	db, err := store.Open(ctx, loaded.databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.CheckSchema(ctx); err != nil {
		return err
	}
	identity, err := auth.NewOIDC(ctx, loaded.identity, db)
	if err != nil {
		return err
	}
	sources := make([]monitoring.Source, 0, len(loaded.sources))
	for _, entry := range loaded.sources {
		entry.VerifyIdentity = func(ctx context.Context, instance, epoch string) error {
			return db.VerifySourceIdentity(ctx, entry.DeploymentID, instance, epoch, c.ConfigurationRevision)
		}
		client, err := control.New(entry)
		if err != nil {
			return err
		}
		defer client.Close()
		sources = append(sources, client)
	}
	engine, err := monitoring.New(sources, db)
	if err != nil {
		return err
	}
	maintenanceDone := make(chan struct{})
	defer func() { stop(); <-maintenanceDone }()
	go func() {
		defer close(maintenanceDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				maintenance, cancel := context.WithTimeout(ctx, 15*time.Second)
				if err := db.PruneAuthentication(maintenance); err != nil {
					slog.Warn("authentication retention pass failed")
				}
				if _, err := db.PruneCursors(maintenance); err != nil {
					slog.Warn("browse retention pass failed")
				}
				cancel()
			}
		}
	}()
	app := &httpapi.Server{Engine: engine, Auth: identity, AuthRoutes: identity, Preferences: db, Static: os.DirFS(c.WebRoot)}
	server := &http.Server{Addr: c.Listen, Handler: app.Handler(), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{loaded.tls}}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan error, 1)
	go func() {
		slog.Info("Dashboard HTTPS service starting", "listen", c.Listen)
		done <- server.ListenAndServeTLS("", "")
	}()
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
