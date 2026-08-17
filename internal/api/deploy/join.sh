#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  Malaxis Fleet - Zero-Touch Silent Client Installer
#  Linux / macOS / Git Bash
#  (Native Windows users should use join.ps1)
#
#  Fully non-interactive: the node registers itself under the OS
#  hostname and ALL configuration (subscription URLs, device name,
#  VPN mode) is done afterwards from the Web UI / Telegram bot.
# ============================================================

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

say()  { printf "${GREEN}[+]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[!]${NC} %s\n" "$*"; }
err()  { printf "${RED}[x]${NC} %s\n" "$*"; exit 1; }

# Never die silently: under `set -e` ANY command returning non-zero aborts the
# script. The ERR trap reports the exact failing line so the user always knows
# why the installer stopped instead of just dropping back to the shell prompt.
trap 'printf "${RED}[x]${NC} Installer encountered an error at line %s.\n" "$LINENO"' ERR

# ------------------------------------------------------------
# Zero-touch defaults: no prompts anywhere in this installer.
# ------------------------------------------------------------
HOSTNAME_CURRENT=$(hostname 2>/dev/null || uname -n 2>/dev/null || echo "fleet-node")
if [ "$(id -u)" = "0" ]; then
    AGENT_DIR="/opt/malaxis-fleet-client"
else
    AGENT_DIR="$HOME/malaxis-fleet-client"
fi

echo -e "\n${CYAN}================================================${NC}"
echo -e "${CYAN}     Malaxis Fleet - Zero-Touch Client Installer${NC}"
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

# Compose v2 plugin / v1 standalone detection (silent, zero-touch):
# v2 is preferred; v1 standalone is the automatic fallback.
HAVE_COMPOSE_V2=false
HAVE_COMPOSE_V1=false
if docker compose version >/dev/null 2>&1; then
    HAVE_COMPOSE_V2=true
fi
if command -v docker-compose >/dev/null 2>&1 && docker-compose version >/dev/null 2>&1; then
    HAVE_COMPOSE_V1=true
fi

COMPOSE_CMD=""
if [ "$HAVE_COMPOSE_V2" = true ]; then
    COMPOSE_CMD="docker compose"
elif [ "$HAVE_COMPOSE_V1" = true ]; then
    COMPOSE_CMD="docker-compose"
else
    err "Neither 'docker compose' nor 'docker-compose' is installed! Please install Docker Compose, then re-run this script."
fi
say "Docker Compose is available ($COMPOSE_CMD)."

say "Checking master server connectivity (__API_DOMAIN__)..."
if curl -fsS --max-time 5 "https://__API_DOMAIN__/api/health" >/dev/null 2>&1; then
    say "Master server is reachable."
else
    warn "Master server did not respond. Check your network/firewall; the agent will retry automatically once started."
fi

