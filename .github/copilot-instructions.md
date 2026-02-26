# CertKeeper Agent (ceagent) — Developer Guide

## Overview

The CertKeeper Agent (`ceagent`) is a lightweight daemon that runs on remote servers and pulls SSL/TLS certificates from a CertKeeper server. It authenticates using **mTLS (mutual TLS)** — the agent holds an agent certificate signed by CertKeeper's internal CA. This provides strong cryptographic identity without shared secrets, and certificates auto-renew with zero manual intervention after initial setup.

This document contains everything needed to build a compatible agent implementation.

## Architecture

```
┌─────────────────────┐          mTLS           ┌──────────────────────┐
│   CertKeeper Agent  │ ◄──────────────────────► │   CertKeeper Server  │
│   (ceagent)         │   agent cert + HTTPS    │   (central)          │
│                     │                           │                      │
│  - Heartbeats       │                           │  - Signs CSRs        │
│  - Pulls certs      │                           │  - Manages CA        │
│  - Deploys to disk  │                           │  - Tracks agents     │
│  - Auto-renews cert │                           │  - Sends commands    │
└─────────────────────┘                           └──────────────────────┘
```

## Authentication: mTLS

CertKeeper uses mutual TLS for agent authentication. The server runs its own internal Certificate Authority (CA). Each agent gets a unique agent certificate signed by this CA.

**Key properties:**
- Agent private keys are generated locally — they never leave the agent machine
- Agent certs are valid for **45 days** and auto-renew
- The server validates the agent cert's SHA-256 fingerprint against its database
- If an agent is deleted or disabled in CertKeeper, its cert is immediately rejected
- During cert renewal, both old and new certs are accepted until the old one naturally expires

## Registration / Enrollment Flow

### Step 1: Admin creates an agent in CertKeeper UI

The admin creates a new agent. CertKeeper generates a **one-time enrollment token** (prefix `cke_`, 64 hex characters, valid for 1 hour).

### Step 2: Agent enrollment

When the agent starts for the first time, the user provides:
- The CertKeeper server URL (e.g. `https://certkeeper.example.com:3000`)
- The enrollment token (e.g. `cke_a1b2c3d4...`)

The agent then:

1. **Generates a 2048-bit RSA key pair** locally
2. **Creates a PKCS#10 CSR** (Certificate Signing Request) using the key pair
3. **Sends the CSR** to the enrollment endpoint with the token as a Bearer header
4. **Receives back** the signed agent certificate + CA certificate
5. **Saves** the agent cert, private key, and CA cert to its config directory
6. **Sends an initial heartbeat** using the new agent cert (mTLS) to confirm setup
7. The server **burns the enrollment token** — it can never be reused

### Step 3: Ongoing operation

The agent uses its agent certificate for all subsequent API calls. No tokens, no passwords.

---

## CLI Interface

The `ceagent` binary serves as both the daemon and a management CLI. Subcommands:

```
ceagent enroll --server <url> --token <cke_...>   # One-time enrollment
ceagent run                                        # Start the daemon (foreground)
ceagent run --config /etc/ceagent/config.yml       # Start with explicit config
ceagent status                                     # Show agent info, cert expiry, deployment count
ceagent renew-cert                                 # Manually trigger agent cert renewal
ceagent deployments                                # List current deployments and their status
ceagent deploy --id <id>                           # Force re-download a specific deployment
ceagent version                                    # Print version/build info
```

- `ceagent run` starts the heartbeat loop, deployment polling, etc. — the daemon mode
- Other commands are one-shot: load config/certs, perform the action, print results, exit
- Both daemon and one-shot commands share the same internal packages

---

## API Reference

**Base URL:** `https://<certkeeper-host>:<port>/api/agent`

All endpoints except `/enroll` and `/time` require mTLS authentication (agent certificate).

**Request format:** All POST requests must include `Content-Type: application/json` header and a JSON body.

---

### POST /api/agent/enroll

Exchange an enrollment token + CSR for a signed agent certificate.

