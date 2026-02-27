# CertKeeper Agent (`ceagent`)

A lightweight daemon that automatically pulls SSL/TLS certificates from a [CertKeeper](https://github.com/claytonfuselier/certkeeper) server and deploys them to the local filesystem.

<br>

## Features

- **Automatic certificate deployment** — polls CertKeeper and deploys new/renewed certificates automatically
- **Post-deploy hooks** — run custom scripts after each deployment (e.g. reload nginx, restart services)
- **mTLS authentication** — agent identity backed by x509 certificates signed by CertKeeper's internal CA
- **Cross-platform** — Linux (.deb, .rpm, tarball) and Windows (MSI installer)
- **Single binary** — no runtime dependencies
- **Interactive & silent install** — GUI/TUI enrollment prompts during install, or pass credentials for fully unattended deployment

<br>

## How It Works

1. **Setup:** Create a new agent record on the CertKeeper server and record the one-time enrollment token.

2. **Enrollment:** The agent generates a local RSA 2048 key pair, sends a CSR to the server with the one-time token, and receives a signed agent certificate valid for 45 days.

2. **Heartbeat loop:** The agent sends periodic heartbeats (server-controlled interval). The heartbeat response includes a `deployments_hash`. If the hash differs from the locally stored value, the agent fetches the full deployment list.

3. **Certificate deployment:** For each assigned deployment with a changed `content_hash`, the agent downloads the certificate bundle, validates the PEM content, and writes `fullchain.pem`, `cert.pem`, and `privkey.pem` to the deployment directory. The agent then runs the relevant post-deploy script if configured.


<br>

## Installation

Download the latest [release](https://github.com/claytonfuselier/certkeeper-agent/releases).

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

<br>

## Quick Start

1. In the CertKeeper web UI, create a new agent. Copy the one-time enrollment token (starts with `cke_`).

2. Use one of the installers above — they handle enrollment interactively. Or enroll manually:

   ```bash
   ceagent enroll --server https://certkeeper.example.com:3000 --token cke_<token>
   ```

3. The installer starts the service automatically on successful enrollment. To start manually:

   ```bash
   # Linux
   sudo systemctl enable --now ceagent

   # Windows
   Start-Service ceagent
   ```

4. The agent heartbeats on the server-provided interval, automatically pulls assigned certificates, and deploys them to disk.

<br>

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

<br>

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

<br>

## Documentation

| Document | Description |
|----------|-------------|
| [Installation Guide](docs/installation.md) | Interactive & silent install for all platforms |
| [Uninstall Guide](docs/uninstall.md) | Standard removal, complete cleanup, and manual recovery |
| [Post-Deploy Script Examples](docs/post-deploy-scripts.md) | Nginx, Apache, IIS, HAProxy, Docker, and more |

<br>

## License

See [LICENSE](LICENSE) for details.
