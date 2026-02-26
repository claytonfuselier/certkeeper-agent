#!/bin/sh
set -e

# Stop and disable the service before removal
if command -v systemctl >/dev/null 2>&1; then
    systemctl stop ceagent 2>/dev/null || true
    systemctl disable ceagent 2>/dev/null || true
    systemctl daemon-reload
fi
