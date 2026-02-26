#!/bin/sh
set -e

# Reload systemd to pick up the new unit file.
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
fi

echo ""
echo "ceagent installed successfully."
echo ""

# ── Enrollment ──
# Silent mode: set CEAGENT_SERVER_URL and CEAGENT_ENROLLMENT_TOKEN env vars.
# Interactive mode: the script will prompt for values.
ENROLL_SCRIPT="/usr/local/bin/ceagent-enroll"
if [ -x "$ENROLL_SCRIPT" ]; then
    "$ENROLL_SCRIPT" || true
fi

# Start service if enrollment succeeded.
if [ -f /etc/ceagent/client.crt ]; then
    if command -v systemctl >/dev/null 2>&1; then
        systemctl enable ceagent 2>/dev/null || true
        systemctl start ceagent 2>/dev/null || true
        echo "Service ceagent enabled and started."
    fi
else
    echo "Enrollment was skipped or failed."
    echo "To enroll later:"
    echo "  ceagent enroll --server <url> --token <token>"
    echo "  systemctl enable --now ceagent"
fi
echo ""
