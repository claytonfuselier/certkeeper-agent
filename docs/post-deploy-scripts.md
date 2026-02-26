# Post-Deploy Script Examples

After deploying a certificate, ceagent runs the configured `post_deploy` script for that deployment. This is how you integrate ceagent with your web server, mail server, or any other service that uses TLS certificates.

## How It Works

1. ceagent downloads new certificate files to `/etc/ceagent/deployments/<id>/`
2. ceagent runs your `post_deploy` script with environment variables set
3. Your script copies the certs where they need to go and reloads the service

## Available Environment Variables

| Variable | Example | Description |
|----------|---------|-------------|
| `CEAGENT_DEPLOYMENT_ID` | `1` | Deployment ID |
| `CEAGENT_DEPLOYMENT_NAME` | `nginx-proxy` | Human-readable name |
| `CEAGENT_DEPLOYMENT_DIR` | `/etc/ceagent/deployments/1` | Directory with cert files |
| `CEAGENT_FULLCHAIN_PATH` | `/etc/ceagent/deployments/1/fullchain.pem` | Full chain (leaf + intermediates) |
| `CEAGENT_CERT_PATH` | `/etc/ceagent/deployments/1/cert.pem` | Leaf certificate only |
| `CEAGENT_KEY_PATH` | `/etc/ceagent/deployments/1/privkey.pem` | Private key |
| `CEAGENT_DOMAINS` | `example.com,www.example.com` | Comma-separated list |

## Configuration

Add scripts to your `config.yml`:

```yaml
server_url: "https://certkeeper.example.com:3000"

deployments:
  1:
    enabled: true
    post_deploy: "./scripts/deploy-nginx.sh"
  2:
    enabled: true
    post_deploy: "./scripts/deploy-apache.sh"
  3:
    enabled: true
    post_deploy: ".\\scripts\\deploy-iis.ps1"
```