**Auth:** Bearer token (enrollment token)
```
Authorization: Bearer cke_<enrollment_token>
```

**Request body:**
```json
{
  "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n"
}
```

**Response (200):**
```json
{
  "certificate": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
  "ca_certificate": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
  "fingerprint": "a1b2c3d4e5f6...",
  "expires_at": "2026-04-02 14:30:00",
  "cert_lifetime_days": 45,
  "agent": {
    "id": 1,
    "name": "docker-host-01"
  }
}
```

**Error responses:**
| Status | Meaning |
|--------|---------|
| `400` | Missing or invalid CSR |
| `401` | Invalid or expired enrollment token |
| `403` | Agent is disabled (admin disabled the agent before enrollment completed) |
| `409` | Two possible causes: **(a)** Agent is already enrolled — do not retry, inform the operator (no `retry` field in body). **(b)** Fingerprint collision — retry with a new key pair (`retry: true` in body). |

**CSR subject:** The server ignores the CSR's subject field and sets the agent certificate's subject to `CN=<agent_name>, O=CertKeeper Agent`. The CSR can use any subject — only the public key and signature are extracted.

**After receiving the response:**
1. Save `certificate` to `client.crt`
2. Save `ca_certificate` to `ca.crt`
3. The private key was already generated locally — save it to `client.key`
4. Use these three files for all subsequent mTLS connections

---

### POST /api/agent/renew-cert

Renew the agent's agent certificate before it expires. The agent generates a new key pair, creates a CSR, and sends it authenticated with the current (still valid) agent certificate.

**Auth:** mTLS (current agent certificate)

**Request body:**
```json
{
  "csr": "-----BEGIN CERTIFICATE REQUEST-----\n...\n-----END CERTIFICATE REQUEST-----\n"
}
```

**Response (200):**
```json
{
  "certificate": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
  "ca_certificate": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n",
  "fingerprint": "f6e5d4c3b2a1...",
  "expires_at": "2026-05-17 14:30:00",
  "cert_lifetime_days": 45
}
```

**Error responses:**
| Status | Meaning |
|--------|---------|
| `400` | Missing or invalid CSR, or renewal failed |
| `401` | Current cert is invalid or expired |
| `409` | Fingerprint collision — retry with a new key pair (`retry: true` in body) |

**Renewal process:**
1. Generate a **new** RSA key pair
2. Create a CSR with the new key
3. Call `/api/agent/renew-cert` using the **current** agent cert for mTLS auth
4. On success, atomically swap the old cert + key files with the new ones
5. The server preserves the old fingerprint during a grace period — both certs work until the old one expires

**When to renew:**
- When the current cert has **less than 15 days remaining** (check `cert_expires_at` from heartbeat)
- When the server sends a `renew_agent_cert` action (see Actions below)
- Recommended: check on every heartbeat cycle, comparing `cert_expires_at` against the current server time

**Handling 409 (fingerprint collision):**
If the response includes `"retry": true`, generate a completely new key pair and CSR and try again. This is astronomically unlikely but the server enforces fingerprint uniqueness.

---

### POST /api/agent/heartbeat

Health check / keepalive. The agent **must** call this at the interval specified in the response. The server uses heartbeats to track agent liveness — missed heartbeats trigger offline alerts.

**Auth:** mTLS

**Request body:**
```json
{
  "config_version": 1
}
```

Send the `config_version` value from the **previous** heartbeat response. This tells the server that the agent has acknowledged and applied that config version. On first heartbeat (or if unknown), send `0`.

**Response (200):**
```json
{
  "ok": true,
  "agent": {
    "id": 1,
    "name": "docker-host-01"
  },
  "deployments": 3,
  "deployments_hash": "a1b2c3d4e5f67890",
  "server_time": "2026-02-15T10:30:00.000Z",
  "cert_expires_at": "2026-04-02 14:30:00",
  "heartbeat_interval": 180,
  "config_version": 2,
  "actions": []
}
```

**Response fields:**

