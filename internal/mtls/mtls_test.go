package mtls

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGenerateKeyAndCSR(t *testing.T) {
	keyPEM, csrPEM, err := GenerateKeyAndCSR()
	if err != nil {
		t.Fatalf("GenerateKeyAndCSR() error: %v", err)
	}

	// Verify key is valid PEM.
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("keyPEM is not valid PEM")
	}
	if keyBlock.Type != "RSA PRIVATE KEY" {
		t.Errorf("key PEM type = %q, want %q", keyBlock.Type, "RSA PRIVATE KEY")
	}

	// Verify CSR is valid PEM.
	csrBlock, _ := pem.Decode(csrPEM)
	if csrBlock == nil {
		t.Fatal("csrPEM is not valid PEM")
	}
	if csrBlock.Type != "CERTIFICATE REQUEST" {
		t.Errorf("CSR PEM type = %q, want %q", csrBlock.Type, "CERTIFICATE REQUEST")
	}

	// Parse CSR to verify it's structurally valid.
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		t.Fatalf("parsing CSR: %v", err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Errorf("CSR signature check failed: %v", err)
	}
}

func TestGenerateKeyAndCSR_UniqueKeys(t *testing.T) {
	key1, _, err := GenerateKeyAndCSR()
	if err != nil {
		t.Fatalf("first GenerateKeyAndCSR() error: %v", err)
	}

	key2, _, err := GenerateKeyAndCSR()
	if err != nil {
		t.Fatalf("second GenerateKeyAndCSR() error: %v", err)
	}

	if string(key1) == string(key2) {
		t.Error("two generated keys should be different")
	}
}

func TestSaveCertFiles(t *testing.T) {
	dir := t.TempDir()

	certPath := filepath.Join(dir, "client.crt")
	keyPath := filepath.Join(dir, "client.key")
	caPath := filepath.Join(dir, "ca.crt")

	cert := []byte("-----BEGIN CERTIFICATE-----\ncert-data\n-----END CERTIFICATE-----\n")
	key := []byte("-----BEGIN RSA PRIVATE KEY-----\nkey-data\n-----END RSA PRIVATE KEY-----\n")
	ca := []byte("-----BEGIN CERTIFICATE-----\nca-data\n-----END CERTIFICATE-----\n")

	if err := SaveCertFiles(certPath, keyPath, caPath, cert, key, ca); err != nil {
		t.Fatalf("SaveCertFiles() error: %v", err)
	}

	// On Windows, the key file is ACL-restricted; restore for test reads and cleanup.
	if runtime.GOOS == "windows" {
		cmd := exec.Command("icacls", keyPath, "/grant", "Everyone:(F)")
		cmd.CombinedOutput()
		t.Cleanup(func() {
			cmd := exec.Command("icacls", keyPath, "/grant", "Everyone:(F)")
			cmd.CombinedOutput()
		})
	}

	// Verify files exist with correct content.
	gotCert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("reading cert: %v", err)
	}
	if string(gotCert) != string(cert) {
		t.Errorf("cert content mismatch")
	}

	gotKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("reading key: %v", err)
	}
	if string(gotKey) != string(key) {
		t.Errorf("key content mismatch")
	}

	gotCA, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("reading ca: %v", err)
	}
	if string(gotCA) != string(ca) {
		t.Errorf("ca content mismatch")
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := atomicWrite(path, []byte("hello world"), 0644); err != nil {
		t.Fatalf("atomicWrite() error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want %q", string(data), "hello world")
	}

	// Verify temp file is cleaned up.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should be removed after successful write")
	}
}

func TestAtomicWrite_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// Write initial content.
	if err := atomicWrite(path, []byte("initial"), 0644); err != nil {
		t.Fatalf("first atomicWrite() error: %v", err)
	}

	// Overwrite.
	if err := atomicWrite(path, []byte("updated"), 0644); err != nil {
		t.Fatalf("second atomicWrite() error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if string(data) != "updated" {
		t.Errorf("content = %q, want %q", string(data), "updated")
	}
}
