#!/bin/sh
# CertKeeper Agent — Interactive enrollment script
# Called by postinstall.sh or manually from tarball installs.
# Supports silent mode via environment variables:
#   CEAGENT_SERVER_URL and CEAGENT_ENROLLMENT_TOKEN
#
# Usage:
#   Interactive:  ./enroll.sh
#   Silent:       CEAGENT_SERVER_URL=https://... CEAGENT_ENROLLMENT_TOKEN=cke_... ./enroll.sh

CEAGENT_BIN="${CEAGENT_BIN:-/usr/local/bin/ceagent}"

# ── Colors (if terminal supports them) ──
if [ -t 1 ] && command -v tput >/dev/null 2>&1; then
    RED=$(tput setaf 1)
    GREEN=$(tput setaf 2)
    YELLOW=$(tput setaf 3)
    BOLD=$(tput bold)
    RESET=$(tput sgr0)
else
    RED="" GREEN="" YELLOW="" BOLD="" RESET=""
fi

print_error() {
    printf "%s%sError:%s %s\n" "$RED" "$BOLD" "$RESET" "$1" >&2
}

print_success() {
    printf "%s%s✓%s %s\n" "$GREEN" "$BOLD" "$RESET" "$1"
}

print_warning() {
    printf "%s%sWarning:%s %s\n" "$YELLOW" "$BOLD" "$RESET" "$1"
}

# ── Check if already enrolled ──
if [ -f /etc/ceagent/client.crt ]; then
    print_success "Agent is already enrolled. Skipping enrollment."
    exit 0
fi

# ── Determine mode (silent vs interactive) ──
SERVER_URL="${CEAGENT_SERVER_URL:-}"
TOKEN="${CEAGENT_ENROLLMENT_TOKEN:-}"

if [ -n "$SERVER_URL" ] && [ -n "$TOKEN" ]; then
    # Silent mode — single attempt, no prompts
    echo "Enrolling with $SERVER_URL ..."
    OUTPUT=$("$CEAGENT_BIN" enroll --server "$SERVER_URL" --token "$TOKEN" 2>&1) && RC=0 || RC=$?

    if [ $RC -eq 0 ]; then
        print_success "Enrollment successful."
        exit 0
    else
        print_error "Enrollment failed:"
        echo "  $OUTPUT"
        echo ""
        echo "You can retry enrollment manually:"
        echo "  ceagent enroll --server <url> --token <token>"
        exit 1
    fi
fi

# ── Interactive mode ──
echo ""
echo "${BOLD}CertKeeper Agent — Enrollment${RESET}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "This agent needs to be enrolled with your CertKeeper server."
echo "You'll need the server URL and a one-time enrollment token"
echo "from the CertKeeper web UI."
echo ""

while true; do
    # ── Prompt for server URL ──
    if [ -z "$SERVER_URL" ]; then
        printf "CertKeeper Server URL (e.g. https://certkeeper.example.com:3000): "
        read -r SERVER_URL
    fi

    if [ -z "$SERVER_URL" ]; then
        print_error "Server URL cannot be empty."
        continue
    fi

    # ── Prompt for token ──
    if [ -z "$TOKEN" ]; then
        printf "Enrollment Token (starts with cke_): "
        read -r TOKEN
    fi

    if [ -z "$TOKEN" ]; then
        print_error "Enrollment token cannot be empty."
        TOKEN=""
        continue
    fi

    # ── Attempt enrollment ──
    echo ""
    echo "Enrolling with $SERVER_URL ..."
    OUTPUT=$("$CEAGENT_BIN" enroll --server "$SERVER_URL" --token "$TOKEN" 2>&1) && RC=0 || RC=$?

    if [ $RC -eq 0 ]; then
        echo ""
        print_success "Enrollment successful!"
        exit 0
    fi

    # ── Handle failure ──
    echo ""
    print_error "Enrollment failed:"
    echo "  $OUTPUT"
    echo ""

    # ── Ask to retry ──
    while true; do
        printf "Would you like to [r]etry with new values, [s]kip enrollment, or [q]uit? (r/s/q): "
        read -r CHOICE
        case "$CHOICE" in
            r|R)
                SERVER_URL=""
                TOKEN=""
                echo ""
                break
                ;;
            s|S)
                echo ""
                print_warning "Enrollment skipped. You can enroll later manually:"
                echo "  ceagent enroll --server <url> --token <token>"
                echo ""
                exit 0
                ;;
            q|Q)
                echo ""
                echo "Enrollment aborted."
                exit 1
                ;;
            *)
                echo "  Please enter r, s, or q."
                ;;
        esac
    done
done
