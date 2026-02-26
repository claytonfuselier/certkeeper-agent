package deployment

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/claytonfuselier/certkeeper-agent/internal/config"
	"github.com/claytonfuselier/certkeeper-agent/internal/state"
)

// restoreACL re-grants the current user full control on Windows so that
// t.TempDir() cleanup (and test reads) succeed on ACL-restricted files.
func restoreACL(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	// Grant Everyone full control so cleanup works.
	cmd := exec.Command("icacls", path, "/grant", "Everyone:(F)")
	cmd.CombinedOutput() // best-effort
}

func TestWriteBundle(t *testing.T) {
	dir := t.TempDir()
	deployDir := filepath.Join(dir, "1")

	bundle := &Bundle{
		Fullchain: "-----BEGIN CERTIFICATE-----\nfullchain\n-----END CERTIFICATE-----\n",
		Cert:      "-----BEGIN CERTIFICATE-----\ncert\n-----END CERTIFICATE-----\n",
		Key:       "-----BEGIN PRIVATE KEY-----\nkey\n-----END PRIVATE KEY-----\n",
	}

	if err := WriteBundle(deployDir, bundle); err != nil {
		t.Fatalf("WriteBundle() error: %v", err)
	}

	// On Windows, privkey.pem gets ACL-restricted; restore for test reads and cleanup.
	privkeyPath := filepath.Join(deployDir, "privkey.pem")
	restoreACL(t, privkeyPath)
	t.Cleanup(func() { restoreACL(t, privkeyPath) })

	// Verify files exist with correct content.
	files := map[string]string{
		"fullchain.pem": bundle.Fullchain,
		"cert.pem":      bundle.Cert,
		"privkey.pem":   bundle.Key,
	}

	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(deployDir, name))
		if err != nil {
			t.Errorf("reading %s: %v", name, err)
			continue
		}
		if string(data) != want {
			t.Errorf("%s content = %q, want %q", name, string(data), want)
		}
	}
}

func TestDeleteCertFiles(t *testing.T) {
	dir := t.TempDir()
	deployDir := filepath.Join(dir, "1")
	os.MkdirAll(deployDir, 0755)

	// Create cert files.
	for _, name := range []string{"fullchain.pem", "cert.pem", "privkey.pem"} {
		if err := os.WriteFile(filepath.Join(deployDir, name), []byte("test"), 0644); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}

	if err := DeleteCertFiles(deployDir); err != nil {
		t.Fatalf("DeleteCertFiles() error: %v", err)
	}

	// Verify files are gone.
	for _, name := range []string{"fullchain.pem", "cert.pem", "privkey.pem"} {
		if _, err := os.Stat(filepath.Join(deployDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been deleted", name)
		}
	}
}

func TestDeleteCertFiles_NonExistent(t *testing.T) {
	// Should not error if files don't exist.
	if err := DeleteCertFiles(filepath.Join(t.TempDir(), "nonexistent")); err != nil {
		t.Errorf("DeleteCertFiles() unexpected error: %v", err)
	}
}

func TestRunPostDeploy_Empty(t *testing.T) {
	// Empty post_deploy should be a no-op.
	if err := RunPostDeploy(context.Background(), "", "/etc/ceagent", 1, "test", "/tmp", nil); err != nil {
		t.Errorf("RunPostDeploy('') unexpected error: %v", err)
	}
}

func TestRunPostDeploy_RelativePath(t *testing.T) {
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "scripts")
	os.MkdirAll(scriptsDir, 0755)

	var scriptName, scriptContent string
	if runtime.GOOS == "windows" {
		scriptName = "test.ps1"
		scriptContent = "Write-Output 'hello'"
	} else {
		scriptName = "test.sh"
		scriptContent = "#!/bin/sh\necho hello"
	}

	scriptPath := filepath.Join(scriptsDir, scriptName)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("creating script: %v", err)
	}

	relPath := filepath.Join(".", "scripts", scriptName)
	deployDir := filepath.Join(dir, "deployments", "1")
	os.MkdirAll(deployDir, 0755)

	err := RunPostDeploy(context.Background(), relPath, dir, 1, "test-deploy", deployDir, []string{"example.com"})
	if err != nil {
		t.Errorf("RunPostDeploy() error: %v", err)
	}
}