Relative paths (e.g. `./scripts/deploy-nginx.sh`) are resolved relative to the config directory (`/etc/ceagent/` on Linux, `C:\ProgramData\ceagent\` on Windows). Absolute paths also work.

---

## Nginx

### Basic — Copy and Reload

```bash
#!/bin/bash
# scripts/deploy-nginx.sh

set -e

cp "$CEAGENT_FULLCHAIN_PATH" /etc/nginx/ssl/fullchain.pem
cp "$CEAGENT_KEY_PATH" /etc/nginx/ssl/privkey.pem

# Test config before reloading
nginx -t

# Graceful reload — no downtime
systemctl reload nginx
```

### Multi-Site — Use Deployment Name as Identifier

```bash
#!/bin/bash
# scripts/deploy-nginx-multi.sh
# Works when deployment names match your nginx site config names.

set -e

SITE_DIR="/etc/nginx/ssl/${CEAGENT_DEPLOYMENT_NAME}"
mkdir -p "$SITE_DIR"

cp "$CEAGENT_FULLCHAIN_PATH" "$SITE_DIR/fullchain.pem"
cp "$CEAGENT_KEY_PATH" "$SITE_DIR/privkey.pem"
chmod 600 "$SITE_DIR/privkey.pem"

nginx -t && systemctl reload nginx
```

### Nginx Proxy Manager (Docker)

```bash
#!/bin/bash
# scripts/deploy-npm.sh
# For Nginx Proxy Manager running in Docker.

set -e

NPM_CERT_DIR="/data/nginx-proxy-manager/custom_ssl/${CEAGENT_DEPLOYMENT_NAME}"
mkdir -p "$NPM_CERT_DIR"

cp "$CEAGENT_FULLCHAIN_PATH" "$NPM_CERT_DIR/fullchain.pem"
cp "$CEAGENT_KEY_PATH" "$NPM_CERT_DIR/privkey.pem"

docker exec nginx-proxy-manager nginx -s reload
```

---

## Apache

### Basic — Copy and Reload

```bash
#!/bin/bash
# scripts/deploy-apache.sh

set -e

cp "$CEAGENT_FULLCHAIN_PATH" /etc/apache2/ssl/fullchain.pem
cp "$CEAGENT_KEY_PATH" /etc/apache2/ssl/privkey.pem
chmod 600 /etc/apache2/ssl/privkey.pem

# Test config
apachectl configtest

# Graceful restart
systemctl reload apache2
```

### RHEL / CentOS (httpd)

```bash
#!/bin/bash
# scripts/deploy-httpd.sh

set -e

cp "$CEAGENT_FULLCHAIN_PATH" /etc/pki/tls/certs/fullchain.pem
cp "$CEAGENT_KEY_PATH" /etc/pki/tls/private/privkey.pem
chmod 600 /etc/pki/tls/private/privkey.pem

# SELinux context (if enforcing)
restorecon -v /etc/pki/tls/certs/fullchain.pem 2>/dev/null || true
restorecon -v /etc/pki/tls/private/privkey.pem 2>/dev/null || true

httpd -t && systemctl reload httpd
```

---

## IIS (Windows)

### Import PFX and Bind to Site

```powershell
# scripts\deploy-iis.ps1
# Imports the certificate into the Windows certificate store and binds it to an IIS site.

$ErrorActionPreference = "Stop"

# Convert PEM to PFX (IIS needs PFX format)
$pfxPath = Join-Path $env:CEAGENT_DEPLOYMENT_DIR "cert.pfx"
$password = [System.Guid]::NewGuid().ToString()

# Use OpenSSL to create PFX (must be in PATH or provide full path)
& openssl pkcs12 -export `
    -in $env:CEAGENT_FULLCHAIN_PATH `
    -inkey $env:CEAGENT_KEY_PATH `
    -out $pfxPath `
    -passout "pass:$password"

# Import into the Local Machine certificate store
$securePassword = ConvertTo-SecureString $password -AsPlainText -Force
$cert = Import-PfxCertificate -FilePath $pfxPath `
    -CertStoreLocation Cert:\LocalMachine\My `
    -Password $securePassword

# Bind to IIS site (change "Default Web Site" to your site name)
Import-Module WebAdministration
$siteName = "Default Web Site"
$binding = Get-WebBinding -Name $siteName -Protocol "https"
if ($binding) {
    $binding.AddSslCertificate($cert.Thumbprint, "My")
}

# Clean up the temporary PFX
Remove-Item $pfxPath -Force -ErrorAction SilentlyContinue

Write-Host "Certificate bound to IIS site '$siteName' (thumbprint: $($cert.Thumbprint))"
```

### IIS — Simple Binding Update (Cert Already in Store)

```powershell
# scripts\deploy-iis-simple.ps1
# Simpler approach if you use netsh directly.

$ErrorActionPreference = "Stop"

# Create PFX
$pfxPath = Join-Path $env:CEAGENT_DEPLOYMENT_DIR "cert.pfx"
& openssl pkcs12 -export -in $env:CEAGENT_FULLCHAIN_PATH -inkey $env:CEAGENT_KEY_PATH -out $pfxPath -passout "pass:temp123"

$securePassword = ConvertTo-SecureString "temp123" -AsPlainText -Force
$cert = Import-PfxCertificate -FilePath $pfxPath -CertStoreLocation Cert:\LocalMachine\My -Password $securePassword

# Update HTTPS binding on port 443
netsh http delete sslcert ipport=0.0.0.0:443 2>$null
netsh http add sslcert ipport=0.0.0.0:443 certhash=$($cert.Thumbprint) appid="{4dc3e181-e14b-4a21-b022-59fc669b0914}"

# Clean up old certs (keep only the new one)
$domain = ($env:CEAGENT_DOMAINS -split ",")[0]
Get-ChildItem Cert:\LocalMachine\My | Where-Object {
    $_.Subject -like "*$domain*" -and $_.Thumbprint -ne $cert.Thumbprint
} | Remove-Item -Force -ErrorAction SilentlyContinue

Remove-Item $pfxPath -Force -ErrorAction SilentlyContinue
Write-Host "Certificate updated for port 443"
```

---

## HAProxy

```bash
#!/bin/bash
# scripts/deploy-haproxy.sh
# HAProxy uses a combined PEM file (cert + key in one file).

set -e

COMBINED="/etc/haproxy/certs/${CEAGENT_DEPLOYMENT_NAME}.pem"

# Concatenate fullchain + key into a single PEM
cat "$CEAGENT_FULLCHAIN_PATH" "$CEAGENT_KEY_PATH" > "$COMBINED"
chmod 600 "$COMBINED"

# Graceful reload (no dropped connections)
systemctl reload haproxy
```

---

## Postfix (Mail Server)

```bash
#!/bin/bash
# scripts/deploy-postfix.sh

set -e

cp "$CEAGENT_FULLCHAIN_PATH" /etc/postfix/ssl/fullchain.pem
cp "$CEAGENT_KEY_PATH" /etc/postfix/ssl/privkey.pem
chmod 600 /etc/postfix/ssl/privkey.pem

# Postfix reads certs on each new connection, but reload ensures config is re-read
systemctl reload postfix
```

---

## Dovecot (IMAP/POP3)

```bash
#!/bin/bash
# scripts/deploy-dovecot.sh

set -e

cp "$CEAGENT_FULLCHAIN_PATH" /etc/dovecot/private/fullchain.pem
cp "$CEAGENT_KEY_PATH" /etc/dovecot/private/privkey.pem
chmod 600 /etc/dovecot/private/privkey.pem

# Dovecot needs a full restart to pick up new certs
systemctl restart dovecot
```

---

## Docker Containers (Generic)

### Copy Into Container

```bash
#!/bin/bash
# scripts/deploy-docker.sh
# Generic approach: copy certs into a running container.

set -e

CONTAINER_NAME="my-web-app"

docker cp "$CEAGENT_FULLCHAIN_PATH" "$CONTAINER_NAME:/etc/ssl/certs/fullchain.pem"
docker cp "$CEAGENT_KEY_PATH" "$CONTAINER_NAME:/etc/ssl/private/privkey.pem"

# Signal the container to reload (depends on the application)
docker exec "$CONTAINER_NAME" nginx -s reload
```

### Bind-Mount Volume

If you mount `/etc/ceagent/deployments/<id>` directly into the container, no copy is needed — just reload:

```bash
#!/bin/bash
# scripts/deploy-docker-reload.sh
# Assumes the deployment directory is bind-mounted into the container.

set -e

docker exec my-web-app nginx -s reload
```

```yaml
# docker-compose.yml
services:
  web:
    image: nginx
    volumes:
      - /etc/ceagent/deployments/1:/etc/nginx/ssl:ro
```

---

## Traefik

Traefik watches the filesystem for cert changes, so the post-deploy script only needs to copy files:

```bash
#!/bin/bash
# scripts/deploy-traefik.sh

set -e

TRAEFIK_CERTS="/etc/traefik/certs"
mkdir -p "$TRAEFIK_CERTS"

cp "$CEAGENT_FULLCHAIN_PATH" "$TRAEFIK_CERTS/${CEAGENT_DEPLOYMENT_NAME}.crt"
cp "$CEAGENT_KEY_PATH" "$TRAEFIK_CERTS/${CEAGENT_DEPLOYMENT_NAME}.key"
chmod 600 "$TRAEFIK_CERTS/${CEAGENT_DEPLOYMENT_NAME}.key"

# Traefik picks up changes automatically via file provider — no reload needed
echo "Certs deployed to $TRAEFIK_CERTS"
```

---

## Proxmox VE

```bash
#!/bin/bash
# scripts/deploy-proxmox.sh

set -e

cp "$CEAGENT_FULLCHAIN_PATH" /etc/pve/local/pveproxy-ssl.pem
cp "$CEAGENT_KEY_PATH" /etc/pve/local/pveproxy-ssl.key
chmod 600 /etc/pve/local/pveproxy-ssl.key

systemctl restart pveproxy
```

---

## Custom Notification (Webhook)

```bash
#!/bin/bash
# scripts/notify-webhook.sh
# Send a webhook notification after deployment.

set -e

WEBHOOK_URL="https://hooks.slack.com/services/T00/B00/xxxx"

curl -s -X POST "$WEBHOOK_URL" \
  -H "Content-Type: application/json" \
  -d "{
    \"text\": \"Certificate deployed: ${CEAGENT_DEPLOYMENT_NAME} (${CEAGENT_DOMAINS}) on $(hostname)\"
  }"
```

---

## Tips

- **Test before reload:** Always validate config (`nginx -t`, `apachectl configtest`) before reloading to avoid downtime from a bad config that's unrelated to the cert.
- **Permissions:** Private key files should be `0600` and owned by the service user.
- **Timeouts:** ceagent kills post-deploy scripts after 30 seconds. Keep them fast.
- **Logging:** Script stdout/stderr is captured by ceagent and logged. Use `echo` or `Write-Host` for status messages.
- **Failures are isolated:** A failed post-deploy script for one deployment doesn't prevent other deployments from being processed.
- **Idempotency:** Scripts may be called multiple times for the same cert (e.g. after an agent restart). Make sure they handle this gracefully.
