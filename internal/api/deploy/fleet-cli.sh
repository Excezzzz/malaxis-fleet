#!/bin/bash
# Malaxis Fleet CLI - Interactive Terminal Utility

set -e

AGENT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONFIG_DIR="$AGENT_DIR/configs"
STATE_FILE="$CONFIG_DIR/agent_state.json"
SUBCACHE_FILE="$CONFIG_DIR/subscription_cache.json"
DOCKER_COMPOSE_FILE="$AGENT_DIR/docker-compose.yml"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
BLUE=$'\033[0;34m'
CYAN=$'\033[0;36m'
NC=$'\033[0m'

get_state() {
    local key="$1"
    local fallback="$2"
    if [ -f "$STATE_FILE" ]; then
        if command -v jq &>/dev/null; then
            jq -r ".${key} // \"$fallback\"" "$STATE_FILE" 2>/dev/null || echo "$fallback"
        else
            grep -o "\"${key}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" "$STATE_FILE" 2>/dev/null | head -1 | sed 's/^"'"${key}"'"[[:space:]]*:[[:space:]]*"//;s/"$//' || echo "$fallback"
        fi
    else
        echo "$fallback"
    fi
}

set_state() {
    local key="$1"
    local value="$2"
    if [ ! -f "$STATE_FILE" ]; then
        echo "{}" > "$STATE_FILE"
    fi
    if command -v jq &>/dev/null; then
        local tmpfile
        tmpfile=$(mktemp)
        jq --arg k "$key" --arg v "$value" '.[$k] = $v' "$STATE_FILE" > "$tmpfile" 2>/dev/null && mv "$tmpfile" "$STATE_FILE"
    elif command -v python3 &>/dev/null; then
        python3 -c "
import json, sys
with open('$STATE_FILE') as f:
    d = json.load(f)
d['$key'] = '$value'
with open('$STATE_FILE', 'w') as f:
    json.dump(d, f)
" 2>/dev/null || true
    else
        grep -q "\"$key\":" "$STATE_FILE" 2>/dev/null && \
            sed -i "s/\"$key\":\"[^\"]*\"/\"$key\":\"$value\"/g" "$STATE_FILE" 2>/dev/null || \
            sed -i "s/}$/, \"$key\":\"$value\"}/" "$STATE_FILE" 2>/dev/null
    fi
}

cache_count() {
    if [ -f "$SUBCACHE_FILE" ]; then
        if command -v jq &>/dev/null; then
            jq 'length' "$SUBCACHE_FILE" 2>/dev/null || echo "0"
        else
            grep -c '"name"' "$SUBCACHE_FILE" 2>/dev/null || echo "0"
        fi
    else
        echo "0"
    fi
}

cache_get() {
    local idx="$1"
    local field="$2"
    if [ -f "$SUBCACHE_FILE" ] && command -v jq &>/dev/null; then
        jq -r ".[${idx}].${field} // \"unknown\"" "$SUBCACHE_FILE" 2>/dev/null || echo "unknown"
    else
        echo "unknown"
    fi
}