| Field | Type | Description |
|-------|------|-------------|
| `ok` | boolean | Always `true` on success |
| `agent.id` | number | Agent's server-side ID |
| `agent.name` | string | Agent's display name |
| `deployments` | number | Total number of deployments assigned to this agent |
| `deployments_hash` | string\|null | SHA-256 hash (16 hex chars) of deployment state (IDs, enabled flags, certificate IDs, and content hashes). `null` if no deployments. Compare with your locally stored value — if different, call `GET /api/agent/deployments` to sync. |
| `server_time` | string | Server's current time (ISO 8601) — use for time-skew detection |
| `cert_expires_at` | string | When the agent's mTLS agent cert expires (`YYYY-MM-DD HH:MM:SS` UTC) |
| `heartbeat_interval` | number | Seconds until the next heartbeat is expected. The agent **must** use this value as its heartbeat timer. |
| `config_version` | number | Latest global config version. Compare with the version you last sent. |
| `actions` | array | Server-initiated commands to execute (see Actions section below) |

**Heartbeat interval handling:**
- The agent should **always** use the server-provided `heartbeat_interval` value, not a local default.
- If `heartbeat_interval` changes between heartbeats, update the timer immediately.
- After applying a config change (detected via `config_version`), send an acknowledgment heartbeat right away — don't wait for the next scheduled one.

**Config version flow:**
1. Receive heartbeat response with `config_version: N`
2. Compare with the version you last acknowledged
3. If different: apply new config (update heartbeat timer to new `heartbeat_interval`), then immediately send another heartbeat with `config_version: N` in the request body
4. If same: normal operation, send `config_version: N` in the next scheduled heartbeat

---

### Actions

The `actions` array in the heartbeat response contains server-initiated commands. Actions are queued by administrators and delivered **exactly once** — the server clears them after including them in a heartbeat response.

| Action | Description | Agent Behavior |
|--------|-------------|----------------|
| `renew_agent_cert` | Admin requested the agent renew its mTLS certificate | Generate a new key pair + CSR, call `POST /api/agent/renew-cert`, swap cert files on success. Do this immediately, regardless of the current cert's remaining lifetime. |
| `update_agent` | Reserved for future self-update functionality | Log the action. No implementation required yet — this is a stub for a future feature where the server can instruct agents to pull and apply a new version of themselves. |

**Processing actions:**
- Process all actions in the array before sleeping for the next heartbeat
- Actions should be processed in order
- If `renew_agent_cert` fails (e.g. network error), the agent should retry on the next heartbeat cycle — the action has already been cleared server-side, so it won't be re-delivered
- Unknown actions should be logged and ignored (forward compatibility)

---

### GET /api/agent/deployments

List all deployments assigned to this agent. Each deployment represents a certificate that should be deployed to this host.

**Auth:** mTLS

**Response (200):**
```json
[
  {
    "id": 1,
    "name": "nginx-proxy",
    "enabled": true,
    "certificate_id": 5,
    "domains": ["example.com", "www.example.com"],
    "cert_status": "active",
    "expires_at": "2026-05-15 00:00:00",
    "issued_at": "2026-02-14 00:00:00",
    "last_renewed_at": null,
    "staging": false,
    "content_hash": "a1b2c3d4e5f67890",
    "last_deployed_at": "2026-02-14 12:00:00",
    "last_deployed_hash": "a1b2c3d4e5f67890"
  }
]
```

**Key fields:**

| Field | Description |
|-------|-------------|
| `id` | Deployment ID — used to download the bundle and as the local folder name |
| `name` | Human-readable name (e.g. "nginx-proxy", "mail-server") |
| `enabled` | Whether this deployment is active — skip disabled deployments |
| `domains` | Array of domain names covered by this certificate |
| `cert_status` | Certificate status — only `active` certs can be downloaded |
| `staging` | Whether this is a Let's Encrypt staging (test) certificate |
| `content_hash` | SHA-256 hash of `fullchain.pem`, truncated to 16 hex chars. Compare with `last_deployed_hash` to detect renewals. `null` if cert is not active. |
| `last_deployed_at` | When this agent last downloaded this deployment's bundle |
| `last_deployed_hash` | The `content_hash` at the time of last download |

