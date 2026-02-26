package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/claytonfuselier/certkeeper-agent/internal/config"
	"github.com/claytonfuselier/certkeeper-agent/internal/deployment"
	"github.com/claytonfuselier/certkeeper-agent/internal/mtls"
	"github.com/claytonfuselier/certkeeper-agent/internal/state"
)

// RenewalThresholdDays triggers agent cert renewal when fewer than this many days remain.
const RenewalThresholdDays = 15

// HeartbeatRequest is the JSON body sent to POST /api/agent/heartbeat.
type HeartbeatRequest struct {
	ConfigVersion int `json:"config_version"`
}

// HeartbeatResponse is the JSON response from POST /api/agent/heartbeat.
type HeartbeatResponse struct {
	OK                bool    `json:"ok"`
	Agent             AgentInfo `json:"agent"`
	Deployments       int     `json:"deployments"`
	DeploymentsHash   *string `json:"deployments_hash"`
	ServerTime        string  `json:"server_time"`
	CertExpiresAt     string  `json:"cert_expires_at"`
	HeartbeatInterval int     `json:"heartbeat_interval"`
	ConfigVersion     int     `json:"config_version"`
	Actions           []Action `json:"actions"`
}

// AgentInfo contains agent identification from the server.
type AgentInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Action represents a server-initiated command.
type Action struct {
	Type string `json:"type"`
}

// RenewCertRequest is the JSON body sent to POST /api/agent/renew-cert.
type RenewCertRequest struct {
	CSR string `json:"csr"`
}

// RenewCertResponse is the JSON response from POST /api/agent/renew-cert.
type RenewCertResponse struct {
	Certificate      string `json:"certificate"`
	CACertificate    string `json:"ca_certificate"`
	Fingerprint      string `json:"fingerprint"`
	ExpiresAt        string `json:"expires_at"`
	CertLifetimeDays int    `json:"cert_lifetime_days"`
}

// sentinel errors for fatal conditions.
var (
	errAgentUnauthorized = errors.New("agent cert not recognized — re-enrollment required")
	errAgentDisabled     = errors.New("agent is disabled on the server")
)

// Backoff constants for retry logic.
const (
	initialBackoff = 5 * time.Second
	maxBackoff     = 5 * time.Minute
)

// Agent holds the runtime state for the daemon.
type Agent struct {
	cfg    *config.Config
	state  *state.State
	client *http.Client

	// pendingRenewal is true when a renew_agent_cert action was received
	// but the renewal failed and needs to be retried.
	pendingRenewal bool
}

// New creates a new Agent instance.
func New(cfg *config.Config, st *state.State) (*Agent, error) {
	client, err := mtls.NewHTTPClientFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating mTLS client: %w", err)
	}

	return &Agent{
		cfg:    cfg,
		state:  st,
		client: client,
	}, nil
}

// Run starts the main agent loop. Blocks until interrupted or a fatal error occurs.
func (a *Agent) Run(ctx context.Context) error {
	slog.Info("Agent starting",
		"server", a.cfg.ServerURL,
		"heartbeat_interval", a.state.HeartbeatInterval,
	)

	// Set up signal handling for graceful shutdown.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Track consecutive failures for exponential backoff.
	consecutiveFailures := 0

	// Send immediate heartbeat on startup to re-establish liveness.
	if err := a.heartbeatCycle(ctx); err != nil {
		if a.isFatal(err) {
			return err
		}
		slog.Error("Initial heartbeat failed", "error", err)
		consecutiveFailures++
	} else {
		consecutiveFailures = 0
	}

	// Main loop.
	for {
		delay := a.nextDelay(consecutiveFailures)
		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			slog.Info("Agent shutting down")
			return nil
		case <-timer.C:
			if err := a.heartbeatCycle(ctx); err != nil {
				if a.isFatal(err) {
					return err
				}
				consecutiveFailures++
				slog.Error("Heartbeat cycle failed",
					"error", err,
					"consecutive_failures", consecutiveFailures,
					"next_retry_in", a.nextDelay(consecutiveFailures).Round(time.Second),
				)
			} else {
				consecutiveFailures = 0
			}
		}
	}
}

