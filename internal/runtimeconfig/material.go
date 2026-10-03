// Package runtimeconfig loads explicitly configured trust/key files. It never
// falls back to ambient credentials, operating-system roots or environment URLs.
package runtimeconfig

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/config"
	"github.com/ryancswallace/jobman-dashboard/internal/control"
	"github.com/ryancswallace/jobman-dashboard/internal/logs"
)

func Roots(path string) (*x509.CertPool, error) {
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
func Certificate(certPath, keyPath string) (tls.Certificate, error) {
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
func SigningKey(path string) (ed25519.PrivateKey, error) {
	encoded, err := config.ReadSecret(path, 65536)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(encoded)
	if block == nil || block.Type != "PRIVATE KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("delegation signing key requires one PKCS8 Ed25519 PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("delegation signing key is not PKCS8")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("delegation requires an Ed25519 key")
	}
	return key, nil
}
func Source(c config.Control) (control.Config, error) {
	var result control.Config
	roots, err := Roots(c.TrustRootsFile)
	if err != nil {
		return result, err
	}
	cert, err := Certificate(c.ClientCertificateFile, c.ClientKeyFile)
	if err != nil {
		return result, err
	}
	key, err := SigningKey(c.DelegationKeyFile)
	if err != nil {
		return result, err
	}
	signer, err := auth.NewDelegationSigner(key, c.DelegationKeyID, c.ServiceID, c.Audience, cert.Certificate[0], c.NamespaceIDs)
	if err != nil {
		return result, err
	}
	return control.Config{DeploymentID: c.ID, Name: c.Name, Endpoint: c.Origin, InstanceID: c.ExpectedInstanceID, NamespaceIDs: c.NamespaceIDs, Roots: roots, Certificate: cert, Signer: signer}, nil
}
func Broker(c config.RemoteBroker) (logs.ClientConfig, error) {
	var result logs.ClientConfig
	roots, err := Roots(c.TrustRootsFile)
	if err != nil {
		return result, err
	}
	cert, err := Certificate(c.ClientCertificateFile, c.ClientKeyFile)
	if err != nil {
		return result, err
	}
	key, err := SigningKey(c.DelegationKeyFile)
	if err != nil {
		return result, err
	}
	signer, err := auth.NewDelegationSigner(key, c.DelegationKeyID, c.ServiceID, c.Audience, cert.Certificate[0], c.NamespaceIDs)
	if err != nil {
		return result, err
	}
	return logs.ClientConfig{DeploymentID: c.DeploymentID, Origin: c.Origin, Roots: roots, Certificate: cert, Signer: signer}, nil
}