show_menu() {
    clear
    echo "=========================================="
    echo "     Malaxis Fleet Agent CLI"
    echo "=========================================="
    echo ""

    if ! docker ps &>/dev/null; then
        echo "Docker is not running!"
        echo ""
        echo "1) Start Docker & Agent"
        echo "2) Exit"
        echo ""
        read -p "Select option [1-2]: " choice
        case "$choice" in
            1) sudo systemctl start docker 2>/dev/null || true
               cd "$AGENT_DIR" && docker compose up -d 2>/dev/null || docker-compose up -d 2>/dev/null || true
               sleep 2
               show_menu
               return
               ;;
            2) exit 0 ;;
        esac
        return
    fi

    local node_status
    node_status=$(docker ps --filter "name=node-agent" --format "{{.Status}}" 2>/dev/null)
    local xray_status
    xray_status=$(docker ps --filter "name=xray-node" --format "{{.Status}}" 2>/dev/null)
    local singbox_status
    singbox_status=$(docker ps --filter "name=singbox-node" --format "{{.Status}}" 2>/dev/null)

    local node_icon="${RED}[OFF]${NC}"
    if echo "$node_status" | grep -q "Up"; then
        node_icon="${GREEN}[ON]${NC}"
    fi
    local xray_icon="${RED}[OFF]${NC}"
    if echo "$xray_status" | grep -q "Up"; then
        xray_icon="${GREEN}[ON]${NC}"
    fi
    local singbox_icon="${RED}[OFF]${NC}"
    if echo "$singbox_status" | grep -q "Up"; then
        singbox_icon="${GREEN}[ON]${NC}"
    fi

    local active_server
    active_server=$(get_state "active_server" "Not selected (Use Option 3)")
    local active_proto
    active_proto=$(get_state "active_proto" "N/A")
    local active_mode
    active_mode=$(get_state "active_mode" "manual")
    local last_seen
    last_seen=$(get_state "last_seen" "N/A")
    local server_count
    server_count=$(cache_count)

    if [ -z "$active_server" ] || [ "$active_server" = "null" ]; then
        active_server="Not selected (Use Option 3)"
    fi

    echo "------------------------------------------"
    echo " ${node_icon} node-agent     ${node_status:-Not running}"
    echo " ${xray_icon} xray-node      ${xray_status:-Not running}"
    echo " ${singbox_icon} singbox-node   ${singbox_status:-Not running}"
    echo "------------------------------------------"
    echo ""
    echo " Active Server:    ${active_server}"
    echo " Active Protocol:  ${active_proto}"
    echo " Selection Mode:   ${active_mode}"
    echo " SOCKS5 Proxy:     127.0.0.1:6357"
    echo " HTTP Proxy:       127.0.0.1:6358"
    echo " Last Update:      ${last_seen}"
    if [ "$server_count" -gt 0 ]; then
        echo " Cached Servers:  ${server_count} available"
    fi
    echo ""
    echo "------------------------------------------"
    echo " 1) Set / Update Subscription URL"
    echo " 2) Update Client Files"
    echo " 3) Switch Server"
    echo " 4) Toggle Auto-Update"
    echo " 5) View Agent Logs"
    echo " 6) Exit"
    echo "------------------------------------------"
    echo ""
    read -p "Select option [1-6]: " choice

    case "$choice" in
        1) update_subscription ;;
        2) update_client_files ;;
        3) switch_server ;;
        4) toggle_auto_update ;;
        5) view_logs ;;
        6) clear; exit 0 ;;
        *) show_menu ;;
    esac
}

update_subscription() {
    echo ""
    echo "--- Update Subscription ---"

    local current_url
    current_url=$(get_state "sub_url" "")
    if [ -n "$current_url" ] && [ "$current_url" != "null" ]; then
        echo "Current URL: ${current_url}"
        read -p "Enter new subscription URL (or press Enter to keep current): " sub_url
        sub_url=$(echo "$sub_url" | tr -d '[:space:]')
        if [ -z "$sub_url" ]; then
            sub_url="$current_url"
        fi
    else
        read -p "Enter your 3x-ui subscription URL: " sub_url
        sub_url=$(echo "$sub_url" | tr -d '[:space:]')
    fi

    if [ -z "$sub_url" ]; then
        echo "No URL entered. Cancelled."
        sleep 2
        show_menu
        return
    fi

    set_state "sub_url" "$sub_url"
    echo "Subscription URL saved. Fetching subscription now..."
    docker exec node-agent python3 -c "import node_agent; exit(node_agent.fetch_subscription_now('$sub_url'))" 2>/dev/null || true
    sleep 2

    local count
    count=$(cache_count)
    if [ "$count" -gt 0 ]; then
        echo "Found ${count} servers in subscription!"
        echo "Use Option 3 to select a server."
    else
        echo "No servers found yet. The agent will fetch in background."
    fi

    sleep 2
    show_menu
}