# ------------------------------------------------------------
# 2. Clean re-install detection
# ------------------------------------------------------------
if [ -d "$AGENT_DIR" ] || docker ps --format '{{.Names}}' 2>/dev/null | grep -q "node-agent"; then
    echo ""
    say "Existing installation detected. Performing clean re-install..."

    if command -v docker &> /dev/null; then
        (cd "$AGENT_DIR" 2>/dev/null && $COMPOSE_CMD down --remove-orphans 2>/dev/null) || true
        docker rm -f node-agent xray-node singbox-node 2>/dev/null || true
    fi

    if [ -d "$AGENT_DIR/configs" ]; then
        say "Preserving existing configs directory..."
        mkdir -p /tmp/fleet-config-backup 2>/dev/null || true
        cp -r "$AGENT_DIR/configs"/* /tmp/fleet-config-backup/ 2>/dev/null || true
    fi

    # The agent container runs as root, so configs/ and __pycache__ are
    # root-owned and a plain rm -rf fails (and under `set -e` used to abort the
    # reinstall). No sudo password is available on most hosts: instead reuse
    # the previously built node-agent image (also root) via a throwaway
    # container to purge the directory, then retry the plain rm.
    if ! rm -rf "$AGENT_DIR" 2>/dev/null; then
        warn "Old install contains root-owned files - cleaning via docker..."
        agent_img="$(cd "$AGENT_DIR" 2>/dev/null && $COMPOSE_CMD config 2>/dev/null | awk -F': ' '/^  node-agent:/{f=1} f&&/^    image:/{print $2; exit}')" || true
        agent_img="${agent_img:-malaxis-fleet-client_node-agent}"
        if docker run --rm -v "$AGENT_DIR":/app --entrypoint /bin/sh "$agent_img" -c 'rm -rf /app/* /app/.[!.]*; exit 0' 2>/dev/null && rm -rf "$AGENT_DIR" 2>/dev/null; then
            say "Old installation cleaned."
        else
            err "Cannot remove the old install at $AGENT_DIR (root-owned files). Remove it manually: sudo rm -rf $AGENT_DIR"
        fi
    else
        say "Old installation cleaned."
    fi
fi

mkdir -p "$AGENT_DIR/configs" 2>/dev/null || err "Failed to create $AGENT_DIR/configs"
cd "$AGENT_DIR" 2>/dev/null || err "Failed to enter $AGENT_DIR"

# ------------------------------------------------------------
# 3. Download client payloads
# ------------------------------------------------------------
echo ""
say "Downloading client files..."
# All payload downloads carry the fleet secret (?t=) which is injected into
# this script by the server at serve time. Unauthenticated requests get a
# generic 404, so the endpoints stay invisible to active probes. A failed
# download must never kill the installer silently - report it clearly.
curl -sSL "https://__SUB_DOMAIN__/docker-compose.yml?t=__SECRET_TOKEN__" -o docker-compose.yml || err "Failed to download docker-compose.yml (check network)"
curl -sSL "https://__SUB_DOMAIN__/Dockerfile.client?t=__SECRET_TOKEN__" -o Dockerfile || err "Failed to download Dockerfile.client"
curl -sSL "https://__SUB_DOMAIN__/requirements.txt?t=__SECRET_TOKEN__" -o requirements.txt || err "Failed to download requirements.txt"
curl -sSL "https://__SUB_DOMAIN__/entrypoint.sh?t=__SECRET_TOKEN__" -o entrypoint.sh || err "Failed to download entrypoint.sh"
curl -sSL "https://__API_DOMAIN__/api/agent/latest?t=__SECRET_TOKEN__" -o node_agent.py || err "Failed to download node_agent.py"
# Download and extract the modular agent package (agent_src/*.py).
curl -sSL "https://__API_DOMAIN__/api/agent/latest.zip?t=__SECRET_TOKEN__" -o agent_src.zip || err "Failed to download agent package (agent_src.zip)"
if command -v unzip >/dev/null 2>&1; then
    unzip -o agent_src.zip -d . >/dev/null 2>&1 || true
else
    python3 -c "import zipfile; zipfile.ZipFile('agent_src.zip').extractall('.')" 2>/dev/null || true
fi
rm -f agent_src.zip
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
    { "port": 6357, "listen": "0.0.0.0", "protocol": "socks", "settings": { "auth": "noauth", "udp": true }, "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"] }, "tag": "socks-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 15 } },
    { "port": 6358, "listen": "0.0.0.0", "protocol": "http", "settings": { "timeout": 0 }, "tag": "http-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 15 } }
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
curl -sSL "https://__JOIN_DOMAIN__/fleet-cli?t=__SECRET_TOKEN__" -o fleet-cli.sh || err "Failed to download fleet-cli.sh"
chmod +x fleet-cli.sh

# Create a global "malaxis-fleet" command so the CLI works from any directory.
# Use `sudo -n` (non-interactive): a plain `sudo` would block the whole
# installer on a password prompt mid-install. If passwordless sudo is not
# configured the symlink is skipped with a clear notice - never a hang.
if [ -w "/usr/local/bin" ]; then
    ln -sf "$AGENT_DIR/fleet-cli.sh" /usr/local/bin/malaxis-fleet
else
    sudo -n ln -sf "$AGENT_DIR/fleet-cli.sh" /usr/local/bin/malaxis-fleet 2>/dev/null || echo "[!] Could not create global command 'malaxis-fleet' (requires root). You can run it manually via ./fleet-cli.sh"
fi

# ------------------------------------------------------------
# 4. Persist zero-touch defaults BEFORE starting the stack:
#    the node registers under the OS hostname; subscription URLs
#    and VPN mode are configured later from the Web UI / bot.
# ------------------------------------------------------------
json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

{
    echo "{"
    echo "  \"node_name\": \"$(json_escape "$HOSTNAME_CURRENT")\","
    echo "  \"active_mode\": \"balanced\","
    echo "  \"compose_cmd\": \"$(json_escape "$COMPOSE_CMD")\","
    echo "  \"sub_url\": \"\","
    echo "  \"sub_urls\": []"
    echo "}"
} > configs/agent_state.json
say "Configuration written to configs/agent_state.json (node: ${HOSTNAME_CURRENT}, smart mode: balanced)."

# compose_up brings the whole stack up. docker-compose v1 (standalone) fails on
# FIRST creation with "Service 'singbox-node' uses the network stack of
# container 'xray-node' which does not exist": singbox-node shares xray-node's
# network namespace (network_mode: container:) and v1 validates the WHOLE
# project against existing containers before creating anything. Bootstrap
# xray-node via a stripped compose file (singbox-node removed), then bring the
# full stack up - harmless for compose v2 as well (skipped, first attempt works).
# NOTE: singbox-node must remain the LAST service before `networks:` in
# client-docker-compose.yml (downloaded as docker-compose.yml) for the awk
# strip to produce a valid file.
compose_up() {
    if ! $COMPOSE_CMD up -d --build; then
        warn "docker-compose v1 ordering detected - bootstrapping xray-node first..."
        awk '/^  singbox-node:/{skip=1} /^networks:/{skip=0} !skip{print}' docker-compose.yml > .compose-xray-only.yml
        # Do NOT hide stderr here: a silent failure turns into the baffling
        # "Docker Compose up failed" with zero output. The bootstrap is
        # best-effort (`|| true` swallows only the exit code), but its stderr
        # must stay visible so the user sees the real reason for a failure.
        $COMPOSE_CMD -f .compose-xray-only.yml up -d xray-node || true
        $COMPOSE_CMD up -d --build || err "Docker Compose up failed - review the output above and re-run this script"
    fi
}

# ------------------------------------------------------------
# 5. Build & start
# ------------------------------------------------------------
echo ""
say "Building agent image and starting services..."
compose_up

# Create singbox-node container so the agent can manage it later via docker start/stop
echo ""
say "Preparing singbox-node container..."
$COMPOSE_CMD create singbox-node 2>/dev/null || true

echo ""
echo "✅ Malaxis Fleet Agent installed successfully!"
echo "   The node is registered as: ${HOSTNAME_CURRENT}"
echo "   Subscription URLs and VPN mode are configured from the Web UI / Telegram bot."
echo ""
echo "Quick commands:"
echo "   Status:  cd \"$AGENT_DIR\" && bash fleet-cli.sh"
echo "   Logs:    docker logs -f node-agent"
echo "   Stop:    cd \"$AGENT_DIR\" && $COMPOSE_CMD down"
echo ""