**Deployment change detection via `deployments_hash`:**

The heartbeat response includes `deployments_hash` — a hash of all deployment IDs, enabled states, certificate IDs, and cert content hashes. The agent should:
1. Store `deployments_hash` locally after each successful deployment sync
2. On each heartbeat, compare the received `deployments_hash` with the stored value
3. If different (or if the stored value is `null`), call `GET /api/agent/deployments` to get the full list
4. If the same, skip the deployments call entirely — nothing has changed

This detects all changes: new deployments, removed deployments, enabled/disabled toggles, certificate reassignments, and cert renewals. The agent only needs to make the extra API call when something actually changed.

**Polling strategy (after fetching deployments):**
1. For each **enabled** deployment where `cert_status === "active"`:
   - Compare `content_hash` with `last_deployed_hash`
   - If they differ (or `last_deployed_hash` is null), the cert has been renewed — download the bundle
2. Skip deployments where `enabled === false` or `cert_status !== "active"`
3. Handle deployments no longer in the server's list (see Deployment Config Management below)

---

### GET /api/agent/deployments/:id/bundle

Download the certificate + key PEM files for a specific deployment.

**Auth:** mTLS

**Response (200):**
```json
{
  "deployment_id": 1,
  "deployment_name": "nginx-proxy",
  "certificate_id": 5,
  "domains": ["example.com", "www.example.com"],
  "expires_at": "2026-05-15 00:00:00",
  "issued_at": "2026-02-14 00:00:00",
  "content_hash": "a1b2c3d4e5f67890",
  "fullchain": "-----BEGIN CERTIFICATE-----\n...",
  "cert": "-----BEGIN CERTIFICATE-----\n...",
  "key": "-----BEGIN PRIVATE KEY-----\n..."
}
```

| Field | Description |
|-------|-------------|
| `fullchain` | Full certificate chain (leaf + intermediates) — used by most web servers |
| `cert` | Leaf certificate only — some configurations need this separately |
| `key` | Private key |
| `content_hash` | Hash of the fullchain — store this locally for comparison |

**Error responses:**
| Status | Meaning |
|--------|---------|
| `400` | Certificate not yet issued or not in `active` status |
| `403` | Deployment is disabled |
| `404` | Deployment not found or doesn't belong to this agent |

**Side effect:** The server updates `last_deployed_at` and `last_deployed_hash` for this deployment when the bundle is downloaded. This is reflected in subsequent `GET /deployments` responses.

---

### GET /api/agent/time

Server clock endpoint for time-skew detection. **No authentication required.**

**Response (200):**
```json
{
  "server_time": "2026-02-15T10:30:00.000Z"
}
```

The agent should call this during startup (before enrollment or first heartbeat) to detect clock skew. If the agent's local time differs significantly from the server, TLS certificate validation and expiry calculations may fail.

**Recommended behavior:**
- Call on startup, compare with local time
- Log a warning if skew exceeds 30 seconds
- Use server time for cert expiry comparisons (the heartbeat also includes `server_time`)

---

## mTLS Connection Details

When making HTTPS requests to CertKeeper, the agent must configure its HTTP client with:

1. **Agent certificate:** The signed cert received from enrollment (or renewal)
2. **Client private key:** The locally generated private key
3. **CA certificate:** The CertKeeper CA cert (received during enrollment) — used to verify the server if it uses a self-signed TLS certificate

**Example with Go:**
```go
import (
    "crypto/tls"
    "crypto/x509"
    "net/http"
    "os"
)

cert, _ := tls.LoadX509KeyPair("client.crt", "client.key")
caCert, _ := os.ReadFile("ca.crt")
caCertPool := x509.NewCertPool()
caCertPool.AppendCertsFromPEM(caCert)

client := &http.Client{
    Transport: &http.Transport{
        TLSClientConfig: &tls.Config{
            Certificates: []tls.Certificate{cert},
            RootCAs:      caCertPool,
        },
    },
}
```