update_client_files() {
    echo ""
    echo "--- Updating Client Files ---"

    if [ -f "$STATE_FILE" ]; then
        cp "$STATE_FILE" /tmp/agent_state_backup.json 2>/dev/null || true
    fi

    curl -sSL https://sub-fleet.malaxis.ru/docker-compose.yml -o "$DOCKER_COMPOSE_FILE" 2>/dev/null || true

    if [ -f /tmp/agent_state_backup.json ]; then
        mv /tmp/agent_state_backup.json "$STATE_FILE" 2>/dev/null || true
    fi

    cd "$AGENT_DIR" && docker compose up -d --force-recreate node-agent 2>/dev/null || docker-compose up -d --force-recreate node-agent 2>/dev/null || true

    echo "Client files updated!"
    sleep 2
    show_menu
}

switch_server() {
    echo ""
    echo "--- Server Switcher ---"

    echo ""
    echo "Available VPN Servers:"
    echo ""
    docker exec node-agent python3 -c "import node_agent; node_agent.print_server_list()" 2>/dev/null || {
        echo "Failed to get server list from agent."
        echo "Make sure node-agent is running and subscription is configured (Option 1)."
        sleep 2
        show_menu
        return
    }

    local server_count
    server_count=$(docker exec node-agent python3 -c "
import json
with open('/app/configs/subscription_cache.json') as f:
    servers = json.load(f)
print(len(servers))
" 2>/dev/null || echo "0")

    if [ "$server_count" -eq 0 ]; then
        echo "No servers in cache."
        sleep 2
        show_menu
        return
    fi

    echo "  F) Fastest   (Auto-select lowest ping)"
    echo "  B) Balanced  (Auto-select highest stability & lowest jitter)"
    echo "  0) Cancel"
    echo ""
    read -p "Select [F/B/1-$server_count] or 0 to Cancel: " choice

    if [ "$choice" = "0" ] 2>/dev/null; then
        show_menu
        return
    fi

    if [ "$choice" = "F" ] 2>/dev/null || [ "$choice" = "f" ] 2>/dev/null; then
        echo ""
        echo "Running benchmark and selecting fastest server..."
        docker exec node-agent python3 -c "import node_agent; exit(node_agent.select_mode('fastest'))" 2>/dev/null || echo "Failed to auto-select fastest server"
        sleep 2
        show_menu
        return
    fi

    if [ "$choice" = "B" ] 2>/dev/null || [ "$choice" = "b" ] 2>/dev/null; then
        echo ""
        echo "Running benchmark and selecting most stable server..."
        docker exec node-agent python3 -c "import node_agent; exit(node_agent.select_mode('balanced'))" 2>/dev/null || echo "Failed to auto-select balanced server"
        sleep 2
        show_menu
        return
    fi

    if ! echo "$choice" | grep -q '^[0-9][0-9]*$' || [ "$choice" -lt 1 ] || [ "$choice" -gt "$server_count" ]; then
        echo "Invalid selection."
        sleep 2
        switch_server
        return
    fi

    local idx=$((choice-1))

    echo ""
    echo "Switching to server $choice..."
    docker exec node-agent python3 -c "import node_agent; exit(node_agent.select_server($idx))" 2>/dev/null || echo "Failed to switch server"

    sleep 2
    show_menu
}

toggle_auto_update() {
    local current
    current=$(get_state "auto_update" "true")
    if [ "$current" = "true" ]; then
        set_state "auto_update" "false"
        echo "Auto-update disabled. You take full responsibility for non-working proxy configurations."
    else
        set_state "auto_update" "true"
        echo "Auto-update enabled."
    fi
    sleep 3
    show_menu
}

view_logs() {
    echo ""
    echo "--- Last 30 lines of node-agent logs ---"
    echo "(Press Ctrl+C to return to menu)"
    echo ""
    docker logs node-agent --tail 30 2>/dev/null || echo "No logs available."
    echo ""
    read -p "Press Enter to return to menu..."
    show_menu
}

if [ ! -d "$AGENT_DIR" ]; then
    echo "Fleet Agent not found at $AGENT_DIR"
    echo "Please run the installation script first:"
    echo "  curl -sSL https://join-fleet.malaxis.ru | bash"
    exit 1
fi

cd "$AGENT_DIR"

trap '' SIGINT

show_menu
