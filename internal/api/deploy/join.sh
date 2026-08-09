#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  Malaxis Fleet - Interactive Client Installer
#  Linux / macOS / Git Bash
#  (Native Windows users should use join.ps1)
# ============================================================

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

say()  { printf "${GREEN}[+]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[!]${NC} %s\n" "$*"; }
err()  { printf "${RED}[x]${NC} %s\n" "$*"; exit 1; }

# Interactive reads: this script is normally piped via `curl | bash`, so
# stdin carries the script itself rather than terminal input. Every prompt
# therefore reads from /dev/tty. When no TTY is available (e.g. headless
# provisioning), prompts silently fall back to their defaults.
if ! exec 3</dev/tty 2>/dev/null; then
    exec 3</dev/null
fi

ask() { read -r -p "$1" "$2" <&3 || true; }

echo -e "\n${CYAN}================================================${NC}"
echo -e "${CYAN}     Malaxis Fleet - Client Installer${NC}"
echo -e "${CYAN}     Linux / macOS / Git Bash${NC}"
echo -e "${CYAN}================================================${NC}\n"

# ------------------------------------------------------------
# 1. Pre-flight dependency & resource checks
# ------------------------------------------------------------
say "Running pre-flight checks..."

if ! command -v docker >/dev/null 2>&1; then
    err "Docker is not installed! Install Docker first (https://docs.docker.com/get-docker/), then re-run this script."
fi
say "Docker is installed."

if ! docker info >/dev/null 2>&1; then
    err "Docker is not running! Please start Docker Desktop or the Docker daemon before installing."
fi
say "Docker daemon is running."

if ! docker compose version >/dev/null 2>&1; then
    err "Docker Compose plugin is not available! Install the 'docker compose' v2 plugin, then re-run this script."
fi
say "Docker Compose plugin is available."

say "Checking master server connectivity (__API_DOMAIN__)..."
if curl -fsS --max-time 8 "https://__API_DOMAIN__/api/health" >/dev/null 2>&1; then
    say "Master server is reachable."
else
    warn "Master server did not respond. Check your network/firewall; the agent will retry automatically once started."
fi

# ------------------------------------------------------------
# 2. Interactive installation directory selector
# ------------------------------------------------------------
DEFAULT_CHOICE=3
if [ -d "$HOME/Documents" ]; then
    DEFAULT_CHOICE=1
elif [ -d "$HOME/Desktop" ]; then
    DEFAULT_CHOICE=2
fi

echo ""
echo "Where would you like to install Malaxis Fleet Client?"
echo "  [1] Documents (Default: ~/Documents/malaxis-fleet-client)"
echo "  [2] Desktop (~/Desktop/malaxis-fleet-client)"
echo "  [3] User Home Directory (~/malaxis-fleet-client)"
echo "  [4] Custom Path"
ask "Select [1-4, default ${DEFAULT_CHOICE}]: " DIR_CHOICE
DIR_CHOICE=${DIR_CHOICE:-$DEFAULT_CHOICE}

BASE_DIR=""
case "$DIR_CHOICE" in
    1) BASE_DIR="$HOME/Documents" ;;
    2) BASE_DIR="$HOME/Desktop" ;;
    3) BASE_DIR="$HOME" ;;
    4)
        ask "Enter custom installation path: " CUSTOM_DIR
        CUSTOM_DIR=${CUSTOM_DIR/#\~/$HOME}
        if [ -z "$CUSTOM_DIR" ]; then
            err "Custom path cannot be empty."
        fi
        BASE_DIR="$CUSTOM_DIR"
        ;;
    *)
        err "Invalid selection: $DIR_CHOICE"
        ;;
esac

AGENT_DIR="$BASE_DIR/malaxis-fleet-client"
mkdir -p "$AGENT_DIR"
say "Installation directory: $AGENT_DIR"

# ------------------------------------------------------------
# 3. Interactive setup (onboarding prompts)
# ------------------------------------------------------------
HOSTNAME_CURRENT=$(hostname 2>/dev/null || uname -n 2>/dev/null || echo "fleet-node")
ask "Enter a friendly name for this device [Default: ${HOSTNAME_CURRENT}]: " NODE_NAME
NODE_NAME=${NODE_NAME:-$HOSTNAME_CURRENT}