// isFatal returns true if the error indicates the agent should stop running.
func (a *Agent) isFatal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errAgentUnauthorized) {
		slog.Error("Fatal: agent certificate not recognized — waiting for re-enrollment", "error", err)
		return true
	}
	if errors.Is(err, errAgentDisabled) {
		slog.Error("Fatal: agent has been disabled on the server — stopping", "error", err)
		return true
	}
	return false
}

// reloadFromDisk refreshes config, state, and the mTLS client from disk.
// This allows the daemon to pick up changes made by CLI commands
// (e.g., ceagent deploy, ceagent renew-cert) between heartbeat cycles.
func (a *Agent) reloadFromDisk() {
	newCfg, err := config.Load(a.cfg.Path())
	if err != nil {
		slog.Debug("Config reload skipped", "error", err)
	} else {
		a.cfg = newCfg
	}

	newState, err := state.Load(a.state.Path())
	if err != nil {
		slog.Debug("State reload skipped", "error", err)
	} else {
		a.state = newState
	}

	newClient, err := mtls.NewHTTPClientFromConfig(a.cfg)
	if err != nil {
		slog.Debug("mTLS client reload skipped", "error", err)
	} else {
		a.client = newClient
	}
}

// nextDelay calculates the delay before the next heartbeat attempt.
// On success (failures=0), uses the server-provided heartbeat interval.
// On failure, uses exponential backoff capped at maxBackoff.
func (a *Agent) nextDelay(consecutiveFailures int) time.Duration {
	if consecutiveFailures == 0 {
		return time.Duration(a.state.HeartbeatInterval) * time.Second
	}
	// Cap the shift to avoid int64 overflow (5s << 16 = 327680s > maxBackoff).
	shift := consecutiveFailures - 1
	if shift > 16 {
		return maxBackoff
	}
	backoff := initialBackoff << shift
	if backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}

// heartbeatCycle performs a single heartbeat and processes the response.
func (a *Agent) heartbeatCycle(ctx context.Context) error {
	// Reload config/state from disk to pick up CLI changes.
	a.reloadFromDisk()

	resp, err := a.sendHeartbeat(ctx)
	if err != nil {
		return err
	}

	slog.Info("Heartbeat OK",
		"agent", resp.Agent.Name,
		"deployments", resp.Deployments,
		"interval", resp.HeartbeatInterval,
		"config_version", resp.ConfigVersion,
	)

	// Track whether a cert renewal happened this cycle to avoid double renewal.
	renewedThisCycle := false

	// Retry pending renewal from a previous failed attempt (before processing new actions).
	if a.pendingRenewal {
		slog.Info("Retrying pending cert renewal")
		if err := a.renewCert(ctx); err != nil {
			slog.Error("Pending cert renewal retry failed", "error", err)
		} else {
			a.pendingRenewal = false
			renewedThisCycle = true
		}
	}

	// Process actions from this heartbeat.
	if a.processActions(ctx, resp.Actions) {
		renewedThisCycle = true
	}

	// Check if config version changed.
	if resp.ConfigVersion != a.state.ConfigVersion {
		slog.Info("Config version changed",
			"old", a.state.ConfigVersion,
			"new", resp.ConfigVersion,
		)
		a.state.HeartbeatInterval = resp.HeartbeatInterval
		a.state.ConfigVersion = resp.ConfigVersion
		if err := a.state.Save(); err != nil {
			slog.Error("Failed to save state", "error", err)
		}

		// Send acknowledgment heartbeat immediately and process its response.
		slog.Info("Sending config acknowledgment heartbeat")
		ackResp, err := a.sendHeartbeat(ctx)
		if err != nil {
			slog.Error("Ack heartbeat failed", "error", err)
		} else {
			a.processActions(ctx, ackResp.Actions)
		}
	} else {
		// Update heartbeat interval if changed.
		if resp.HeartbeatInterval != a.state.HeartbeatInterval {
			slog.Info("Heartbeat interval updated",
				"old", a.state.HeartbeatInterval,
				"new", resp.HeartbeatInterval,
			)
			a.state.HeartbeatInterval = resp.HeartbeatInterval
			if err := a.state.Save(); err != nil {
				slog.Error("Failed to save state", "error", err)
			}
		}
	}

	// Check agent cert expiry (skip if already renewed this cycle).
	if !renewedThisCycle {
		if err := a.checkCertExpiry(ctx, resp.CertExpiresAt, resp.ServerTime); err != nil {
			slog.Error("Cert expiry check failed", "error", err)
		}
	}

	// Check if deployments changed.
	deploymentsHash := ""
	if resp.DeploymentsHash != nil {
		deploymentsHash = *resp.DeploymentsHash
	}

	if deploymentsHash != a.state.DeploymentsHash {
		slog.Info("Deployments changed, syncing",
			"old_hash", a.state.DeploymentsHash,
			"new_hash", deploymentsHash,
		)

		incomplete, err := deployment.Sync(a.client, a.cfg.ServerURL, a.cfg, a.state)
		if err != nil {
			slog.Error("Deployment sync failed", "error", err)
		} else if incomplete {
			slog.Warn("Deployment sync partially completed — will retry on next hash change")
			// Don't update deploymentsHash so the next heartbeat re-triggers sync.
		} else {
			a.state.DeploymentsHash = deploymentsHash
			if err := a.state.Save(); err != nil {
				slog.Error("Failed to save state", "error", err)
			}
		}
	}

	return nil
}

