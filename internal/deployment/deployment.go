package deployment

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/claytonfuselier/certkeeper-agent/internal/config"
	"github.com/claytonfuselier/certkeeper-agent/internal/mtls"
	"github.com/claytonfuselier/certkeeper-agent/internal/state"
)

// ScriptTimeout is the maximum time a post-deploy script may run.
const ScriptTimeout = 30 * time.Second

// maxResponseSize limits the maximum size of HTTP response bodies (10 MB).
const maxResponseSize = 10 * 1024 * 1024

// Deployment represents a deployment from the server API.
type Deployment struct {
	ID               int      `json:"id"`
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	CertificateID    int      `json:"certificate_id"`
	Domains          []string `json:"domains"`
	CertStatus       string   `json:"cert_status"`
	ExpiresAt        string   `json:"expires_at"`
	IssuedAt         string   `json:"issued_at"`
	LastRenewedAt    *string  `json:"last_renewed_at"`
	Staging          bool     `json:"staging"`
	ContentHash      *string  `json:"content_hash"`
	LastDeployedAt   *string  `json:"last_deployed_at"`
	LastDeployedHash *string  `json:"last_deployed_hash"`
}

// Bundle represents the certificate bundle from GET /api/agent/deployments/:id/bundle.
type Bundle struct {
	DeploymentID   int      `json:"deployment_id"`
	DeploymentName string   `json:"deployment_name"`
	CertificateID  int      `json:"certificate_id"`
	Domains        []string `json:"domains"`
	ExpiresAt      string   `json:"expires_at"`
	IssuedAt       string   `json:"issued_at"`
	ContentHash    string   `json:"content_hash"`
	Fullchain      string   `json:"fullchain"`
	Cert           string   `json:"cert"`
	Key            string   `json:"key"`
}

// FetchDeployments calls GET /api/agent/deployments and returns the list.
func FetchDeployments(ctx context.Context, client *http.Client, serverURL string) ([]Deployment, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", serverURL+"/api/agent/deployments", nil)
	if err != nil {
		return nil, fmt.Errorf("creating deployments request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching deployments: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("reading deployments response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deployments returned %d: %s", resp.StatusCode, string(body))
	}

	var deployments []Deployment
	if err := json.Unmarshal(body, &deployments); err != nil {
		return nil, fmt.Errorf("parsing deployments: %w", err)
	}

	return deployments, nil
}

// FetchBundle calls GET /api/agent/deployments/:id/bundle and returns the cert bundle.
func FetchBundle(ctx context.Context, client *http.Client, serverURL string, deploymentID int) (*Bundle, error) {
	url := fmt.Sprintf("%s/api/agent/deployments/%d/bundle", serverURL, deploymentID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating bundle request for deployment %d: %w", deploymentID, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching bundle for deployment %d: %w", deploymentID, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("reading bundle response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bundle for deployment %d returned %d: %s", deploymentID, resp.StatusCode, string(body))
	}

	var bundle Bundle
	if err := json.Unmarshal(body, &bundle); err != nil {
		return nil, fmt.Errorf("parsing bundle for deployment %d: %w", deploymentID, err)
	}

	return &bundle, nil
}

// WriteBundle writes the certificate bundle files to the deployment directory.
// Uses atomic writes (temp file + rename) to prevent partial reads.
func WriteBundle(deployDir string, bundle *Bundle) error {
	// Ensure deployment directory exists.
	if err := os.MkdirAll(deployDir, 0755); err != nil {
		return fmt.Errorf("creating deployment dir %s: %w", deployDir, err)
	}

	files := map[string]struct {
		content string
		perm    os.FileMode
	}{
		"fullchain.pem": {bundle.Fullchain, 0644},
		"cert.pem":      {bundle.Cert, 0644},
		"privkey.pem":   {bundle.Key, 0600},
	}

	for name, f := range files {
		path := filepath.Join(deployDir, name)
		tmp := path + ".tmp"

		if err := os.WriteFile(tmp, []byte(f.content), f.perm); err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("renaming %s: %w", name, err)
		}

		// Restrict ACLs on private key files (Windows).
		if f.perm == 0600 {
			if err := mtls.RestrictKeyFile(path); err != nil {
				slog.Warn("Failed to restrict key file ACL", "path", path, "error", err)
			}
		}
	}

	return nil
}