ask "Enter your 3x-ui Subscription URL (Press Enter to skip): " SUB_URL
SUB_URL=$(echo "$SUB_URL" | tr -d '[:space:]')

echo ""
echo "Select default Smart Routing Mode:"
echo "  [1] Balanced - Best stability & lowest jitter (Recommended)"
echo "  [2] Fastest - Lowest ping"
echo "  [3] Manual"
ask "Select [1-3, default 1]: " MODE_CHOICE
case "${MODE_CHOICE:-1}" in
    2) SMART_MODE="fastest" ;;
    3) SMART_MODE="manual" ;;
    *) SMART_MODE="balanced" ;;
esac

ask "Enable automatic startup on system boot? (systemd on Linux / Task Scheduler on Windows) [Y/n]: " AUTOSTART
AUTOSTART=$(echo "${AUTOSTART:-Y}" | tr '[:upper:]' '[:lower:]')

# ------------------------------------------------------------
# 4. Clean re-install detection
# ------------------------------------------------------------
if [ -d "$AGENT_DIR" ] || docker ps --format '{{.Names}}' 2>/dev/null | grep -q "node-agent"; then
    echo ""
    say "Existing installation detected. Performing clean re-install..."

    if command -v docker &> /dev/null; then
        (cd "$AGENT_DIR" 2>/dev/null && docker compose down --remove-orphans 2>/dev/null) || true
        docker rm -f node-agent xray-node singbox-node 2>/dev/null || true
    fi

    if [ -d "$AGENT_DIR/configs" ]; then
        say "Preserving existing configs directory..."
        mkdir -p /tmp/fleet-config-backup
        cp -r "$AGENT_DIR/configs"/* /tmp/fleet-config-backup/ 2>/dev/null || true
    fi

    rm -rf "$AGENT_DIR"
    say "Old installation cleaned."
fi

mkdir -p "$AGENT_DIR/configs"
cd "$AGENT_DIR"

# ------------------------------------------------------------
# 5. Download client payloads
# ------------------------------------------------------------
echo ""
say "Downloading client files..."
# All payload downloads carry the fleet secret (?t=) which is injected into
# this script by the server at serve time. Unauthenticated requests get a
# generic 404, so the endpoints stay invisible to active probes.
curl -sSL "https://__SUB_DOMAIN__/docker-compose.yml?t=__SECRET_TOKEN__" -o docker-compose.yml
curl -sSL "https://__SUB_DOMAIN__/Dockerfile.client?t=__SECRET_TOKEN__" -o Dockerfile
curl -sSL "https://__SUB_DOMAIN__/requirements.txt?t=__SECRET_TOKEN__" -o requirements.txt
curl -sSL "https://__SUB_DOMAIN__/entrypoint.sh?t=__SECRET_TOKEN__" -o entrypoint.sh
curl -sSL "https://__API_DOMAIN__/api/agent/latest?t=__SECRET_TOKEN__" -o node_agent.py
chmod +x entrypoint.sh

# Restore configs if backed up
if [ -d /tmp/fleet-config-backup ] && [ "$(ls -A /tmp/fleet-config-backup 2>/dev/null)" ]; then
    cp /tmp/fleet-config-backup/* "$AGENT_DIR/configs/" 2>/dev/null || true
    rm -rf /tmp/fleet-config-backup
    say "Previous configs restored."
fi

# Download default configs so containers start cleanly
echo ""
say "Downloading default proxy configs..."
curl -sSL "https://__SUB_DOMAIN__/configs/xray_config.json?t=__SECRET_TOKEN__" -o configs/xray_config.json 2>/dev/null || cat > configs/xray_config.json << 'XRAYEOF'
{
  "log": { "loglevel": "warning" },
  "dns": { "servers": ["https://dns.google/dns-query", "https://cloudflare-dns.com/dns-query", "8.8.8.8", "1.1.1.1"], "queryStrategy": "UseIPv4" },
  "inbounds": [
    { "port": 6357, "listen": "0.0.0.0", "protocol": "socks", "settings": { "auth": "noauth", "udp": true }, "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": true }, "tag": "socks-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15 } },
    { "port": 6358, "listen": "0.0.0.0", "protocol": "http", "settings": { "timeout": 0 }, "tag": "http-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15 } }
  ],
  "outbounds": [ { "protocol": "freedom", "tag": "direct" } ],
  "routing": {
    "domainStrategy": "IPIfNonMatch",
    "rules": [
      { "type": "field", "port": 53, "network": "udp", "outboundTag": "direct" },
      { "type": "field", "ip": ["91.108.0.0/16", "149.154.160.0/20", "185.76.151.0/24"], "outboundTag": "direct" }
    ]
  }
}
XRAYEOF
curl -sSL "https://__SUB_DOMAIN__/configs/singbox_config.json?t=__SECRET_TOKEN__" -o configs/singbox_config.json 2>/dev/null || cat > configs/singbox_config.json << 'SINGEOF'
{
  "log": { "level": "warn" },
  "dns": {
    "servers": [
      { "tag": "resolver", "address": "https://1.1.1.1/dns-query", "detour": "direct", "strategy": "ipv4_only" },
      { "tag": "block", "address": "rcode://success" }
    ],
    "final": "resolver",
    "independent_cache": true
  },
  "inbounds": [
    { "type": "socks", "tag": "socks-in", "listen": "0.0.0.0", "listen_port": 6357, "udp": true, "users": [], "sniff": { "enabled": true, "override_destination": false, "route_only": true } },
    { "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358, "users": [], "sniff": { "enabled": true, "override_destination": true, "route_only": true } }
  ],
  "route": { "final": "direct", "auto_detect_interface": true },
  "outbounds": [ { "type": "direct", "tag": "direct" } ]
}
SINGEOF

# Download fleet-cli utility
echo ""
say "Downloading fleet-cli utility..."
curl -sSL "https://__JOIN_DOMAIN__/fleet-cli?t=__SECRET_TOKEN__" -o fleet-cli.sh
chmod +x fleet-cli.sh

# ------------------------------------------------------------
# 6. Persist onboarding choices BEFORE starting the stack
# ------------------------------------------------------------
json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

{
    echo "{"
    echo "  \"sub_url\": \"$(json_escape "$SUB_URL")\","
    echo "  \"node_name\": \"$(json_escape "$NODE_NAME")\","
    echo "  \"active_mode\": \"$SMART_MODE\""
    echo "}"
} > configs/agent_state.json
say "Configuration written to configs/agent_state.json (node: ${NODE_NAME}, smart mode: ${SMART_MODE})."

# ------------------------------------------------------------
# 7. Build & start
# ------------------------------------------------------------
echo ""
say "Building agent image and starting services..."
docker compose up -d --build || docker-compose up -d --build

# Create singbox-node container so the agent can manage it later via docker start/stop
echo ""
say "Preparing singbox-node container..."
docker compose create singbox-node 2>/dev/null || true

# ------------------------------------------------------------
# 8. Auto-start on boot (systemd, Linux only)
# ------------------------------------------------------------
if [ -z "$AUTOSTART" ] || [ "$AUTOSTART" = "y" ] || [ "$AUTOSTART" = "yes" ]; then
    if command -v systemctl &> /dev/null; then
        echo ""
        say "Installing systemd service for auto-start on boot..."
        curl -sSL "https://__JOIN_DOMAIN__/fleet-agent.service?t=__SECRET_TOKEN__" -o /etc/systemd/system/fleet-agent.service
        systemctl daemon-reload
        systemctl enable --now fleet-agent
        say "systemd service installed and enabled."
    else
        warn "systemctl not found; systemd auto-start not installed."
    fi
else
    echo ""
    say "Skipping auto-start on boot."
fi

echo ""
echo "Malaxis Fleet Agent is running!"
echo ""
echo "Quick commands:"
echo "   View status:  cd \"$AGENT_DIR\" && bash fleet-cli.sh"
echo "   View logs:    docker logs -f node-agent"
echo "   Stop agent:   cd \"$AGENT_DIR\" && docker compose down"
echo "   Journalctl:   journalctl -u fleet-agent.service"
echo ""
