package mtls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"time"
)

// GenerateKeyAndCSR creates a new RSA 2048-bit key pair and PKCS#10 CSR.
// Returns PEM-encoded private key and CSR.
func GenerateKeyAndCSR() (keyPEM, csrPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generating RSA key: %w", err)
	}

	csrTemplate := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "agent"},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, key)
	if err != nil {
		return nil, nil, fmt.Errorf("creating CSR: %w", err)
	}

	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	csrPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})

	return keyPEM, csrPEM, nil
}

// CertPathProvider describes anything that can supply the three mTLS file paths.
// Implemented by config.Config.
type CertPathProvider interface {
	ClientCertPath() string
	ClientKeyPath() string
	CACertPath() string
}

// NewHTTPClientFromConfig creates an mTLS HTTP client using the paths from a CertPathProvider.
func NewHTTPClientFromConfig(p CertPathProvider) (*http.Client, error) {
	return NewHTTPClient(p.ClientCertPath(), p.ClientKeyPath(), p.CACertPath())
}

// NewHTTPClient creates an HTTP client configured for mTLS using the agent's
// certificate, private key, and the CA certificate for server verification.
func NewHTTPClient(certPath, keyPath, caPath string) (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("loading client certificate: %w", err)
	}

	caCert, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("reading CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		Timeout: 30 * time.Second,
	}, nil
}

// SaveCertFiles writes the agent certificate, private key, and CA certificate
// to disk with appropriate file permissions.
func SaveCertFiles(certPath, keyPath, caPath string, cert, key, ca []byte) error {
	// Write private key first (most sensitive).
	if err := atomicWrite(keyPath, key, 0600); err != nil {
		return fmt.Errorf("saving private key: %w", err)
	}
	// On Windows, restrict the ACL to SYSTEM and Administrators.
	if err := restrictFileACL(keyPath); err != nil {
		return fmt.Errorf("restricting private key ACL: %w", err)
	}

	if err := atomicWrite(certPath, cert, 0644); err != nil {
		return fmt.Errorf("saving certificate: %w", err)
	}

	if err := atomicWrite(caPath, ca, 0644); err != nil {
		return fmt.Errorf("saving CA certificate: %w", err)
	}

	return nil
}

// RestrictKeyFile sets platform-appropriate restrictive permissions on a private key file.
// On Windows, this sets ACLs to SYSTEM and Administrators only.
// On Unix, this is a no-op (file permissions are set at write time).
func RestrictKeyFile(path string) error {
	return restrictFileACL(path)
}

// atomicWrite writes data to a file atomically via a temp file + rename.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
