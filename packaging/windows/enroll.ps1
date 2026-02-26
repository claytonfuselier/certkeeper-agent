# CertKeeper Agent — Enrollment Script
# Launched by the MSI installer after files are installed.
#
# Interactive mode (default): shows a Windows Forms UI with retry on failure.
# Silent mode: pass -Quiet to skip UI; exits non-zero on failure.
#
# Silent install example:
#   msiexec /i ceagent.msi SERVER_URL=https://... ENROLLMENT_TOKEN=cke_... /quiet

param(
    [Parameter(Mandatory=$true)][string]$CeagentPath,
    [Parameter(Mandatory=$true)][string]$ServerUrl,
    [Parameter(Mandatory=$true)][string]$Token,
    [switch]$Quiet
)

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

[System.Windows.Forms.Application]::EnableVisualStyles()

function Show-EnrollmentForm {
    param([string]$InitialServer, [string]$InitialToken, [string]$ErrorMessage)

    $form = New-Object System.Windows.Forms.Form
    $form.Text = "CertKeeper Agent — Enrollment"
    $form.Size = New-Object System.Drawing.Size(500, 400)
    $form.StartPosition = "CenterScreen"
    $form.FormBorderStyle = "FixedDialog"
    $form.MaximizeBox = $false
    $form.MinimizeBox = $false
    $form.TopMost = $true

    $y = 15

    # ── Header ──
    $header = New-Object System.Windows.Forms.Label
    $header.Location = New-Object System.Drawing.Point(15, $y)
    $header.Size = New-Object System.Drawing.Size(455, 22)
    $header.Text = "Enroll this agent with your CertKeeper server"
    $header.Font = New-Object System.Drawing.Font("Segoe UI", 11, [System.Drawing.FontStyle]::Bold)
    $form.Controls.Add($header)
    $y += 35

    # ── Error banner (if present) ──
    $errorLabel = $null
    if ($ErrorMessage) {
        $errorPanel = New-Object System.Windows.Forms.Panel
        $errorPanel.Location = New-Object System.Drawing.Point(15, $y)
        $errorPanel.Size = New-Object System.Drawing.Size(455, 60)
        $errorPanel.BackColor = [System.Drawing.Color]::FromArgb(255, 240, 240)
        $errorPanel.BorderStyle = "FixedSingle"

        $errorLabel = New-Object System.Windows.Forms.Label
        $errorLabel.Location = New-Object System.Drawing.Point(8, 5)
        $errorLabel.Size = New-Object System.Drawing.Size(435, 50)
        $errorLabel.ForeColor = [System.Drawing.Color]::FromArgb(180, 0, 0)
        $errorLabel.Text = $ErrorMessage
        $errorLabel.Font = New-Object System.Drawing.Font("Segoe UI", 9)
        $errorPanel.Controls.Add($errorLabel)
        $form.Controls.Add($errorPanel)
        $y += 70
    }

    # ── Server URL ──
    $serverLabel = New-Object System.Windows.Forms.Label
    $serverLabel.Location = New-Object System.Drawing.Point(15, $y)
    $serverLabel.Size = New-Object System.Drawing.Size(455, 18)
    $serverLabel.Text = "CertKeeper Server URL:"
    $serverLabel.Font = New-Object System.Drawing.Font("Segoe UI", 9)
    $form.Controls.Add($serverLabel)
    $y += 20

    $serverBox = New-Object System.Windows.Forms.TextBox
    $serverBox.Location = New-Object System.Drawing.Point(15, $y)
    $serverBox.Size = New-Object System.Drawing.Size(455, 24)
    $serverBox.Font = New-Object System.Drawing.Font("Segoe UI", 10)
    $serverBox.Text = $InitialServer
    $form.Controls.Add($serverBox)
    $y += 35

    # ── Token ──
    $tokenLabel = New-Object System.Windows.Forms.Label
    $tokenLabel.Location = New-Object System.Drawing.Point(15, $y)
    $tokenLabel.Size = New-Object System.Drawing.Size(455, 18)
    $tokenLabel.Text = "Enrollment Token:"
    $tokenLabel.Font = New-Object System.Drawing.Font("Segoe UI", 9)
    $form.Controls.Add($tokenLabel)
    $y += 20

    $tokenBox = New-Object System.Windows.Forms.TextBox
    $tokenBox.Location = New-Object System.Drawing.Point(15, $y)
    $tokenBox.Size = New-Object System.Drawing.Size(455, 24)
    $tokenBox.Font = New-Object System.Drawing.Font("Consolas", 10)
    $tokenBox.Text = $InitialToken
    $form.Controls.Add($tokenBox)
    $y += 35

    # ── Hint ──
    $hint = New-Object System.Windows.Forms.Label
    $hint.Location = New-Object System.Drawing.Point(15, $y)
    $hint.Size = New-Object System.Drawing.Size(455, 32)
    $hint.Text = "The enrollment token is single-use and expires after 1 hour. You can find it in the CertKeeper web UI."
    $hint.ForeColor = [System.Drawing.Color]::Gray
    $hint.Font = New-Object System.Drawing.Font("Segoe UI", 8.5)
    $form.Controls.Add($hint)

    # ── Buttons ──
    $enrollBtn = New-Object System.Windows.Forms.Button
    $enrollBtn.Location = New-Object System.Drawing.Point(275, 320)
    $enrollBtn.Size = New-Object System.Drawing.Size(90, 30)
    $enrollBtn.Text = "Enroll"
    $enrollBtn.Font = New-Object System.Drawing.Font("Segoe UI", 9)
    $enrollBtn.DialogResult = [System.Windows.Forms.DialogResult]::OK
    $form.AcceptButton = $enrollBtn
    $form.Controls.Add($enrollBtn)

    $skipBtn = New-Object System.Windows.Forms.Button
    $skipBtn.Location = New-Object System.Drawing.Point(380, 320)
    $skipBtn.Size = New-Object System.Drawing.Size(90, 30)
    $skipBtn.Text = "Skip"
    $skipBtn.Font = New-Object System.Drawing.Font("Segoe UI", 9)
    $skipBtn.DialogResult = [System.Windows.Forms.DialogResult]::Cancel
    $form.CancelButton = $skipBtn
    $form.Controls.Add($skipBtn)

    # Disable Enroll if fields are empty.
    $validateFields = {
        $enrollBtn.Enabled = ($serverBox.Text.Trim() -ne "" -and $tokenBox.Text.Trim() -ne "")
    }
    $serverBox.Add_TextChanged($validateFields)
    $tokenBox.Add_TextChanged($validateFields)
    & $validateFields

    $result = $form.ShowDialog()

    return @{
        Result = $result
        Server = $serverBox.Text.Trim()
        Token  = $tokenBox.Text.Trim()
    }
}