func TestRunPostDeploy_EnvVars(t *testing.T) {
	dir := t.TempDir()
	deployDir := filepath.Join(dir, "deployments", "1")
	os.MkdirAll(deployDir, 0755)

	var scriptName, scriptContent string
	if runtime.GOOS == "windows" {
		scriptName = "check-env.ps1"
		scriptContent = `
if ($env:CEAGENT_DEPLOYMENT_ID -ne "42") { exit 1 }
if ($env:CEAGENT_DEPLOYMENT_NAME -ne "nginx-proxy") { exit 1 }
if ($env:CEAGENT_DOMAINS -ne "example.com,www.example.com") { exit 1 }
`
	} else {
		scriptName = "check-env.sh"
		scriptContent = `#!/bin/sh
[ "$CEAGENT_DEPLOYMENT_ID" = "42" ] || exit 1
[ "$CEAGENT_DEPLOYMENT_NAME" = "nginx-proxy" ] || exit 1
[ "$CEAGENT_DOMAINS" = "example.com,www.example.com" ] || exit 1
`
	}

	scriptPath := filepath.Join(dir, scriptName)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("creating script: %v", err)
	}

	err := RunPostDeploy(context.Background(), scriptPath, dir, 42, "nginx-proxy", deployDir, []string{"example.com", "www.example.com"})
	if err != nil {
		t.Errorf("RunPostDeploy() error: %v", err)
	}
}

func TestFetchDeployments(t *testing.T) {
	deployments := []Deployment{
		{ID: 1, Name: "nginx", Enabled: true, CertStatus: "active"},
		{ID: 5, Name: "mail", Enabled: false, CertStatus: "active"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/deployments" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deployments)
	}))
	defer srv.Close()

	result, err := FetchDeployments(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchDeployments() error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
	if result[0].Name != "nginx" {
		t.Errorf("result[0].Name = %q, want %q", result[0].Name, "nginx")
	}
}

func TestFetchDeployments_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	_, err := FetchDeployments(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Error("FetchDeployments() expected error on 500")
	}
}

func TestFetchBundle(t *testing.T) {
	bundle := Bundle{
		DeploymentID:   1,
		DeploymentName: "nginx",
		ContentHash:    "abc123",
		Fullchain:      "fullchain-pem",
		Cert:           "cert-pem",
		Key:            "key-pem",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/deployments/1/bundle" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bundle)
	}))
	defer srv.Close()

	result, err := FetchBundle(context.Background(), srv.Client(), srv.URL, 1)
	if err != nil {
		t.Fatalf("FetchBundle() error: %v", err)
	}

	if result.ContentHash != "abc123" {
		t.Errorf("ContentHash = %q, want %q", result.ContentHash, "abc123")
	}
	if result.Fullchain != "fullchain-pem" {
		t.Errorf("Fullchain = %q", result.Fullchain)
	}
}

func TestFetchBundle_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer srv.Close()

	_, err := FetchBundle(context.Background(), srv.Client(), srv.URL, 999)
	if err == nil {
		t.Error("FetchBundle() expected error on 404")
	}
}

