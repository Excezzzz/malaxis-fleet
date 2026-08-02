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
    { "port": 6357, "listen": "0.0.0.0", "protocol": "socks", "settings": { "auth": "noauth", "udp": true }, "tag": "socks-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15 } },
    { "port": 6358, "listen": "0.0.0.0", "protocol": "http", "settings": { "timeout": 0 }, "tag": "http-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15 } }
  ],
  "outbounds": [ { "protocol": "freedom", "tag": "direct" } ],
  "routing": { "domainStrategy": "IPIfNonMatch" }
}
XRAYEOF
curl -sSL https://__SUB_DOMAIN__/configs/singbox_config.json -o configs/singbox_config.json 2>/dev/null || cat > configs/singbox_config.json << 'SINGEOF'
{
  "log": { "level": "warn" },
  "inbounds": [
    { "type": "socks", "tag": "socks-in", "listen": "0.0.0.0", "listen_port": 6357, "udp": true, "users": [] },
    { "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358, "users": [] }
  ],
  "outbounds": [ { "type": "direct", "tag": "direct" } ]
}
SINGEOF

# Download fleet-cli utility
echo "Downloading fleet-cli utility..."
curl -sSL https://__JOIN_DOMAIN__/fleet-cli -o fleet-cli.sh
chmod +x fleet-cli.sh

echo "Building agent image and starting services..."
docker compose up -d --build || docker-compose up -d --build

# Create singbox-node container so the agent can manage it later via docker start/stop
echo "Preparing singbox-node container..."
docker compose create singbox-node 2>/dev/null || true

# Optional: install systemd service for auto-start at boot (Linux only)
if command -v systemctl &> /dev/null; then
    echo "Installing systemd service..."
    curl -sSL https://__JOIN_DOMAIN__/fleet-agent.service -o /etc/systemd/system/fleet-agent.service
    systemctl daemon-reload
    systemctl enable fleet-agent.service
    echo "systemd service installed and enabled."
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
