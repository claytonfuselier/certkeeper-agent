# Installation Guide

The CertKeeper Agent (`ceagent`) can be installed via **MSI** (Windows), **.deb/.rpm** packages (Linux), or a **tarball** (generic Linux). All methods install the same binary, enroll the agent with your CertKeeper server, and configure a background service.

---

## Prerequisites

- A running [CertKeeper server](https://github.com/claytonfuselier/certkeeper) (accessible over HTTPS)
- An agent created in the CertKeeper web UI with an enrollment token ready (see [CertKeeper docs — Agents](https://github.com/claytonfuselier/certkeeper/blob/main/docs/api.md#agents-admin))
- Root / Administrator privileges on the target machine
- Network connectivity to the CertKeeper server (the agent must be able to reach it on the configured port)

## Supported Platforms

| Platform | Architectures | Package Formats |
|----------|---------------|-----------------|
| Windows 10+ / Server 2016+ | amd64, arm64 | `.msi` |
| Debian / Ubuntu | amd64, arm64 | `.deb` |
| RHEL / CentOS / Fedora | amd64, arm64 | `.rpm` |
| Generic Linux | amd64, arm64 | `.tar.gz` |

All packages are available on the [Releases](https://github.com/claytonfuselier/certkeeper-agent/releases) page.

## Windows

### Interactive Install (MSI)

1. Download the `.msi` file for your architecture from the [Releases](https://github.com/claytonfuselier/certkeeper-agent/releases) page.

2. Double-click the MSI or run:
   ```powershell
   msiexec /i ceagent_1.0.0_windows_amd64.msi
   ```

3. The installer will walk you through:
   - **License Agreement** — review and accept
   - **Enrollment** — enter your CertKeeper server URL and enrollment token
   - **Install** — files are installed and enrollment is attempted

4. If enrollment fails, a dialog will appear showing the error message. You can:
   - **Edit** the server URL and token and click **Enroll** to retry
   - **Skip** enrollment to complete the install without enrolling (enroll later via CLI)

5. On success, the `ceagent` Windows service is set to auto-start and started immediately.

### Silent Install (MSI)

For automated deployments, pass the server URL and token as MSI properties:

```powershell
msiexec /i ceagent_1.0.0_windows_amd64.msi SERVER_URL=https://certkeeper.example.com:3000 ENROLLMENT_TOKEN=cke_a1b2c3d4... /quiet
```

`/quiet` suppresses all UI. The installer will:
1. Install files to `C:\Program Files\ceagent\`
2. Create `C:\ProgramData\ceagent\` and `deployments\` subdirectory
3. Register the `ceagent` Windows service
4. Attempt enrollment silently — on success, set service to auto-start and start it
5. Add `C:\Program Files\ceagent\` to the system PATH

If enrollment fails during silent install, the install still completes. The binary is on PATH and the service is registered (manual start). Enroll manually later:

```powershell
ceagent enroll --server https://certkeeper.example.com:3000 --token cke_<token>
sc config ceagent start=auto
Start-Service ceagent
```

### What the MSI Installs

| Item | Location |
|------|----------|
| `ceagent.exe` | `C:\Program Files\ceagent\` |
| `enroll.ps1` | `C:\Program Files\ceagent\` |
| Data directory | `C:\ProgramData\ceagent\` |
| Deployments directory | `C:\ProgramData\ceagent\deployments\` |
| Windows service | `ceagent` (DisplayName: "CertKeeper Agent") |
| System PATH entry | `C:\Program Files\ceagent\` |

---

## Debian / Ubuntu (.deb)

### Interactive Install

```bash
sudo dpkg -i ceagent_1.0.0_linux_amd64.deb
```

The post-install script will prompt you for the CertKeeper server URL and enrollment token interactively. If enrollment succeeds, the systemd service is enabled and started automatically.

If enrollment fails, you'll see the error and be given the option to:
- **r** — Retry with new values
- **s** — Skip and enroll later
- **q** — Quit (install still completes)

### Silent Install

Pass the server URL and token as environment variables:

```bash
sudo CEAGENT_SERVER_URL=https://certkeeper.example.com:3000 \
     CEAGENT_ENROLLMENT_TOKEN=cke_a1b2c3d4... \
     dpkg -i ceagent_1.0.0_linux_amd64.deb
```

In silent mode, enrollment is attempted once. If it fails, the install completes and the error is printed. Enroll manually later:

```bash
sudo ceagent enroll --server https://certkeeper.example.com:3000 --token cke_<token>
sudo systemctl enable --now ceagent
```

---

## Red Hat / CentOS / Fedora (.rpm)

### Interactive Install

```bash
sudo rpm -i ceagent_1.0.0_linux_amd64.rpm
```

Or with `dnf`:

```bash
sudo dnf install ./ceagent_1.0.0_linux_amd64.rpm
```

Enrollment behavior is identical to the .deb install — interactive prompts with retry.

### Silent Install

```bash
sudo CEAGENT_SERVER_URL=https://certkeeper.example.com:3000 \
     CEAGENT_ENROLLMENT_TOKEN=cke_a1b2c3d4... \
     rpm -i ceagent_1.0.0_linux_amd64.rpm
```

---

## Tarball (Generic Linux)

For distributions without .deb/.rpm support, or when you want manual control.

### Interactive Install

```bash
# Download and extract
tar xzf ceagent_1.0.0_linux_amd64.tar.gz

# Install the binary
sudo cp ceagent /usr/local/bin/
sudo chmod 755 /usr/local/bin/ceagent

# Create data directories
sudo mkdir -p /etc/ceagent/deployments

# Install the enrollment script
sudo cp ceagent-enroll /usr/local/bin/
sudo chmod 755 /usr/local/bin/ceagent-enroll

# Install the systemd service (if using systemd)
sudo cp ceagent.service /etc/systemd/system/
sudo systemctl daemon-reload

# Enroll interactively
sudo ceagent-enroll

# Or enroll directly
sudo ceagent enroll --server https://certkeeper.example.com:3000 --token cke_<token>

# Enable and start the service
sudo systemctl enable --now ceagent
```

### Silent Install

```bash
tar xzf ceagent_1.0.0_linux_amd64.tar.gz

sudo cp ceagent /usr/local/bin/
sudo mkdir -p /etc/ceagent/deployments

sudo CEAGENT_SERVER_URL=https://certkeeper.example.com:3000 \
     CEAGENT_ENROLLMENT_TOKEN=cke_a1b2c3d4... \
     ceagent-enroll

sudo cp ceagent.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ceagent
```

### One-Liner (Download + Install + Enroll)

Replace `VERSION` and `ARCH` (`amd64` or `arm64`) with the appropriate values:

```bash
VERSION=1.0.0 ARCH=amd64 && \
  curl -sL "https://github.com/claytonfuselier/certkeeper-agent/releases/download/v${VERSION}/ceagent_${VERSION}_linux_${ARCH}.tar.gz" | \
  sudo tar xzf - -C /usr/local/bin ceagent && \
  sudo mkdir -p /etc/ceagent/deployments && \
  sudo ceagent enroll --server https://certkeeper.example.com:3000 --token cke_<token> && \
  echo "Enrollment successful"
```

---

## Post-Install Verification

After installation and enrollment, verify the agent is working:

```bash
# Check agent status
ceagent status

# Check service status (Linux)
systemctl status ceagent

# Check service status (Windows)
Get-Service ceagent
```

You should see:
- Server URL and config file path
- Agent certificate expiry date (45 days from enrollment) with days remaining
- Config version and heartbeat interval
- Number of deployments assigned and deployments hash
- Live server data: agent name/ID, server-side deployment count, server cert expiry, and server time

## File Layout

### Linux

```
/usr/local/bin/ceagent           # Main binary
/usr/local/bin/ceagent-enroll    # Enrollment helper script
/etc/systemd/system/ceagent.service  # systemd unit
/etc/ceagent/
├── config.yml                   # Configuration
├── state.json                   # Runtime state
├── client.crt                   # Agent mTLS certificate
├── client.key                   # Agent private key
├── ca.crt                       # CertKeeper CA certificate
└── deployments/
    └── <id>/
        ├── fullchain.pem
        ├── cert.pem
        └── privkey.pem
```

### Windows

```
C:\Program Files\ceagent\
├── ceagent.exe                  # Main binary
└── enroll.ps1                   # Enrollment helper script

C:\ProgramData\ceagent\
├── config.yml
├── state.json
├── client.crt
├── client.key
├── ca.crt
└── deployments\
    └── <id>\
        ├── fullchain.pem
        ├── cert.pem
        └── privkey.pem
```

## Enrollment Token Notes

- Tokens are prefixed with `cke_` and contain 64 hex characters
- Each token is **single-use** — it's burned after successful enrollment
- Tokens expire after **1 hour**
- If enrollment fails with an expired or invalid token, create a new agent (or re-enroll the existing one) in the CertKeeper web UI to get a fresh token

---

## Updating

### Windows (MSI)

Download the new `.msi` and install over the existing installation:

```powershell
msiexec /i ceagent_1.1.0_windows_amd64.msi /quiet
```

The installer upgrades the binary in place. The service is restarted automatically. Configuration, certificates, and state are preserved.

### Debian / Ubuntu (.deb)

```bash
sudo dpkg -i ceagent_1.1.0_linux_amd64.deb
```

The package upgrade replaces the binary and restarts the service. Enrollment is skipped if the agent is already enrolled.

### Red Hat / CentOS / Fedora (.rpm)

```bash
sudo rpm -U ceagent_1.1.0_linux_amd64.rpm
```

Or with `dnf`:

```bash
sudo dnf upgrade ./ceagent_1.1.0_linux_amd64.rpm
```

### Tarball (Generic Linux)

```bash
# Stop the service
sudo systemctl stop ceagent

# Replace the binary
tar xzf ceagent_1.1.0_linux_amd64.tar.gz
sudo cp ceagent /usr/local/bin/

# Restart
sudo systemctl start ceagent
```

Data, configuration, and certificates are preserved across all upgrade methods.

---

## Troubleshooting

### Enrollment fails with "invalid or expired enrollment token"

Tokens expire after 1 hour. Create a new agent (or click **Re-enroll** on an existing one) in the CertKeeper web UI to generate a fresh token.

### Enrollment fails with TLS errors

If the CertKeeper server uses a self-signed TLS certificate, the agent cannot verify its identity during enrollment. Switch the server to a managed or custom TLS certificate before enrolling agents. See [CertKeeper docs — TLS Configuration](https://github.com/claytonfuselier/certkeeper/blob/main/docs/configuration.md#tls-configuration).

### Agent shows "agent cert not recognized — re-enrollment required"

The agent's mTLS certificate is no longer accepted by the server. This happens when:
- The agent was deleted and re-created on the server
- The admin clicked **Re-enroll** (which revokes the old cert)
- The agent cert expired (45-day lifetime) without renewal

Re-enroll with a new token:

```bash
sudo ceagent enroll --server https://certkeeper.example.com:3000 --token cke_<new_token>
sudo systemctl restart ceagent
```

### Clock skew warning during enrollment

The agent checks the server's time during enrollment. A skew exceeding 30 seconds is logged as a warning. Large time differences can cause TLS handshake failures. Sync the system clock with NTP:

```bash
# Linux
sudo timedatectl set-ntp true

# Windows
w32tm /resync
```

### Service fails to start

Check the logs:

```bash
# Linux
sudo journalctl -u ceagent -n 50

# Windows
Get-EventLog -LogName Application -Source ceagent -Newest 20
```

Common causes:
- Missing `config.yml` — the agent hasn't been enrolled yet
- Missing or corrupted cert files (`client.crt`, `client.key`, `ca.crt`) — re-enroll
- Server unreachable — check network connectivity and firewall rules

### Agent is "offline" on the server

The server marks agents offline after missing multiple heartbeats. Check:
1. The service is running (`systemctl status ceagent` or `Get-Service ceagent`)
2. Network connectivity to the server (`ceagent status` shows live server data)
3. The agent cert hasn't expired (`ceagent status` shows cert expiry)
