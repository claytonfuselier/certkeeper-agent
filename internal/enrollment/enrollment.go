package enrollment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/claytonfuselier/certkeeper-agent/internal/config"
	"github.com/claytonfuselier/certkeeper-agent/internal/mtls"
)

// EnrollRequest is the JSON body sent to POST /api/agent/enroll.
type EnrollRequest struct {
	CSR string `json:"csr"`
}

// EnrollResponse is the JSON response from POST /api/agent/enroll.
type EnrollResponse struct {
	Certificate      string `json:"certificate"`
	CACertificate    string `json:"ca_certificate"`
	Fingerprint      string `json:"fingerprint"`
	ExpiresAt        string `json:"expires_at"`
	CertLifetimeDays int    `json:"cert_lifetime_days"`
	Agent            struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"agent"`
}

// ErrorResponse represents an error response body from the server.
type ErrorResponse struct {
	Error string `json:"error,omitempty"`
	Retry bool   `json:"retry,omitempty"`
}

// TimeResponse is the JSON response from GET /api/agent/time.
type TimeResponse struct {
	ServerTime string `json:"server_time"`
}

// CheckServerTime calls GET /api/agent/time and warns if clock skew exceeds 30 seconds.
func CheckServerTime(serverURL string) error {
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: nil, // use system trust for time check
		},
	}

	resp, err := client.Get(serverURL + "/api/agent/time")
	if err != nil {
		slog.Warn("Could not check server time (if the server uses a self-signed TLS cert, this is expected)", "error", err)
		return nil // Non-fatal — continue with enrollment.
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("Server time check returned non-200", "status", resp.StatusCode)
		return nil
	}

	var timeResp TimeResponse
	if err := json.NewDecoder(resp.Body).Decode(&timeResp); err != nil {
		slog.Warn("Could not parse server time response", "error", err)
		return nil
	}

	serverTime, err := time.Parse(time.RFC3339Nano, timeResp.ServerTime)
	if err != nil {
		serverTime, err = time.Parse(time.RFC3339, timeResp.ServerTime)
		if err != nil {
			slog.Warn("Could not parse server time", "error", err)
			return nil
		}
	}

	skew := time.Since(serverTime).Abs()
	if skew > 30*time.Second {
		slog.Warn("Significant clock skew detected",
			"skew", skew.Round(time.Second),
			"server_time", serverTime.Format(time.RFC3339),
			"local_time", time.Now().Format(time.RFC3339),
		)
	} else {
		slog.Info("Server time check OK", "skew", skew.Round(time.Millisecond))
	}

	return nil
}

// Enroll performs the one-time enrollment flow:
// 1. Check server time
// 2. Generate RSA key pair + CSR
// 3. POST /api/agent/enroll with the token
// 4. Save cert files and config
// 5. Send initial heartbeat
func Enroll(serverURL, token, configDir string) error {
	slog.Info("Starting enrollment", "server", serverURL)

	// Check server time for clock skew.
	CheckServerTime(serverURL)

	// Generate key pair and CSR.
	slog.Info("Generating RSA 2048-bit key pair and CSR")
	keyPEM, csrPEM, err := mtls.GenerateKeyAndCSR()
	if err != nil {
		return fmt.Errorf("generating key/CSR: %w", err)
	}

	// Send enrollment request (with retry on fingerprint collision).
	var enrollResp *EnrollResponse
	var enrollKey []byte = keyPEM
	for attempts := 0; attempts < 3; attempts++ {
		resp, err := sendEnrollRequest(serverURL, token, csrPEM)
		if err != nil {
			if isRetryableCollision(err) {
				slog.Warn("Fingerprint collision during enrollment, generating new key pair")
				enrollKey, csrPEM, err = mtls.GenerateKeyAndCSR()
				if err != nil {
					return fmt.Errorf("generating new key/CSR for retry: %w", err)
				}
				continue
			}
			return err
		}
		enrollResp = resp
		break
	}
	if enrollResp == nil {
		return fmt.Errorf("enrollment failed: max retries exceeded for fingerprint collision")
	}

	slog.Info("Enrollment successful",
		"agent_id", enrollResp.Agent.ID,
		"agent_name", enrollResp.Agent.Name,
		"fingerprint", enrollResp.Fingerprint,
		"expires_at", enrollResp.ExpiresAt,
	)

	// Ensure config directory exists.
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	// Save certificate files.
	certPath := filepath.Join(configDir, "client.crt")
	keyPath := filepath.Join(configDir, "client.key")
	caPath := filepath.Join(configDir, "ca.crt")

	if err := mtls.SaveCertFiles(
		certPath, keyPath, caPath,
		[]byte(enrollResp.Certificate),
		enrollKey,
		[]byte(enrollResp.CACertificate),
	); err != nil {
		return fmt.Errorf("saving cert files: %w", err)
	}

	slog.Info("Certificate files saved", "dir", configDir)

	// Create initial config.yml.
	configPath := filepath.Join(configDir, "config.yml")
	cfg := config.New(serverURL, configPath)
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	slog.Info("Config saved", "path", configPath)

	// Send initial heartbeat to confirm setup.
	if err := sendInitialHeartbeat(serverURL, certPath, keyPath, caPath); err != nil {
		slog.Warn("Initial heartbeat failed — enrollment is complete but the server may not know yet",
			"error", err,
		)
		// Non-fatal: enrollment succeeded, the next daemon heartbeat will confirm.
	} else {
		slog.Info("Initial heartbeat sent successfully")
	}

	return nil
}

// maxResponseSize limits the maximum size of HTTP response bodies (10 MB).
const maxResponseSize = 10 * 1024 * 1024

// sendEnrollRequest sends the CSR to POST /api/agent/enroll.
func sendEnrollRequest(serverURL, token string, csrPEM []byte) (*EnrollResponse, error) {
	reqBody := EnrollRequest{CSR: string(csrPEM)}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling enroll request: %w", err)
	}

	req, err := http.NewRequest("POST", serverURL+"/api/agent/enroll", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating enroll request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending enroll request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("reading enroll response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, handleEnrollError(resp.StatusCode, body)
	}

	var enrollResp EnrollResponse
	if err := json.Unmarshal(body, &enrollResp); err != nil {
		return nil, fmt.Errorf("parsing enroll response: %w", err)
	}

	if enrollResp.Certificate == "" || enrollResp.CACertificate == "" {
		return nil, fmt.Errorf("enrollment response missing certificate data")
	}

	return &enrollResp, nil
}

// handleEnrollError returns a descriptive error for non-200 enrollment responses.
func handleEnrollError(status int, body []byte) error {
	var errResp ErrorResponse
	json.Unmarshal(body, &errResp) // Best-effort parse.

	switch status {
	case http.StatusBadRequest:
		return fmt.Errorf("enrollment failed (400): invalid CSR — %s", errResp.Error)
	case http.StatusUnauthorized:
		return fmt.Errorf("enrollment failed (401): invalid or expired enrollment token")
	case http.StatusForbidden:
		return fmt.Errorf("enrollment failed (403): agent is disabled on the server")
	case http.StatusConflict:
		if errResp.Retry {
			return fmt.Errorf("enrollment failed (409): fingerprint collision — retry with new key pair")
		}
		return fmt.Errorf("enrollment failed (409): agent is already enrolled — do not retry")
	default:
		return fmt.Errorf("enrollment failed (%d): %s", status, string(body))
	}
}

// sendInitialHeartbeat sends a heartbeat using the newly enrolled cert.
func sendInitialHeartbeat(serverURL, certPath, keyPath, caPath string) error {
	client, err := mtls.NewHTTPClient(certPath, keyPath, caPath)
	if err != nil {
		return fmt.Errorf("creating mTLS client: %w", err)
	}

	reqBody, _ := json.Marshal(map[string]int{"config_version": 0})
	req, err := http.NewRequest("POST", serverURL+"/api/agent/heartbeat", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("creating heartbeat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending heartbeat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		return fmt.Errorf("heartbeat returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// isRetryableCollision checks if an enrollment error is a fingerprint collision
// that should be retried with a new key pair.
func isRetryableCollision(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "fingerprint collision")
}