**Example with curl:**
```bash
curl --cert client.crt --key client.key --cacert ca.crt \
  -X POST -H 'Content-Type: application/json' \
  -d '{"config_version": 0}' \
  https://certkeeper.example.com:3000/api/agent/heartbeat
```

---

## Agent Lifecycle

```
INSTALL & ENROLL (one-time):
  1. ceagent enroll --server <url> --token <cke_...>
  2. Check server time (GET /api/agent/time) — warn if clock skew detected
  3. Generate RSA 2048 key pair locally
  4. Create PKCS#10 CSR
  5. POST /api/agent/enroll (Authorization: Bearer cke_<token>)
  6. Receive signed agent cert + CA cert → save to config dir
  7. Send initial heartbeat to confirm setup
  8. ✅ Ready — token is burned, mTLS is the only auth going forward

HEARTBEAT LOOP (ongoing, every heartbeat_interval seconds):
  1. POST /api/agent/heartbeat — send { config_version: <last_acked_version> }
  2. Receive: heartbeat_interval, config_version, actions, cert_expires_at, deployments_hash
  3. Process actions:
     - "renew_agent_cert" → immediately trigger cert renewal flow
     - "update_agent" → log (stub, no action required yet)
     - unknown actions → log and skip
  4. If config_version changed:
     a. Apply new config (update heartbeat timer to new interval)
     b. Immediately send another heartbeat with new config_version to acknowledge
  5. Check cert_expires_at — if < 15 days remain, trigger cert renewal
  6. If deployments_hash changed → run POLL & DEPLOY
  7. Sleep for heartbeat_interval seconds, then repeat from step 1

POLL & DEPLOY (when deployments_hash differs from local value):
  1. GET /api/agent/deployments — list assigned certificates
  2. For each enabled deployment where content_hash ≠ last_deployed_hash:
     a. GET /api/agent/deployments/:id/bundle — download cert+key
     b. Validate PEM content (parse certs and key) before writing
     c. Write files to deployments/<id>/ (atomic write)
     d. Run post-deploy script if configured
  3. For deployments no longer in the server's list:
     a. Set enabled: false in config.yml (preserve post_deploy script)
     b. Delete cert files from deployments/<id>/
  4. For new deployments not yet in config:
     a. Add entry with enabled: true (no post_deploy script)
  5. Skip disabled deployments and non-active certificates
  6. Store the new deployments_hash locally

CERT RENEWAL (when < 15 days remain on agent cert OR "renew_agent_cert" action):
  1. Generate new RSA 2048 key pair
  2. Create CSR with new key
  3. POST /api/agent/renew-cert (authenticated by current mTLS cert)
  4. On success: atomically swap cert + key files
  5. On 409 with retry:true: generate new key pair and retry
  6. Next request automatically uses the new cert

RE-ENROLLMENT (admin-initiated, when cert is expired or compromised):
  1. Admin clicks "Re-enroll" in CertKeeper UI → gets new enrollment token
  2. Old cert is revoked via CRL and fingerprints are cleared server-side
  3. Agent must re-enroll using the new token (ceagent enroll --server <url> --token <cke_...>)
  4. All deployments are preserved — only the auth cert changes
```

---

## Deployment Config Management

The agent dynamically manages deployment entries in `config.yml`. User customization (e.g. `post_deploy` scripts) is always preserved.

**Rules:**

| Server says | Config has it? | Agent action |
|---|---|---|
| New deployment | No | Add to config with `enabled: true`, no `post_deploy` |
| Existing deployment | Yes, enabled | Normal operation — download/deploy as needed |
| Existing deployment | Yes, disabled | Leave as disabled — user must manually re-enable |
| Deployment missing | Yes, enabled | Set `enabled: false`, **delete cert files** from disk |
| Deployment missing | Yes, disabled | No change — already disabled |