func TestSync(t *testing.T) {
	certPEM, keyPEM := testCertAndKey(t)
	contentHash := "newhash123"
	deployments := []Deployment{
		{
			ID:          1,
			Name:        "nginx",
			Enabled:     true,
			CertStatus:  "active",
			ContentHash: &contentHash,
		},
	}
	bundle := Bundle{
		DeploymentID:   1,
		DeploymentName: "nginx",
		ContentHash:    contentHash,
		Fullchain:      certPEM,
		Cert:           certPEM,
		Key:            keyPEM,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/agent/deployments":
			json.NewEncoder(w).Encode(deployments)
		case "/api/agent/deployments/1/bundle":
			json.NewEncoder(w).Encode(bundle)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	cfg := config.New(srv.URL, cfgPath)

	statePath := filepath.Join(dir, "state.json")
	st, _ := state.Load(statePath)

	if _, err := Sync(srv.Client(), srv.URL, cfg, st); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	// Restore ACL on privkey so we can read and clean up.
	deployDir := cfg.DeploymentDir(1)
	restoreACL(t, filepath.Join(deployDir, "privkey.pem"))
	t.Cleanup(func() { restoreACL(t, filepath.Join(deployDir, "privkey.pem")) })

	// Verify deployment was added to config.
	d1 := cfg.Deployments[1]
	if d1 == nil || !d1.Enabled {
		t.Error("deployment 1 should be enabled in config")
	}

	// Verify content hash was saved in state.
	if st.DeploymentHashes[1] != contentHash {
		t.Errorf("DeploymentHashes[1] = %q, want %q", st.DeploymentHashes[1], contentHash)
	}

	// Verify cert files were written.
	data, err := os.ReadFile(filepath.Join(deployDir, "fullchain.pem"))
	if err != nil {
		t.Fatalf("reading fullchain.pem: %v", err)
	}
	if string(data) != certPEM {
		t.Errorf("fullchain.pem content mismatch")
	}
}

func TestSync_RemovedDeployment(t *testing.T) {
	// Server returns empty list — deployment 1 should be disabled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "[]")
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	cfg := config.New(srv.URL, cfgPath)
	cfg.Deployments[1] = &config.DeploymentConfig{Enabled: true, PostDeploy: "./scripts/deploy.sh"}

	// Create cert files that should be deleted.
	deployDir := cfg.DeploymentDir(1)
	os.MkdirAll(deployDir, 0755)
	for _, name := range []string{"fullchain.pem", "cert.pem", "privkey.pem"} {
		os.WriteFile(filepath.Join(deployDir, name), []byte("test"), 0644)
	}

	statePath := filepath.Join(dir, "state.json")
	st, _ := state.Load(statePath)
	st.DeploymentHashes[1] = "oldhash"

	if _, err := Sync(srv.Client(), srv.URL, cfg, st); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	// Deployment should be disabled but still in config.
	d1 := cfg.Deployments[1]
	if d1 == nil {
		t.Fatal("deployment 1 should still be in config")
	}
	if d1.Enabled {
		t.Error("deployment 1 should be disabled")
	}
	if d1.PostDeploy != "./scripts/deploy.sh" {
		t.Error("post_deploy script should be preserved")
	}

	// Cert files should be deleted.
	for _, name := range []string{"fullchain.pem", "cert.pem", "privkey.pem"} {
		if _, err := os.Stat(filepath.Join(deployDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been deleted", name)
		}
	}

	// Content hash should be removed from state.
	if _, exists := st.DeploymentHashes[1]; exists {
		t.Error("deployment hash should be removed from state")
	}
}

func TestSync_SkipsDisabled(t *testing.T) {
	contentHash := "hash123"
	deployments := []Deployment{
		{ID: 1, Name: "nginx", Enabled: true, CertStatus: "active", ContentHash: &contentHash},
	}

	bundleRequested := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/agent/deployments":
			json.NewEncoder(w).Encode(deployments)
		case "/api/agent/deployments/1/bundle":
			bundleRequested = true
			json.NewEncoder(w).Encode(Bundle{ContentHash: contentHash})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := config.New(srv.URL, filepath.Join(dir, "config.yml"))
	// User has locally disabled this deployment.
	cfg.Deployments[1] = &config.DeploymentConfig{Enabled: false}

	st, _ := state.Load(filepath.Join(dir, "state.json"))

	Sync(srv.Client(), srv.URL, cfg, st)

	if bundleRequested {
		t.Error("bundle should not be downloaded for disabled deployments")
	}

	// Should remain disabled (not re-enabled by sync).
	if cfg.Deployments[1].Enabled {
		t.Error("sync should not re-enable a user-disabled deployment")
	}
}

func TestSync_SkipsUpToDate(t *testing.T) {
	contentHash := "samehash"
	deployments := []Deployment{
		{ID: 1, Name: "nginx", Enabled: true, CertStatus: "active", ContentHash: &contentHash},
	}

	bundleRequested := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/bundle"):
			bundleRequested = true
			w.WriteHeader(http.StatusOK)
		default:
			json.NewEncoder(w).Encode(deployments)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := config.New(srv.URL, filepath.Join(dir, "config.yml"))
	cfg.Deployments[1] = &config.DeploymentConfig{Enabled: true}

	st, _ := state.Load(filepath.Join(dir, "state.json"))
	st.DeploymentHashes[1] = "samehash" // already up to date

	Sync(srv.Client(), srv.URL, cfg, st)

	if bundleRequested {
		t.Error("bundle should not be downloaded when content_hash matches")
	}
}

// testCertAndKey generates a self-signed certificate and EC private key for testing.
func testCertAndKey(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating cert: %v", err)
	}
	certStr := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling key: %v", err)
	}
	keyStr := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certStr, keyStr
}

func TestValidateBundle_Valid(t *testing.T) {
	certPEM, keyPEM := testCertAndKey(t)
	bundle := &Bundle{
		Fullchain: certPEM,
		Cert:      certPEM,
		Key:       keyPEM,
	}
	if err := ValidateBundle(bundle); err != nil {
		t.Errorf("ValidateBundle() unexpected error: %v", err)
	}
}

func TestValidateBundle_InvalidCert(t *testing.T) {
	_, keyPEM := testCertAndKey(t)
	bundle := &Bundle{
		Fullchain: "not valid pem",
		Cert:      "not valid pem",
		Key:       keyPEM,
	}
	err := ValidateBundle(bundle)
	if err == nil {
		t.Error("ValidateBundle() expected error for invalid cert PEM")
	}
}

func TestValidateBundle_InvalidKey(t *testing.T) {
	certPEM, _ := testCertAndKey(t)
	bundle := &Bundle{
		Fullchain: certPEM,
		Cert:      certPEM,
		Key:       "not valid pem",
	}
	err := ValidateBundle(bundle)
	if err == nil {
		t.Error("ValidateBundle() expected error for invalid key PEM")
	}
}