// maxResponseSize limits the maximum size of HTTP response bodies (10 MB).
const maxResponseSize = 10 * 1024 * 1024

// sendHeartbeat sends POST /api/agent/heartbeat and returns the parsed response.
func (a *Agent) sendHeartbeat(ctx context.Context) (*HeartbeatResponse, error) {
	reqBody := HeartbeatRequest{ConfigVersion: a.state.ConfigVersion}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling heartbeat: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.cfg.ServerURL+"/api/agent/heartbeat", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating heartbeat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending heartbeat: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("reading heartbeat response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errAgentUnauthorized
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, errAgentDisabled
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("heartbeat returned %d: %s", resp.StatusCode, string(body))
	}

	var hbResp HeartbeatResponse
	if err := json.Unmarshal(body, &hbResp); err != nil {
		return nil, fmt.Errorf("parsing heartbeat response: %w", err)
	}

	return &hbResp, nil
}

// processActions handles server-initiated actions from the heartbeat response.
// Returns true if a cert renewal was successfully performed.
func (a *Agent) processActions(ctx context.Context, actions []Action) bool {
	renewed := false
	for _, action := range actions {
		switch action.Type {
		case "renew_agent_cert":
			slog.Info("Processing action: renew_agent_cert")
			if err := a.renewCert(ctx); err != nil {
				slog.Error("Agent cert renewal failed (will retry next cycle)", "error", err)
				a.pendingRenewal = true
			} else {
				a.pendingRenewal = false
				renewed = true
			}
		case "update_agent":
			slog.Info("Processing action: update_agent (stub — not yet implemented)")
		default:
			slog.Warn("Unknown action, skipping", "action", action.Type)
		}
	}
	return renewed
}

// checkCertExpiry checks if the agent cert is approaching expiry and renews if needed.
func (a *Agent) checkCertExpiry(ctx context.Context, certExpiresAt, serverTime string) error {
	expiry, err := parseTime(certExpiresAt)
	if err != nil {
		return fmt.Errorf("parsing cert_expires_at: %w", err)
	}

	now := time.Now()
	if serverTime != "" {
		if st, err := parseTime(serverTime); err == nil {
			now = st
		}
	}

	daysRemaining := expiry.Sub(now).Hours() / 24
	slog.Debug("Agent cert expiry check",
		"expires_at", certExpiresAt,
		"days_remaining", fmt.Sprintf("%.1f", daysRemaining),
	)

	if daysRemaining < RenewalThresholdDays {
		slog.Info("Agent cert approaching expiry, triggering renewal",
			"days_remaining", fmt.Sprintf("%.1f", daysRemaining),
		)
		return a.renewCert(ctx)
	}

	return nil
}

// renewCert generates a new key pair and CSR, calls POST /api/agent/renew-cert,
// and atomically swaps the cert files on success.
func (a *Agent) renewCert(ctx context.Context) error {
	slog.Info("Generating new key pair for cert renewal")

	keyPEM, csrPEM, err := mtls.GenerateKeyAndCSR()
	if err != nil {
		return fmt.Errorf("generating key/CSR: %w", err)
	}

	renewResp, actualKey, err := SendRenewRequest(ctx, a.client, a.cfg.ServerURL, keyPEM, csrPEM)
	if err != nil {
		return err
	}

	// Atomically swap cert files.
	if err := mtls.SaveCertFiles(
		a.cfg.ClientCertPath(),
		a.cfg.ClientKeyPath(),
		a.cfg.CACertPath(),
		[]byte(renewResp.Certificate),
		actualKey,
		[]byte(renewResp.CACertificate),
	); err != nil {
		return fmt.Errorf("saving renewed cert files: %w", err)
	}

	slog.Info("Agent cert renewed successfully",
		"fingerprint", renewResp.Fingerprint,
		"expires_at", renewResp.ExpiresAt,
	)

	// Recreate the HTTP client with the new cert.
	newClient, err := mtls.NewHTTPClientFromConfig(a.cfg)
	if err != nil {
		return fmt.Errorf("recreating mTLS client: %w", err)
	}
	a.client = newClient

	return nil
}