**Key behaviors:**
- The agent never deletes config entries — only flips `enabled` to `false`
- Cert files are deleted when a deployment is removed from the server (security)
- User-configured `post_deploy` scripts persist across disable/re-enable cycles
- Users can manually set `enabled: false` in config to opt out of a deployment locally — the agent respects this and will not auto-re-enable it
- The `ceagent deployments` command shows both enabled and disabled deployments with their status

---

## Error Handling

| HTTP Status | Meaning | Agent Action |
|-------------|---------|--------------|
| `200` | Success | Process response normally |
| `400` | Bad request (invalid CSR, cert not active) | Log error, fix the request — don't retry blindly |
| `401` | Authentication failed | Agent cert not recognized or expired. Log error, alert the operator. Stop heartbeats if persistent. |
| `403` | Agent disabled | The agent has been disabled server-side. Stop polling, log warning, alert the operator. |
| `404` | Not found | Deployment may have been removed. Update local state, remove local cert files if appropriate. |
| `409` | Conflict | During enrollment: agent already has a cert. During cert renewal: fingerprint collision — retry with new key pair if `retry: true`. |
| `500` | Server error | Retry with exponential backoff (e.g. 5s, 10s, 20s, 40s, max 5 min) |

**General retry strategy:**
- Network errors and 5xx: exponential backoff, cap at 5 minutes
- 4xx errors: do not retry (except 409 with `retry: true`)
- If the agent receives persistent 401s, it should enter a "waiting for re-enrollment" state and stop making API calls until the operator intervenes

---

## Configuration

The agent uses `config.yml` for persistent configuration. The agent dynamically updates this file as deployments are added/removed.

```yaml
# config.yml
server_url: "https://certkeeper.example.com:3000"

deployments:
  1:
    enabled: true
    post_deploy: "./scripts/deploy-nginx.sh"
  5:
    enabled: false    # removed from server, agent set this
    post_deploy: "./scripts/deploy-mail.sh"  # preserved
  12:
    enabled: true     # newly added by agent, no script yet
```