// DeleteCertFiles removes all certificate files from a deployment directory.
func DeleteCertFiles(deployDir string) error {
	files := []string{"fullchain.pem", "cert.pem", "privkey.pem"}
	for _, name := range files {
		path := filepath.Join(deployDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}

	// Try to remove the directory if empty.
	os.Remove(deployDir)
	return nil
}

// RunPostDeploy executes the post-deploy script/command for a deployment.
// Environment variables are set per-deployment to provide context.
// If postDeploy is a relative path, it is resolved relative to configDir.
// The parent context is used so that scripts can be interrupted during shutdown.
func RunPostDeploy(ctx context.Context, postDeploy string, configDir string, deployID int, deployName string, deployDir string, domains []string) error {
	if postDeploy == "" {
		return nil
	}

	// Resolve relative paths against the config directory.
	if !filepath.IsAbs(postDeploy) {
		postDeploy = filepath.Join(configDir, postDeploy)
	}

	ctx, cancel := context.WithTimeout(ctx, ScriptTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", postDeploy)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", postDeploy)
	}

	// Set deployment-specific environment variables.
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("CEAGENT_DEPLOYMENT_ID=%d", deployID),
		fmt.Sprintf("CEAGENT_DEPLOYMENT_NAME=%s", deployName),
		fmt.Sprintf("CEAGENT_DEPLOYMENT_DIR=%s", deployDir),
		fmt.Sprintf("CEAGENT_FULLCHAIN_PATH=%s", filepath.Join(deployDir, "fullchain.pem")),
		fmt.Sprintf("CEAGENT_CERT_PATH=%s", filepath.Join(deployDir, "cert.pem")),
		fmt.Sprintf("CEAGENT_KEY_PATH=%s", filepath.Join(deployDir, "privkey.pem")),
		fmt.Sprintf("CEAGENT_DOMAINS=%s", strings.Join(domains, ",")),
	)

	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		slog.Info("Post-deploy output",
			"deployment_id", deployID,
			"output", strings.TrimSpace(string(output)),
		)
	}

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("post-deploy script timed out after %s", ScriptTimeout)
	}

	if err != nil {
		return fmt.Errorf("post-deploy script failed: %w", err)
	}

	return nil
}