// SendRenewRequest sends POST /api/agent/renew-cert.
// Returns the response and the private key that matches the issued certificate.
// Handles 409 with retry:true by generating a new key pair (at most once).
func SendRenewRequest(ctx context.Context, client *http.Client, serverURL string, keyPEM, csrPEM []byte) (*RenewCertResponse, []byte, error) {
	activeKey, activeCSR := keyPEM, csrPEM

	for attempt := 0; attempt < 2; attempt++ {
		reqBody := RenewCertRequest{CSR: string(activeCSR)}
		bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return nil, nil, fmt.Errorf("marshaling renew request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", serverURL+"/api/agent/renew-cert", bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, nil, fmt.Errorf("creating renew request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("sending renew request: %w", err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("reading renew response: %w", err)
		}

		if resp.StatusCode == http.StatusConflict {
			var errResp struct {
				Retry bool `json:"retry"`
			}
			json.Unmarshal(body, &errResp)
			if errResp.Retry && attempt == 0 {
				slog.Warn("Fingerprint collision during renewal, retrying with new key pair")
				newKey, newCSR, err := mtls.GenerateKeyAndCSR()
				if err != nil {
					return nil, nil, fmt.Errorf("generating new key/CSR for retry: %w", err)
				}
				activeKey, activeCSR = newKey, newCSR
				continue
			}
			return nil, nil, fmt.Errorf("cert renewal conflict (409): %s", string(body))
		}

		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("cert renewal returned %d: %s", resp.StatusCode, string(body))
		}

		var renewResp RenewCertResponse
		if err := json.Unmarshal(body, &renewResp); err != nil {
			return nil, nil, fmt.Errorf("parsing renew response: %w", err)
		}

		return &renewResp, activeKey, nil
	}

	return nil, nil, fmt.Errorf("cert renewal failed: max retries exceeded for fingerprint collision")
}

// parseTime attempts to parse a time string in multiple formats.
// All parsed times are treated as UTC (the CertKeeper server sends UTC timestamps).
func parseTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("could not parse time: %s", s)
}

// Status holds information for the ceagent status command.
type Status struct {
	ServerURL       string
	AgentCertExpiry time.Time
	AgentCertPath   string
	ConfigVersion   int
	HeartbeatInterval int
	DeploymentsHash string
	DeploymentCount int
}

// GetStatus loads current agent status information.
func GetStatus(cfg *config.Config, st *state.State) (*Status, error) {
	status := &Status{
		ServerURL:         cfg.ServerURL,
		AgentCertPath:     cfg.ClientCertPath(),
		ConfigVersion:     st.ConfigVersion,
		HeartbeatInterval: st.HeartbeatInterval,
		DeploymentsHash:   st.DeploymentsHash,
		DeploymentCount:   len(cfg.Deployments),
	}

	// Try to read cert expiry from the certificate file.
	certPEM, err := os.ReadFile(cfg.ClientCertPath())
	if err == nil {
		if expiry, err := parseCertExpiry(certPEM); err == nil {
			status.AgentCertExpiry = expiry
		}
	}

	return status, nil
}

// FetchLiveStatus sends a heartbeat to retrieve current server-side information.
func FetchLiveStatus(cfg *config.Config, st *state.State) (*HeartbeatResponse, error) {
	client, err := mtls.NewHTTPClientFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating mTLS client: %w", err)
	}

	reqBody := HeartbeatRequest{ConfigVersion: st.ConfigVersion}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling heartbeat: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.ServerURL+"/api/agent/heartbeat", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting server: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var hbResp HeartbeatResponse
	if err := json.Unmarshal(body, &hbResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &hbResp, nil
}