**Script paths:** Both relative and absolute paths are supported. Relative paths (e.g. `./scripts/deploy-nginx.sh`) are resolved relative to the config directory (`/etc/ceagent/` on Linux, `C:\ProgramData\ceagent\` on Windows).

**Configuration options:**

| Option | Default | Description |
|--------|---------|-------------|
| `server_url` | *(required)* | CertKeeper server URL (e.g. `https://certkeeper.example.com:3000`) |
| `deployments` | `{}` | Map of deployment ID → config (managed by agent, customizable by user) |

**Platform-specific defaults:**

| Setting | Linux | Windows |
|---------|-------|---------|
| Config/data dir | `/etc/ceagent/` | `C:\ProgramData\ceagent\` |
| Binary location | `/usr/local/bin/ceagent` | `C:\Program Files\ceagent\ceagent.exe` |

**Important:** `heartbeat_interval` is server-authoritative. The agent should always use the value from the latest heartbeat response. The local default (180s) is only for the initial startup before the first successful heartbeat.

---

## File Layout

```
# Linux
/etc/ceagent/
├── config.yml              # Agent configuration (server_url, deployments, hooks)
├── state.json              # Runtime state (config_version, heartbeat_interval, hashes)
├── client.crt              # Agent certificate (from enrollment/renewal)
├── client.key              # Client private key (generated locally, NEVER transmitted)
├── ca.crt                  # CertKeeper CA cert (for server TLS verification)
├── scripts/                # User-created post-deploy scripts (optional)
│   └── deploy-nginx.sh
└── deployments/            # Deployed certificates (one subdirectory per deployment ID)
    ├── 1/
    │   ├── fullchain.pem   # Full chain (leaf + intermediates)
    │   ├── cert.pem        # Leaf certificate only
    │   └── privkey.pem     # Private key
    └── 5/
        ├── fullchain.pem
        ├── cert.pem
        └── privkey.pem

# Windows
C:\ProgramData\ceagent\
├── config.yml
├── state.json
├── client.crt
├── client.key
├── ca.crt
├── scripts\                # User-created post-deploy scripts (optional)
│   └── deploy-iis.ps1
└── deployments\
    ├── 1\
    │   ├── fullchain.pem
    │   ├── cert.pem
    │   └── privkey.pem
    └── 5\
        └── ...
```

**File permissions (Linux):**
- `client.key` and all `privkey.pem` files: `0600` (owner read/write only)
- `client.crt`, `ca.crt`, `fullchain.pem`, `cert.pem`: `0644` (world readable)
- `config.yml`: `0600` (contains server URL)
- `state.json`: `0600`
- Directories: `0755`

**File permissions (Windows):**
- Private key files (`client.key`, `privkey.pem`): ACL restricted to `SYSTEM` and `Administrators`
- Other files: default ACLs

---

## Post-Deploy Scripts

After writing certificate files to disk, the agent runs a user-configurable script per deployment. This is the integration point — the script decides what to do with the certs.

**Configuration in config.yml:**

```yaml
deployments:
  1:
    enabled: true
    post_deploy: "./scripts/deploy-nginx.sh"
  5:
    enabled: true
    post_deploy: ".\\scripts\\deploy-iis.ps1"
```

**Script paths:** Relative paths are resolved relative to the config directory. Absolute paths also work.

**Environment variables passed to the script:**

| Variable | Example | Description |
|----------|---------|-------------|
| `CEAGENT_DEPLOYMENT_ID` | `1` | Deployment ID |
| `CEAGENT_DEPLOYMENT_NAME` | `nginx-proxy` | Human-readable deployment name |
| `CEAGENT_DEPLOYMENT_DIR` | `/etc/ceagent/deployments/1` | Directory containing the cert files |
| `CEAGENT_FULLCHAIN_PATH` | `/etc/ceagent/deployments/1/fullchain.pem` | Full chain file path |
| `CEAGENT_CERT_PATH` | `/etc/ceagent/deployments/1/cert.pem` | Leaf cert file path |
| `CEAGENT_KEY_PATH` | `/etc/ceagent/deployments/1/privkey.pem` | Private key file path |
| `CEAGENT_DOMAINS` | `example.com,www.example.com` | Comma-separated domain list |

**Script execution guidelines:**
- Run only when certificate files have actually changed
- Run as the agent's user (should have appropriate sudo/permissions)
- Log script stdout/stderr for troubleshooting
- A failed script should not prevent deploying other certificates
- Timeout: 30 seconds (configurable)

**Example script (nginx):**
```bash
#!/bin/bash
cp "$CEAGENT_FULLCHAIN_PATH" /etc/nginx/ssl/fullchain.pem
cp "$CEAGENT_KEY_PATH" /etc/nginx/ssl/privkey.pem
systemctl reload nginx
```

---

## Generating Key Pairs and CSRs

The agent generates RSA key pairs and PKCS#10 CSRs using Go's standard `crypto` library. No external dependencies (e.g. OpenSSL) required.

```go
import (
    "crypto/rand"
    "crypto/rsa"
    "crypto/x509"
    "crypto/x509/pkix"
    "encoding/pem"
)

func generateKeyAndCSR() (keyPEM, csrPEM []byte, err error) {
    key, err := rsa.GenerateKey(rand.Reader, 2048)
    if err != nil {
        return nil, nil, err
    }

    csrTemplate := &x509.CertificateRequest{
        Subject: pkix.Name{CommonName: "agent"},
    }
    csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, key)
    if err != nil {
        return nil, nil, err
    }

    keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
    csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
    return keyPEM, csrPEM, nil
}
```

> **Note:** The CSR subject (e.g. `CN=agent`) doesn't matter — the server replaces it with the agent's name from the CertKeeper database. Any valid subject works.

---

## State Persistence

The agent persists runtime state in `state.json` to survive restarts:

| State | Where | Purpose |
|-------|-------|---------|
| `config_version` | `state.json` | Last acknowledged config version — sent in heartbeat requests |
| `heartbeat_interval` | `state.json` | Current server-provided interval — used as initial timer on restart |
| `deployments_hash` | `state.json` | Last known deployments hash from heartbeat — used to detect changes on next heartbeat |
| Per-deployment `content_hash` | `state.json` | Last deployed content hash — used to detect renewals without re-downloading |

Static configuration lives in `config.yml`:

| State | Where | Purpose |
|-------|-------|---------|
| `server_url` | `config.yml` | CertKeeper server URL |
| `deployments` | `config.yml` | Deployment config with enabled flags and post_deploy scripts |

Credentials on disk:

| File | Purpose |
|------|---------|
| `client.crt` | Agent mTLS certificate |
| `client.key` | Agent private key (generated locally, NEVER transmitted) |
| `ca.crt` | CertKeeper CA certificate (for server TLS verification) |

On restart, the agent should:
1. Load saved state (`config_version`, `heartbeat_interval`, `deployments_hash`)
2. Send an immediate heartbeat to re-establish liveness
3. Check deployments for any cert changes that occurred while stopped

---

## Security Considerations

- **Private keys never leave the agent.** Both the agent's mTLS key and all deployed certificate keys stay on the local filesystem. The agent generates its own key pairs locally.
- **Atomic file writes.** Write cert files to a temp file in the same directory, then `rename()` to the final path. This prevents services from reading partial files.
- **File permissions.** Private keys should be `0600`. The agent process should run as a dedicated user (not root if possible), with sudo access only for reloading services.
- **Enrollment token handling.** The enrollment token should be provided interactively or via a secure channel — not stored in config files after use. The token is burned server-side after enrollment and cannot be reused.
- **CA certificate pinning.** After enrollment, the agent has the CA cert and should use it exclusively for server verification (`ca.crt` in the TLS trust chain). This protects against MITM attacks even if the server uses a self-signed TLS certificate.
- **Clock synchronization.** The agent should verify that its system clock is reasonably in sync with the server (via `GET /api/agent/time`). Clock skew can cause TLS handshake failures and incorrect expiry calculations.
- **Cert deletion on removal.** When a deployment is removed from the server, the agent deletes the local cert files immediately. The config entry is preserved (disabled) but no sensitive material remains on disk.

---

## Project Structure

```
certkeeper-agent/
├── .github/
│   ├── copilot-instructions.md
│   └── workflows/
│       ├── build.yml              # CI: test + build binaries
│       └── release.yml            # On tag: build + package + publish
├── cmd/
│   └── ceagent/
│       ├── main.go                # Entry point, root command
│       ├── enroll.go              # ceagent enroll
│       ├── run.go                 # ceagent run (daemon)
│       ├── status.go              # ceagent status
│       ├── renew_cert.go          # ceagent renew-cert
│       ├── deployments.go         # ceagent deployments + ceagent deploy
│       └── version.go             # ceagent version
├── docs/
│   ├── installation.md            # Installation guide (all platforms)
│   ├── uninstall.md               # Uninstall + manual cleanup guide
│   └── post-deploy-scripts.md     # Example post-deploy scripts
├── internal/
│   ├── agent/                     # Core agent loop (heartbeat, actions)
│   ├── enrollment/                # Enrollment flow
│   ├── deployment/                # Cert deployment + hooks
│   ├── mtls/                      # mTLS client, key/CSR generation
│   ├── config/                    # Config loading (config.yml)
│   └── state/                     # State persistence (state.json)
├── packaging/
│   ├── linux/
│   │   ├── ceagent.service        # systemd unit
│   │   ├── nfpm.yaml              # nfpm config for .deb/.rpm
│   │   ├── enroll.sh              # Interactive/silent enrollment script
│   │   ├── postinstall.sh         # Package post-install hook
│   │   └── preremove.sh           # Package pre-remove hook
│   └── windows/
│       ├── ceagent.wxs            # WiX MSI definition
│       ├── enroll.ps1             # Interactive/silent enrollment script
│       └── license.rtf            # License for MSI dialog
├── .gitignore
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```
