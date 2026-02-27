# Uninstall Guide

This guide covers removing the CertKeeper Agent from Windows and Linux systems. For installation, see the [Installation Guide](installation.md). For configuration reference, see [Configuration](configuration.md).

---

## Standard Uninstall

### Windows (MSI)

**Via Settings:**
1. Open **Settings → Apps → Installed apps**
2. Find **CertKeeper Agent** → click **Uninstall**

**Via command line:**
```powershell
# Find the product code
$product = Get-WmiObject Win32_Product | Where-Object { $_.Name -eq "CertKeeper Agent" }
$product.Uninstall()

# Or if you have the original MSI:
msiexec /x ceagent_1.0.0_windows_amd64.msi /quiet
```

**What the uninstaller does:**
- Stops and removes the `ceagent` Windows service
- Removes `ceagent.exe` and `enroll.ps1` from `C:\Program Files\ceagent\`
- Removes the PATH entry
- Does **not** remove `C:\ProgramData\ceagent\` (certificates, config, state are preserved)

### Debian / Ubuntu (.deb)

```bash
# Remove the package (keeps config)
sudo dpkg -r ceagent

# Remove the package and purge config
sudo dpkg -P ceagent
```

**What the uninstaller does:**
- Stops and disables the `ceagent` systemd service
- Removes `/usr/local/bin/ceagent` and `/usr/local/bin/ceagent-enroll`
- Removes `/etc/systemd/system/ceagent.service`
- Does **not** remove `/etc/ceagent/` (certificates, config, state are preserved)

### Red Hat / CentOS / Fedora (.rpm)

```bash
sudo rpm -e ceagent
# or
sudo dnf remove ceagent
```

### Tarball (Manual)

```bash
sudo systemctl stop ceagent
sudo systemctl disable ceagent
sudo rm /etc/systemd/system/ceagent.service
sudo systemctl daemon-reload
sudo rm /usr/local/bin/ceagent
sudo rm /usr/local/bin/ceagent-enroll
```

---

## Complete Removal (Including Data)

After uninstalling via the standard method, remove all remaining data:

### Linux

```bash
# Remove all agent data, certificates, and config
sudo rm -rf /etc/ceagent
```

### Windows

```powershell
# Remove all agent data, certificates, and config
Remove-Item -Recurse -Force "C:\ProgramData\ceagent"
```

> **Warning:** This permanently deletes all deployed certificates, the agent's mTLS certificate, and configuration. The agent will need to be re-enrolled if reinstalled.

---

## Manual Cleanup After Catastrophic Failure

If the normal uninstall process fails (e.g. corrupted MSI database, broken package manager), follow these steps to manually clean up everything.

### Windows — Full Manual Removal

#### 1. Stop and remove the Windows service

```powershell
# Stop the service
Stop-Service ceagent -Force -ErrorAction SilentlyContinue

# Delete the service
sc.exe delete ceagent
```

If `sc.exe delete` reports the service is marked for deletion, reboot and try again.

#### 2. Remove files

```powershell
# Remove program files
Remove-Item -Recurse -Force "C:\Program Files\ceagent" -ErrorAction SilentlyContinue

# Remove data directory (certificates, config, state)
Remove-Item -Recurse -Force "C:\ProgramData\ceagent" -ErrorAction SilentlyContinue
```

#### 3. Remove from system PATH

```powershell
$machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
$cleanedPath = ($machinePath -split ";" | Where-Object { $_ -ne "C:\Program Files\ceagent\" -and $_ -ne "C:\Program Files\ceagent" }) -join ";"
[Environment]::SetEnvironmentVariable("Path", $cleanedPath, "Machine")
```

#### 4. Remove from the MSI internal database

If the MSI thinks the product is still installed (e.g. shows in "Apps & Features" but won't uninstall):

```powershell
# Find the product code
$product = Get-WmiObject Win32_Product | Where-Object { $_.Name -eq "CertKeeper Agent" }
if ($product) {
    Write-Host "Product code: $($product.IdentifyingNumber)"
    # Force remove the registration
    msiexec /x $product.IdentifyingNumber /quiet /norestart
}
```

If that still fails, use the **Microsoft Program Install and Uninstall Troubleshooter**:
1. Download from [Microsoft Support](https://support.microsoft.com/en-us/topic/fix-problems-that-block-programs-from-being-installed-or-removed-cca7d1b6-65a9-3d98-426b-e9f927e1eb4d)
2. Run the troubleshooter → select **Uninstalling** → select **CertKeeper Agent**
3. This cleans up the Windows Installer database entry

As a **last resort**, manually remove from the registry:

```powershell
# Search for the product in the registry (run as Administrator)
$paths = @(
    "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall",
    "HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall"
)

foreach ($path in $paths) {
    Get-ChildItem $path | ForEach-Object {
        $displayName = (Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue).DisplayName
        if ($displayName -eq "CertKeeper Agent") {
            Write-Host "Found: $($_.PSPath)"
            # Uncomment the next line to remove:
            # Remove-Item $_.PSPath -Recurse -Force
        }
    }
}
```

> **Warning:** Editing the registry directly can cause system instability. Only do this as a last resort after the troubleshooter fails.

#### 5. Verify complete removal

```powershell
# Check service is gone
Get-Service ceagent -ErrorAction SilentlyContinue

# Check files are gone
Test-Path "C:\Program Files\ceagent"
Test-Path "C:\ProgramData\ceagent"

# Check PATH is clean
$env:Path -split ";" | Where-Object { $_ -like "*ceagent*" }

# Check MSI database
Get-WmiObject Win32_Product | Where-Object { $_.Name -like "*CertKeeper*" }
```

All four checks should return nothing / `False`.

---

### Linux — Full Manual Removal

#### 1. Stop and remove the service

```bash
sudo systemctl stop ceagent 2>/dev/null
sudo systemctl disable ceagent 2>/dev/null
sudo rm -f /etc/systemd/system/ceagent.service
sudo systemctl daemon-reload
```

#### 2. Remove binaries

```bash
sudo rm -f /usr/local/bin/ceagent
sudo rm -f /usr/local/bin/ceagent-enroll
```

#### 3. Remove data directory

```bash
sudo rm -rf /etc/ceagent
```

#### 4. Clean up package manager state (if corrupted)

**Debian/Ubuntu:**
```bash
# Force remove the package from dpkg's database
sudo dpkg --remove --force-remove-reinstreq ceagent

# If dpkg is locked or corrupted
sudo rm -f /var/lib/dpkg/info/ceagent.*
sudo dpkg --configure -a
```

**RHEL/CentOS/Fedora:**
```bash
# Force remove from rpm database
sudo rpm -e --noscripts ceagent

# If rpm database is corrupted
sudo rpm --rebuilddb
sudo rpm -e ceagent
```

#### 5. Verify complete removal

```bash
# Check service is gone
systemctl status ceagent 2>&1 | grep -q "not found" && echo "Service removed" || echo "Service still exists"

# Check files are gone
ls /usr/local/bin/ceagent 2>/dev/null && echo "Binary still exists" || echo "Binary removed"
ls -d /etc/ceagent 2>/dev/null && echo "Data dir still exists" || echo "Data dir removed"

# Check package manager
dpkg -l ceagent 2>/dev/null || rpm -q ceagent 2>/dev/null || echo "Package removed from database"
```
