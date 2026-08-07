#!/bin/bash
set -e

echo "Installing Malaxis Fleet Agent..."

AGENT_DIR="$(pwd)/fleet-agent"

# --- Clean re-install detection ---
if [ -d "$AGENT_DIR" ] || docker ps --format '{{.Names}}' 2>/dev/null | grep -q "node-agent"; then
    echo "Existing installation detected. Performing clean re-install..."

    # Stop and remove existing containers
    if command -v docker &> /dev/null; then
        (cd "$AGENT_DIR" 2>/dev/null && docker compose down --remove-orphans 2>/dev/null) || (cd "$AGENT_DIR" 2>/dev/null && docker-compose down --remove-orphans 2>/dev/null) || true
        docker rm -f node-agent xray-node singbox-node 2>/dev/null || true
    fi

    # Wipe old installation files (preserve configs if they exist)
    if [ -d "$AGENT_DIR/configs" ]; then
        echo "Preserving existing configs directory..."
        mkdir -p /tmp/fleet-config-backup
        cp -r "$AGENT_DIR/configs"/* /tmp/fleet-config-backup/ 2>/dev/null || true
    fi

    rm -rf "$AGENT_DIR"
    echo "Old installation cleaned."
fi

# Create fresh directory structure
mkdir -p "$AGENT_DIR/configs"
cd "$AGENT_DIR"

echo "Downloading client files..."
curl -sSL https://__SUB_DOMAIN__/docker-compose.yml -o docker-compose.yml
curl -sSL https://__SUB_DOMAIN__/Dockerfile.client -o Dockerfile
curl -sSL https://__SUB_DOMAIN__/requirements.txt -o requirements.txt
curl -sSL https://__SUB_DOMAIN__/entrypoint.sh -o entrypoint.sh
curl -sSL https://__API_DOMAIN__/api/agent/latest -o node_agent.py
chmod +x entrypoint.sh

# Restore configs if backed up
if [ -d /tmp/fleet-config-backup ] && [ "$(ls -A /tmp/fleet-config-backup 2>/dev/null)" ]; then
    cp /tmp/fleet-config-backup/* "$AGENT_DIR/configs/" 2>/dev/null || true
    rm -rf /tmp/fleet-config-backup
    echo "Previous configs restored."
fi

# Download default configs so containers start cleanly
echo "Downloading default proxy configs..."
curl -sSL https://__SUB_DOMAIN__/configs/xray_config.json -o configs/xray_config.json 2>/dev/null || cat > configs/xray_config.json << 'XRAYEOF'
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
curl -sSL https://__SUB_DOMAIN__/configs/singbox_config.json -o configs/singbox_config.json 2>/dev/null || cat > configs/singbox_config.json << 'SINGEOF'
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
echo "Downloading fleet-cli utility..."
curl -sSL https://__JOIN_DOMAIN__/fleet-cli -o fleet-cli.sh
chmod +x fleet-cli.sh

# --- Docker availability check ---
if ! command -v docker &> /dev/null; then
    echo "ERROR: Docker is not installed. Install Docker, then re-run this script."
    exit 1
fi

# --- Interactive setup ---
# This script runs via `curl ... | bash`, so stdin carries the script itself
# rather than terminal input. Interactive prompts must therefore read from
# /dev/tty. When no TTY is available (e.g. non-interactive provisioning), the
# prompts silently fall back to their defaults.
if ! exec 3</dev/tty 2>/dev/null; then
    exec 3</dev/null
fi

echo ""
read -p "Enter Subscription URL (or press Enter to skip): " SUB_URL <&3 || true
SUB_URL=$(echo "$SUB_URL" | tr -d '[:space:]')

if [ -n "$SUB_URL" ]; then
    # Pre-seed the agent's persistent state so the first container boot
    # fetches and benchmarks servers immediately.
    echo "{\"sub_url\":\"$SUB_URL\"}" > configs/agent_state.json
    echo "Subscription URL saved to configs/agent_state.json."
else
    echo "Skipping subscription URL configuration."
fi

read -p "Install systemd service for auto-start on boot? [Y/n]: " INSTALL_SYSTEMD <&3 || true
INSTALL_SYSTEMD=$(echo "$INSTALL_SYSTEMD" | tr '[:upper:]' '[:lower:]')

echo "Building agent image and starting services..."
docker compose up -d --build || docker-compose up -d --build

# Create singbox-node container so the agent can manage it later via docker start/stop
echo "Preparing singbox-node container..."
docker compose create singbox-node 2>/dev/null || true

# Optional: install systemd service for auto-start at boot (Linux only)
if [ -z "$INSTALL_SYSTEMD" ] || [ "$INSTALL_SYSTEMD" = "y" ] || [ "$INSTALL_SYSTEMD" = "yes" ]; then
    if command -v systemctl &> /dev/null; then
        echo "Installing systemd service..."
        curl -sSL https://__JOIN_DOMAIN__/fleet-agent.service -o /etc/systemd/system/fleet-agent.service
        systemctl daemon-reload
        systemctl enable --now fleet-agent
        echo "systemd service installed and enabled."
    else
        echo "systemctl not found; systemd service not installed."
    fi
else
    echo "Skipping systemd service installation."
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
