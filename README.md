# CertKeeper Agent (`ceagent`)

A lightweight daemon that automatically pulls SSL/TLS certificates from a [CertKeeper](https://github.com/claytonfuselier/certkeeper) server and deploys them to the local filesystem. Authenticates using **mTLS** (mutual TLS) — no shared secrets, no tokens after initial enrollment.

## Features

- **mTLS authentication** — agent identity backed by x509 certificates signed by CertKeeper's internal CA
- **Automatic certificate deployment** — polls the server and deploys new/renewed certificates with zero manual intervention
- **Post-deploy hooks** — run custom scripts after each deployment (e.g. reload nginx, restart services)
- **Auto-renewing agent certificate** — the agent's own mTLS cert renews automatically before expiry
- **Cross-platform** — Linux (.deb, .rpm, tarball), Windows (MSI installer)
- **Single binary** — no runtime dependencies
- **Interactive & silent install** — GUI/TUI enrollment prompts during install, or pass credentials for fully unattended deployment

## Installation

Download the latest release from the [Releases](https://github.com/claytonfuselier/certkeeper-agent/releases) page.

| Platform | Package | Install Command |
|----------|---------|-----------------|
| Windows | `.msi` | Double-click or `msiexec /i ceagent_<ver>_windows_amd64.msi` |
| Debian/Ubuntu | `.deb` | `sudo dpkg -i ceagent_<ver>_linux_amd64.deb` |
| RHEL/CentOS/Fedora | `.rpm` | `sudo rpm -i ceagent_<ver>_linux_amd64.rpm` |
| Generic Linux | `.tar.gz` | Extract and copy to `/usr/local/bin/` |

All installers prompt for enrollment during setup. For silent/unattended installs:

```bash
# Linux (.deb / .rpm)
sudo CEAGENT_SERVER_URL=https://certkeeper.example.com:3000 \
     CEAGENT_ENROLLMENT_TOKEN=cke_<token> \
     dpkg -i ceagent_<ver>_linux_amd64.deb

# Windows (MSI)
msiexec /i ceagent.msi SERVER_URL=https://certkeeper.example.com:3000 ENROLLMENT_TOKEN=cke_<token> /quiet
```

See the [Installation Guide](docs/installation.md) for full details including tarball installs and one-liners.

## Quick Start

### 1. Create the agent in CertKeeper

In the CertKeeper web UI, create a new agent. Copy the one-time enrollment token (starts with `cke_`).

### 2. Install & enroll

Use one of the installers above — they handle enrollment interactively. Or enroll manually:

```bash
ceagent enroll --server https://certkeeper.example.com:3000 --token cke_<token>
```

### 3. Start the daemon

The installer starts the service automatically on successful enrollment. To start manually:

```bash
# Linux
sudo systemctl enable --now ceagent

# Windows
Start-Service ceagent
```

The agent heartbeats on the server-provided interval, automatically pulls assigned certificates, and deploys them to disk.

## CLI Reference

```
ceagent enroll --server <url> --token <cke_...>   # One-time enrollment
ceagent run                                        # Start the daemon (foreground)
ceagent run --config /etc/ceagent/config.yml       # Start with explicit config
ceagent status                                     # Show agent info, cert expiry, and live server status
ceagent renew-cert                                 # Manually trigger agent cert renewal
ceagent deployments                                # List assigned deployments
ceagent deploy --id <id>                           # Force re-download a deployment
ceagent version                                    # Print version/build info
```

## Configuration

The agent uses two files for persistent state:

| File | Purpose |
|------|---------|
| `config.yml` | Server URL, per-deployment settings (enabled, post-deploy scripts) |
| `state.json` | Runtime state (config version, heartbeat interval, deployment hashes) |

### Default paths

| | Linux | Windows |
|-|-------|---------|
| Config / data directory | `/etc/ceagent/` | `C:\ProgramData\ceagent\` |
| Binary | `/usr/local/bin/ceagent` | `C:\Program Files\ceagent\ceagent.exe` |

### Example `config.yml`

```yaml
server_url: "https://certkeeper.example.com:3000"

deployments:
  1:
    enabled: true
    post_deploy: "./scripts/deploy-nginx.sh"
  5:
    enabled: true
    post_deploy: "./scripts/deploy-mail.sh"
```

### Post-deploy scripts

After writing certificate files, the agent runs the configured `post_deploy` script with these environment variables:

| Variable | Description |
|----------|-------------|
| `CEAGENT_DEPLOYMENT_ID` | Deployment ID |
| `CEAGENT_DEPLOYMENT_NAME` | Human-readable name |
| `CEAGENT_DEPLOYMENT_DIR` | Directory containing cert files |
| `CEAGENT_FULLCHAIN_PATH` | Path to `fullchain.pem` |
| `CEAGENT_CERT_PATH` | Path to `cert.pem` |
| `CEAGENT_KEY_PATH` | Path to `privkey.pem` |
| `CEAGENT_DOMAINS` | Comma-separated domain list |

## File Layout

```
/etc/ceagent/                   # (Linux default)
├── config.yml                  # Agent configuration
├── state.json                  # Runtime state
├── client.crt                  # Agent mTLS certificate
├── client.key                  # Agent private key (never leaves this machine)
├── ca.crt                      # CertKeeper CA certificate
├── scripts/                    # User-created post-deploy scripts (optional)
│   └── deploy-nginx.sh
└── deployments/
    └── <id>/
        ├── fullchain.pem       # Full certificate chain
        ├── cert.pem            # Leaf certificate
        └── privkey.pem         # Private key
```

## Building

Requires Go 1.25+.

```bash
go build -o ceagent ./cmd/ceagent/
```

With version info:

```bash
go build -ldflags "-X main.version=1.0.0 -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o ceagent ./cmd/ceagent/
```

## How It Works

1. **Enrollment** — The agent generates a local RSA 2048 key pair, sends a CSR to the server with the one-time token, and receives a signed agent certificate valid for 45 days.

2. **Heartbeat loop** — The agent sends periodic heartbeats (server-controlled interval). The heartbeat response includes a `deployments_hash` — if it differs from the locally stored value, the agent fetches the full deployment list and syncs certificates.

3. **Certificate deployment** — For each assigned deployment with a changed `content_hash`, the agent downloads the certificate bundle, validates the PEM content, and writes `fullchain.pem`, `cert.pem`, and `privkey.pem` to the deployment directory, then runs the post-deploy script if configured.

4. **Agent cert renewal** — When the agent certificate has fewer than 15 days remaining (or the server requests it), the agent generates a new key pair and CSR, authenticates with the current cert, and atomically swaps to the new certificate.

## Project Structure

```
certkeeper-agent/
├── cmd/ceagent/          # CLI entry point and subcommands (enroll, run, status, renew-cert, deployments, deploy, version)
├── docs/                 # Documentation (installation, uninstall, post-deploy scripts)
├── internal/
│   ├── agent/            # Core daemon loop (heartbeat, actions, cert renewal)
│   ├── config/           # Config loading/saving (config.yml)
│   ├── deployment/       # Certificate deployment, sync, post-deploy hooks
│   ├── enrollment/       # One-time enrollment flow
│   ├── mtls/             # mTLS client, key/CSR generation
│   └── state/            # Runtime state persistence (state.json)
└── packaging/
    ├── linux/            # systemd unit, nfpm config, enrollment script
    └── windows/          # WiX MSI installer, enrollment script
```

## Documentation

- [Installation Guide](docs/installation.md) — interactive & silent install for all platforms
- [Uninstall Guide](docs/uninstall.md) — standard removal, complete cleanup, and manual recovery
- [Post-Deploy Script Examples](docs/post-deploy-scripts.md) — Nginx, Apache, IIS, HAProxy, Docker, and more

## License

See [LICENSE](LICENSE) for details.