// Sync synchronizes local deployments with the server's list.
// It downloads new/updated certs, disables removed deployments, and runs post-deploy scripts.
// Returns (incomplete, error): incomplete is true if some individual deployments failed
// to download or validate but the overall sync was not a total failure.
func Sync(client *http.Client, serverURL string, cfg *config.Config, st *state.State) (bool, error) {
	ctx := context.Background()
	deployments, err := FetchDeployments(ctx, client, serverURL)
	if err != nil {
		return false, err
	}

	// Track whether any individual deployment failed.
	incomplete := false
	configChanged := false

	// Build a set of server-side deployment IDs.
	serverIDs := make(map[int]bool)
	for _, d := range deployments {
		serverIDs[d.ID] = true
	}

	// Handle deployments no longer on the server.
	for id, depCfg := range cfg.Deployments {
		if !serverIDs[id] && depCfg.Enabled {
			slog.Info("Deployment removed from server, disabling", "deployment_id", id)
			depCfg.Enabled = false
			configChanged = true

			// Delete cert files for security.
			deployDir := cfg.DeploymentDir(id)
			if err := DeleteCertFiles(deployDir); err != nil {
				slog.Error("Failed to delete cert files", "deployment_id", id, "error", err)
			}

			// Remove the content hash from state.
			delete(st.DeploymentHashes, id)
		}
	}

	// Process each server-side deployment.
	for _, d := range deployments {
		// Add new deployments to config.
		if _, exists := cfg.Deployments[d.ID]; !exists {
			slog.Info("New deployment discovered", "deployment_id", d.ID, "name", d.Name)
			cfg.Deployments[d.ID] = &config.DeploymentConfig{
				Enabled: true,
			}
			configChanged = true
		}

		depCfg := cfg.Deployments[d.ID]

		// Skip disabled deployments (user or agent disabled).
		if !depCfg.Enabled {
			slog.Debug("Skipping disabled deployment", "deployment_id", d.ID)
			continue
		}

		// Skip non-active certificates.
		if d.CertStatus != "active" {
			slog.Debug("Skipping non-active deployment", "deployment_id", d.ID, "status", d.CertStatus)
			continue
		}

		// Check if cert has changed.
		if d.ContentHash == nil {
			continue
		}
		localHash := st.DeploymentHashes[d.ID]
		if localHash == *d.ContentHash {
			slog.Debug("Deployment up to date", "deployment_id", d.ID)
			continue
		}

		// Download and deploy the bundle.
		slog.Info("Downloading cert bundle", "deployment_id", d.ID, "name", d.Name)
		bundle, err := FetchBundle(ctx, client, serverURL, d.ID)
		if err != nil {
			slog.Error("Failed to download bundle", "deployment_id", d.ID, "error", err)
			incomplete = true
			continue // Don't block other deployments.
		}

		// Validate PEM content before writing to disk.
		if err := ValidateBundle(bundle); err != nil {
			slog.Error("Bundle has invalid PEM content", "deployment_id", d.ID, "error", err)
			incomplete = true
			continue
		}

		deployDir := cfg.DeploymentDir(d.ID)
		if err := WriteBundle(deployDir, bundle); err != nil {
			slog.Error("Failed to write bundle", "deployment_id", d.ID, "error", err)
			incomplete = true
			continue
		}

		slog.Info("Cert deployed", "deployment_id", d.ID, "name", d.Name, "dir", deployDir)

		// Update state with new content hash.
		st.DeploymentHashes[d.ID] = bundle.ContentHash

		// Run post-deploy script if configured.
		if depCfg.PostDeploy != "" {
			slog.Info("Running post-deploy script", "deployment_id", d.ID)
			if err := RunPostDeploy(ctx, depCfg.PostDeploy, cfg.Dir(), d.ID, d.Name, deployDir, d.Domains); err != nil {
				slog.Error("Post-deploy script failed", "deployment_id", d.ID, "error", err)
				// Continue — don't block other deployments.
			} else {
				slog.Info("Post-deploy script completed", "deployment_id", d.ID)
			}
		}
	}

	// Save updated config and state only if something changed.
	if configChanged {
		if err := cfg.Save(); err != nil {
			slog.Error("Failed to save config", "error", err)
		}
	}
	if err := st.Save(); err != nil {
		slog.Error("Failed to save state", "error", err)
	}

	return incomplete, nil
}

// ValidateBundle checks that a bundle's PEM data is structurally valid
// (parseable certificates and private key) before writing to disk.
func ValidateBundle(bundle *Bundle) error {
	if err := validateCertPEM(bundle.Fullchain, "fullchain"); err != nil {
		return err
	}
	if err := validateCertPEM(bundle.Cert, "cert"); err != nil {
		return err
	}
	if err := validateKeyPEM(bundle.Key); err != nil {
		return err
	}
	return nil
}

// validateCertPEM checks that data contains at least one valid PEM-encoded certificate.
func validateCertPEM(data string, label string) error {
	block, _ := pem.Decode([]byte(data))
	if block == nil {
		return fmt.Errorf("%s: no PEM block found", label)
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

// validateKeyPEM checks that data contains a valid PEM-encoded private key.
func validateKeyPEM(data string) error {
	block, _ := pem.Decode([]byte(data))
	if block == nil {
		return fmt.Errorf("private key: no PEM block found")
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return nil
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return nil
	}
	if _, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return nil
	}
	return fmt.Errorf("private key: unrecognized or invalid key format")
}