function Run-Enrollment {
    param([string]$ExePath, [string]$Server, [string]$Token)

    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $ExePath
    $psi.Arguments = "enroll --server `"$Server`" --token `"$Token`""
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    $psi.WindowStyle = "Hidden"

    $process = [System.Diagnostics.Process]::Start($psi)
    $stdout = $process.StandardOutput.ReadToEnd()
    $stderr = $process.StandardError.ReadToEnd()
    $process.WaitForExit()

    return @{
        ExitCode = $process.ExitCode
        Stdout   = $stdout.Trim()
        Stderr   = $stderr.Trim()
    }
}

# ── Main loop ──

$currentServer = $ServerUrl
$currentToken = $Token
$errorMsg = $null
$enrolled = $false

# ── Silent mode: single attempt, no UI ──
if ($Quiet) {
    Write-Host "Enrolling with $currentServer (silent mode) ..."
    $result = Run-Enrollment -ExePath $CeagentPath -Server $currentServer -Token $currentToken

    if ($result.ExitCode -eq 0) {
        Write-Host "Enrollment successful."
        try {
            Set-Service -Name "ceagent" -StartupType Automatic -ErrorAction Stop
            Start-Service -Name "ceagent" -ErrorAction Stop
            Write-Host "Service started."
        } catch {
            Write-Host "Warning: Could not start service: $_"
        }
        exit 0
    } else {
        $output = if ($result.Stderr) { $result.Stderr } else { $result.Stdout }
        if (-not $output) { $output = "Unknown error (exit code $($result.ExitCode))" }
        Write-Host "Enrollment failed: $output"
        Write-Host "You can retry enrollment manually: ceagent enroll --server <url> --token <token>"
        exit 1
    }
}

# ── Interactive mode: retry loop with Windows Forms UI ──

while (-not $enrolled) {
    # First attempt uses the values from the MSI dialog (no form shown).
    # Subsequent attempts show the form with the error.
    if ($null -ne $errorMsg) {
        $input = Show-EnrollmentForm -InitialServer $currentServer -InitialToken $currentToken -ErrorMessage $errorMsg
        if ($input.Result -ne [System.Windows.Forms.DialogResult]::OK) {
            # User clicked Skip — install completes without enrollment.
            # Service stays registered but won't start. User can enroll later via CLI.
            Write-Host "Enrollment skipped by user."
            exit 0
        }
        $currentServer = $input.Server
        $currentToken = $input.Token
    }

    Write-Host "Enrolling with $currentServer ..."
    $result = Run-Enrollment -ExePath $CeagentPath -Server $currentServer -Token $currentToken

    if ($result.ExitCode -eq 0) {
        $enrolled = $true
        Write-Host "Enrollment successful."

        # Set service to auto-start and start it.
        try {
            Set-Service -Name "ceagent" -StartupType Automatic -ErrorAction Stop
            Start-Service -Name "ceagent" -ErrorAction Stop
            Write-Host "Service started."
        } catch {
            Write-Host "Warning: Could not start service: $_"
        }
    } else {
        # Build a user-friendly error message.
        $output = if ($result.Stderr) { $result.Stderr } else { $result.Stdout }
        if (-not $output) { $output = "Unknown error (exit code $($result.ExitCode))" }
        $errorMsg = "Enrollment failed: $output"
        Write-Host $errorMsg
    }
}

exit 0
