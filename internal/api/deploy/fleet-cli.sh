#!/bin/bash
# Malaxis Fleet CLI - Interactive Terminal Utility

set -e

# Resolve the real install directory when invoked through the global
# /usr/local/bin/malaxis-fleet symlink (readlink chain, relative-safe).
SCRIPT_PATH="$0"
while [ -L "$SCRIPT_PATH" ]; do
    LINK_TARGET="$(readlink "$SCRIPT_PATH")"
    case "$LINK_TARGET" in
        /*) SCRIPT_PATH="$LINK_TARGET" ;;
        *) SCRIPT_PATH="$(dirname "$SCRIPT_PATH")/$LINK_TARGET" ;;
    esac
done
AGENT_DIR="$(cd "$(dirname "$SCRIPT_PATH")" && pwd)"
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
BOLD=$'\033[1m'

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

# Resolve the docker compose command to use: the choice saved by the installer
# in agent_state.json wins, otherwise auto-detect (v2 plugin first, then v1
# standalone). Falls back to the v2 plugin so callers never get an empty word.
compose_cmd() {
    local saved
    saved=$(get_state "compose_cmd" "")
    if [ "$saved" = "docker compose" ] || [ "$saved" = "docker-compose" ]; then
        echo "$saved"
        return
    fi
    if docker compose version >/dev/null 2>&1; then
        echo "docker compose"
    elif command -v docker-compose >/dev/null 2>&1 && docker-compose version >/dev/null 2>&1; then
        echo "docker-compose"
    else
        echo "docker compose"
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

# Detect a terminated / rejected node: an explicit marker written by the agent
# during self-destruct, or the agent container being gone entirely while Docker
# is up (Docker being down just means the regular "Start Docker" menu applies).
is_terminated() {
    [ "$(get_state "terminated" "false")" = "true" ] && return 0
    docker ps -a --filter "name=node-agent" --format "{{.Names}}" 2>/dev/null | grep -q "node-agent" && return 1
    docker info &>/dev/null || return 1
    return 0
}

has_sub() {
    local u
    u=$(get_state "sub_url" "")
    [ -n "$u" ] && [ "$u" != "null" ]
}

show_menu() {
    clear
    echo "${CYAN}==========================================${NC}"
    echo "${CYAN}${BOLD}     Malaxis Fleet Agent CLI${NC}"
    echo "${CYAN}==========================================${NC}"
    echo ""

    if is_terminated; then
        echo " ${RED}[TERMINATED]${NC} This node was terminated or rejected by the admin."
        echo " You can wipe the local identity and re-register it as a new device."
        echo ""
        echo " 1) Send Re-join Request"
        echo " 2) Exit"
        echo ""
        read -p "Select option [1-2]: " choice
        case "$choice" in
            1) rejoin ;;
            2) clear; exit 0 ;;
            *) show_menu ;;
        esac
        return
    fi

    if ! has_sub; then
        echo " ${YELLOW}[SETUP]${NC} No subscription URL configured yet."
        echo " Set your 3x-ui subscription URL to start using the fleet."
        echo ""
        echo " 1) Set Subscription URL"
        echo " 2) Exit"
        echo ""
        read -p "Select option [1-2]: " choice
        case "$choice" in
            1) update_subscription ;;
            2) clear; exit 0 ;;
            *) show_menu ;;
        esac
        return
    fi

    if ! docker ps &>/dev/null; then
        echo "${RED}Docker is not running!${NC}"
        echo ""
        echo "1) Start Docker & Agent"
        echo "2) Exit"
        echo ""
        read -p "Select option [1-2]: " choice
        case "$choice" in
            1) sudo systemctl start docker 2>/dev/null || true
               cd "$AGENT_DIR" && $(compose_cmd) up -d 2>/dev/null || true
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

    local node_disp="${RED}Not running${NC}"
    if echo "$node_status" | grep -q "Up"; then
        node_disp="${GREEN}${node_status}${NC}"
    fi
    local xray_disp="${RED}Not running${NC}"
    if echo "$xray_status" | grep -q "Up"; then
        xray_disp="${GREEN}${xray_status}${NC}"
    fi
    local singbox_disp="${RED}Not running${NC}"
    if echo "$singbox_status" | grep -q "Up"; then
        singbox_disp="${GREEN}${singbox_status}${NC}"
    fi

    echo "------------------------------------------"
    echo " ${node_icon} node-agent     ${node_disp}"
    echo " ${xray_icon} xray-node      ${xray_disp}"
    echo " ${singbox_icon} singbox-node   ${singbox_disp}"
    echo "------------------------------------------"
    echo ""
    echo " ${CYAN}Active Server:${NC}    ${active_server}"
    echo " ${CYAN}Active Protocol:${NC}  ${active_proto}"
    echo " ${CYAN}Selection Mode:${NC}   ${active_mode}"
    echo " ${CYAN}SOCKS5 Proxy:${NC}     127.0.0.1:6357"
    echo " ${CYAN}HTTP Proxy:${NC}       127.0.0.1:6358"
    echo " ${CYAN}Last Update:${NC}      ${last_seen}"
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
    echo " 6) Rename Node"
    echo " 7) Terminate & Self-Destruct"
    echo " 8) Exit"
    echo "------------------------------------------"
    echo ""
    read -p "Select option [1-8]: " choice

    case "$choice" in
        1) update_subscription ;;
        2) update_client_files ;;
        3) switch_server ;;
        4) toggle_auto_update ;;
        5) view_logs ;;
        6) rename_node ;;
        7) terminate_node ;;
        8) clear; exit 0 ;;
        *) show_menu ;;
    esac
}

rejoin() {
    echo ""
    echo "${CYAN}--- Send Re-join Request ---${NC}"
    echo "${YELLOW}WARNING:${NC} This wipes the local identity and re-registers this"
    echo "device with the fleet as a brand-new node. This cannot be undone."
    echo ""
    read -p "Continue? [y/N]: " confirm
    case "$confirm" in
        y|Y) ;;
        *) echo "${YELLOW}Cancelled.${NC}"; sleep 2; show_menu; return ;;
    esac

    rm -f "$STATE_FILE" "$SUBCACHE_FILE" "$CONFIG_DIR/node_id.txt" 2>/dev/null || true
    mkdir -p "$CONFIG_DIR"
    docker rm -f node-agent 2>/dev/null || true
    cd "$AGENT_DIR"
    $(compose_cmd) up -d --force-recreate node-agent 2>/dev/null || true
    echo "${GREEN}Re-join request sent. The agent is re-registering with a fresh identity...${NC}"
    sleep 3
    clear
    show_menu
}

update_subscription() {
    echo ""
    echo "${CYAN}--- Update Subscription ---${NC}"

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
        echo "${YELLOW}No URL entered. Cancelled.${NC}"
        sleep 2
        show_menu
        return
    fi

    set_state "sub_url" "$sub_url"
    echo "${GREEN}Subscription URL saved.${NC} Fetching subscription now..."
    docker exec node-agent python3 -c "import node_agent; exit(node_agent.fetch_subscription_now('$sub_url'))" 2>/dev/null || true
    docker exec node-agent python3 -c "import node_agent; node_agent.report()" 2>/dev/null || true
    echo "${GREEN}Subscription URL synced to the web dashboard.${NC}"
    sleep 2

    local count
    count=$(cache_count)
    if [ "$count" -gt 0 ]; then
        echo "${GREEN}Found ${count} servers in subscription!${NC}"
        echo "Use Option 3 to select a server."
    else
        echo "${YELLOW}No servers found yet. The agent will fetch in background.${NC}"
    fi

    sleep 2
    show_menu
}

update_client_files() {
    echo ""
    echo "${CYAN}--- Updating Client Files ---${NC}"

    if [ -f "$STATE_FILE" ]; then
        cp "$STATE_FILE" /tmp/agent_state_backup.json 2>/dev/null || true
    fi

    curl -sSL "https://__SUB_DOMAIN__/docker-compose.yml?t=__SECRET_TOKEN__" -o "$DOCKER_COMPOSE_FILE" 2>/dev/null || true

    if [ -f /tmp/agent_state_backup.json ]; then
        mv /tmp/agent_state_backup.json "$STATE_FILE" 2>/dev/null || true
    fi

    cd "$AGENT_DIR" && $(compose_cmd) up -d --force-recreate node-agent 2>/dev/null || true

    echo "${GREEN}Client files updated!${NC}"
    sleep 2
    show_menu
}

rename_node() {
    echo ""
    echo "${CYAN}--- Rename Node ---${NC}"

    local current_name
    current_name=$(get_state "node_name" "$(hostname 2>/dev/null || echo 'this node')")
    echo "Current name: ${current_name}"
    read -p "Enter new name for this node: " new_name
    new_name=$(echo "$new_name" | tr -d '[:space:]')
    if [ -z "$new_name" ]; then
        echo "${YELLOW}No name entered. Cancelled.${NC}"
        sleep 2
        show_menu
        return
    fi

    set_state "node_name" "$new_name"
    docker exec node-agent python3 -c "import node_agent; node_agent.report(status='Renamed', message='Node renamed via CLI')" 2>/dev/null || true
    echo "${GREEN}Node renamed to '$new_name'.${NC} The dashboard will pick it up shortly."
    sleep 2
    show_menu
}

terminate_node() {
    echo ""
    echo "${RED}--- Terminate & Self-Destruct ---${NC}"
    echo "${RED}WARNING:${NC} This will tear down the VPN containers, wipe this node's local"
    echo "configuration, and remove the agent. This cannot be undone."
    echo ""
    read -p "Type TERMINATE to confirm, or anything else to cancel: " confirm_word
    if [ "$confirm_word" != "TERMINATE" ]; then
        echo "${YELLOW}Cancelled.${NC}"
        sleep 2
        show_menu
        return
    fi
    echo "${RED}Terminating...${NC}"
    docker exec node-agent python3 -c "import node_agent; node_agent.enqueue('terminate')" 2>/dev/null || true
    echo "${RED}Self-destruct initiated. Goodbye.${NC}"
    exit 0
}

switch_server() {
    echo ""
    echo "${CYAN}--- Server Switcher ---${NC}"

    echo ""
    echo "Available VPN Servers:"
    echo ""
    docker exec node-agent python3 -c "import node_agent; node_agent.print_server_list()" 2>/dev/null || {
        echo "${YELLOW}Failed to get server list from agent.${NC}"
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
        echo "${YELLOW}No servers in cache.${NC}"
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
        docker exec node-agent python3 -c "import node_agent; exit(node_agent.select_mode('fastest'))" 2>/dev/null || echo "${YELLOW}Failed to auto-select fastest server${NC}"
        sleep 2
        show_menu
        return
    fi

    if [ "$choice" = "B" ] 2>/dev/null || [ "$choice" = "b" ] 2>/dev/null; then
        echo ""
        echo "Running benchmark and selecting most stable server..."
        docker exec node-agent python3 -c "import node_agent; exit(node_agent.select_mode('balanced'))" 2>/dev/null || echo "${YELLOW}Failed to auto-select balanced server${NC}"
        sleep 2
        show_menu
        return
    fi

    if ! echo "$choice" | grep -q '^[0-9][0-9]*$' || [ "$choice" -lt 1 ] || [ "$choice" -gt "$server_count" ]; then
        echo "${YELLOW}Invalid selection.${NC}"
        sleep 2
        switch_server
        return
    fi

    local idx=$((choice-1))

    echo ""
    echo "Switching to server $choice..."
    docker exec node-agent python3 -c "import node_agent; exit(node_agent.select_server($idx))" 2>/dev/null || echo "${YELLOW}Failed to switch server${NC}"

    sleep 2
    show_menu
}

toggle_auto_update() {
    local current
    current=$(get_state "auto_update" "true")
    if [ "$current" = "true" ]; then
        set_state "auto_update" "false"
        echo "${YELLOW}Auto-update disabled. You take full responsibility for non-working proxy configurations.${NC}"
    else
        set_state "auto_update" "true"
        echo "${GREEN}Auto-update enabled.${NC}"
    fi
    sleep 3
    show_menu
}

view_logs() {
    echo ""
    echo "${CYAN}--- Last 30 lines of node-agent logs ---${NC}"
    echo "(Press Ctrl+C to return to menu)"
    echo ""
    docker logs node-agent --tail 30 2>/dev/null || echo "${YELLOW}No logs available.${NC}"
    echo ""
    read -p "Press Enter to return to menu..."
    show_menu
}

if [ ! -d "$AGENT_DIR" ]; then
    echo "${RED}Fleet Agent not found at $AGENT_DIR${NC}"
    echo "Please run the installation script first:"
    echo "  curl -sSL https://__JOIN_DOMAIN__ | bash"
    exit 1
fi

cd "$AGENT_DIR"

trap '' SIGINT

show_menu
