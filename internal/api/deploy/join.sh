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

# ------------------------------------------------------------
# 0. Language selector (first step, before any other output)
# ------------------------------------------------------------
echo ""
echo "Select installer language / Выберите язык установки:"
echo "  [1] Русский (По умолчанию / Default)"
echo "  [2] English"
ask "> " LANG_CHOICE
LANG_CHOICE=${LANG_CHOICE:-1}

if [ "$LANG_CHOICE" = "2" ] || [ "$LANG_CHOICE" = "en" ] || [ "$LANG_CHOICE" = "EN" ]; then
    lang="en"
else
    lang="ru"
fi

# Values needed by localized prompts (computed before strings)
DEFAULT_CHOICE=3
if [ -d "$HOME/Documents" ]; then
    DEFAULT_CHOICE=1
elif [ -d "$HOME/Desktop" ]; then
    DEFAULT_CHOICE=2
fi
HOSTNAME_CURRENT=$(hostname 2>/dev/null || uname -n 2>/dev/null || echo "fleet-node")

# ------------------------------------------------------------
# Localized strings
# ------------------------------------------------------------
if [ "$lang" = "en" ]; then
    T_PREFLIGHT="Running pre-flight checks..."
    T_DOCKER_NOT_INSTALLED="Docker is not installed! Install Docker first (https://docs.docker.com/get-docker/), then re-run this script."
    T_DOCKER_INSTALLED="Docker is installed."
    T_DOCKER_NOT_RUNNING="Docker is not running! Please start Docker Desktop or the Docker daemon before installing."
    T_DOCKER_RUNNING="Docker daemon is running."
    T_COMPOSE_MISSING="Docker Compose plugin is not available! Install the 'docker compose' v2 plugin, then re-run this script."
    T_COMPOSE_OK="Docker Compose plugin is available."
    T_CHECK_MASTER="Checking master server connectivity (__API_DOMAIN__)..."
    T_MASTER_OK="Master server is reachable."
    T_MASTER_WARN="Master server did not respond. Check your network/firewall; the agent will retry automatically once started."
    T_DIR_TITLE="Select installation directory for Malaxis Fleet Client:"
    T_DIR_1="[1] Documents (Default: ~/Documents/malaxis-fleet-client)"
    T_DIR_2="[2] Desktop (~/Desktop/malaxis-fleet-client)"
    T_DIR_3="[3] User Home Directory (~/malaxis-fleet-client)"
    T_DIR_4="[4] Custom Path"
    T_CUSTOM_PATH="Enter custom installation path: "
    T_PATH_EMPTY="Custom path cannot be empty."
    T_INVALID_CHOICE="Invalid selection: "
    T_INSTALL_DIR="Installation directory: "
    T_SUB_PROMPT="Enter your 3x-ui Subscription URL (Press Enter to skip): "
    T_MODE_TITLE="Select default Smart Routing Mode:"
    T_MODE_1="[1] Balanced - Best stability & lowest jitter (Recommended)"
    T_MODE_2="[2] Fastest - Lowest ping"
    T_MODE_3="[3] Manual"
    T_MODE_PROMPT="Select [1-3, default 1]: "
    T_AUTOSTART_PROMPT="Enable automatic startup on system boot? (systemd on Linux / Task Scheduler on Windows) [Y/n]: "
    T_REINSTALL="Existing installation detected. Performing clean re-install..."
    T_PRESERVE="Preserving existing configs directory..."
    T_OLD_CLEANED="Old installation cleaned."
    T_DL_FILES="Downloading client files..."
    T_CONFIGS_RESTORED="Previous configs restored."
    T_DL_CONFIGS="Downloading default proxy configs..."
    T_DL_CLI="Downloading fleet-cli utility..."
    T_BUILD="Building agent image and starting services..."
    T_PREP_SING="Preparing singbox-node container..."
    T_SYSTEMD_INSTALL="Installing systemd service for auto-start on boot..."
    T_SYSTEMD_OK="systemd service installed and enabled."
    T_SYSTEMD_MISSING="systemctl not found; systemd auto-start not installed."
    T_SKIP_AUTOSTART="Skipping auto-start on boot."
    T_DONE="Malaxis Fleet Agent is running!"
    T_QUICK="Quick commands:"
    T_Q_STATUS="View status:"
    T_Q_LOGS="View logs:"
    T_Q_STOP="Stop agent:"
    T_Q_JOURNAL="Journalctl:"
else
    T_PREFLIGHT="Проверка системных требований..."
    T_DOCKER_NOT_INSTALLED="Docker не установлен! Установите Docker (https://docs.docker.com/get-docker/), затем запустите скрипт заново."
    T_DOCKER_INSTALLED="Docker установлен."
    T_DOCKER_NOT_RUNNING="Docker не запущен! Пожалуйста, запустите Docker Desktop или службу Docker перед установкой."
    T_DOCKER_RUNNING="Docker запущен."
    T_COMPOSE_MISSING="Плагин Docker Compose недоступен! Установите плагин 'docker compose' v2, затем запустите скрипт заново."
    T_COMPOSE_OK="Плагин Docker Compose доступен."
    T_CHECK_MASTER="Проверка связи с мастер-сервером (__API_DOMAIN__)..."
    T_MASTER_OK="Мастер-сервер доступен."
    T_MASTER_WARN="Мастер-сервер не ответил. Проверьте сеть/файрвол; агент автоматически повторит попытку после запуска."
    T_DIR_TITLE="Выберите папку для установки Malaxis Fleet Client:"
    T_DIR_1="[1] Документы (По умолчанию: ~/Documents/malaxis-fleet-client)"
    T_DIR_2="[2] Рабочий стол (~/Desktop/malaxis-fleet-client)"
    T_DIR_3="[3] Домашняя папка (~/malaxis-fleet-client)"
    T_DIR_4="[4] Ввести свой путь"
    T_CUSTOM_PATH="Введите путь установки: "
    T_PATH_EMPTY="Путь не может быть пустым."
    T_INVALID_CHOICE="Неверный выбор: "
    T_INSTALL_DIR="Папка установки: "
    T_SUB_PROMPT="Введите ссылку подписки 3x-ui (Enter — пропустить): "
    T_MODE_TITLE="Режим балансировки по умолчанию:"
    T_MODE_1="[1] Балансировка — лучшая стабильность и минимальный джиттер (Рекомендуется)"
    T_MODE_2="[2] Самый быстрый — минимальный пинг"
    T_MODE_3="[3] Вручную"
    T_MODE_PROMPT="Выберите [1-3, по умолчанию 1]: "
    T_AUTOSTART_PROMPT="Включить автозапуск при загрузке системы? [Y/n]: "
    T_REINSTALL="Обнаружена существующая установка. Выполняется чистая переустановка..."
    T_PRESERVE="Сохраняю существующие конфигурации..."
    T_OLD_CLEANED="Старая установка удалена."
    T_DL_FILES="Загрузка файлов клиента..."
    T_CONFIGS_RESTORED="Предыдущие конфигурации восстановлены."
    T_DL_CONFIGS="Загрузка стандартных конфигураций прокси..."
    T_DL_CLI="Загрузка утилиты fleet-cli..."
    T_BUILD="Сборка образа агента и запуск сервисов..."
    T_PREP_SING="Подготовка контейнера singbox-node..."
    T_SYSTEMD_INSTALL="Установка systemd-службы для автозапуска..."
    T_SYSTEMD_OK="systemd-служба установлена и включена."
    T_SYSTEMD_MISSING="systemctl не найден; автозапуск через systemd не установлен."
    T_SKIP_AUTOSTART="Автозапуск пропущен."
    T_DONE="Malaxis Fleet Agent запущен!"
    T_QUICK="Быстрые команды:"
    T_Q_STATUS="Статус:"
    T_Q_LOGS="Логи:"
    T_Q_STOP="Остановить:"
    T_Q_JOURNAL="Журнал:"
fi

# Prompts with values only known at ask-time (hostname, defaults, node/mode)
t_dir_prompt() {
    if [ "$lang" = "en" ]; then
        printf "Select [1-4, default %s]: " "$DEFAULT_CHOICE"
    else
        printf "Выберите [1-4, по умолчанию %s]: " "$DEFAULT_CHOICE"
    fi
}
t_node_prompt() {
    if [ "$lang" = "en" ]; then
        printf "Enter a friendly name for this device [Default: %s]: " "$HOSTNAME_CURRENT"
    else
        printf "Введите имя устройства [По умолчанию: %s]: " "$HOSTNAME_CURRENT"
    fi
}
t_state_written() {
    if [ "$lang" = "en" ]; then
        echo "Configuration written to configs/agent_state.json (node: ${NODE_NAME}, smart mode: ${SMART_MODE})."
    else
        echo "Конфигурация сохранена в configs/agent_state.json (узел: ${NODE_NAME}, режим: ${SMART_MODE})."
    fi
}

echo -e "\n${CYAN}================================================${NC}"
echo -e "${CYAN}     Malaxis Fleet - Client Installer${NC}"
echo -e "${CYAN}     Linux / macOS / Git Bash${NC}"
echo -e "${CYAN}================================================${NC}\n"

# ------------------------------------------------------------
# 1. Pre-flight dependency & resource checks
# ------------------------------------------------------------
say "$T_PREFLIGHT"

if ! command -v docker >/dev/null 2>&1; then
    err "$T_DOCKER_NOT_INSTALLED"
fi
say "$T_DOCKER_INSTALLED"

if ! docker info >/dev/null 2>&1; then
    err "$T_DOCKER_NOT_RUNNING"
fi
say "$T_DOCKER_RUNNING"

if ! docker compose version >/dev/null 2>&1; then
    err "$T_COMPOSE_MISSING"
fi
say "$T_COMPOSE_OK"

say "$T_CHECK_MASTER"
if curl -fsS --max-time 8 "https://__API_DOMAIN__/api/health" >/dev/null 2>&1; then
    say "$T_MASTER_OK"
else
    warn "$T_MASTER_WARN"
fi

# ------------------------------------------------------------
# 2. Interactive installation directory selector
# ------------------------------------------------------------
echo ""
echo "$T_DIR_TITLE"
echo "$T_DIR_1"
echo "$T_DIR_2"
echo "$T_DIR_3"
echo "$T_DIR_4"
ask "$(t_dir_prompt)" DIR_CHOICE
DIR_CHOICE=${DIR_CHOICE:-$DEFAULT_CHOICE}

BASE_DIR=""
case "$DIR_CHOICE" in
    1) BASE_DIR="$HOME/Documents" ;;
    2) BASE_DIR="$HOME/Desktop" ;;
    3) BASE_DIR="$HOME" ;;
    4)
        ask "$T_CUSTOM_PATH" CUSTOM_DIR
        CUSTOM_DIR=${CUSTOM_DIR/#\~/$HOME}
        if [ -z "$CUSTOM_DIR" ]; then
            err "$T_PATH_EMPTY"
        fi
        BASE_DIR="$CUSTOM_DIR"
        ;;
    *)
        err "${T_INVALID_CHOICE}$DIR_CHOICE"
        ;;
esac

AGENT_DIR="$BASE_DIR/malaxis-fleet-client"
mkdir -p "$AGENT_DIR"
say "${T_INSTALL_DIR}$AGENT_DIR"

# ------------------------------------------------------------
# 3. Interactive setup (onboarding prompts)
# ------------------------------------------------------------
ask "$(t_node_prompt)" NODE_NAME
NODE_NAME=${NODE_NAME:-$HOSTNAME_CURRENT}

ask "$T_SUB_PROMPT" SUB_URL
SUB_URL=$(echo "$SUB_URL" | tr -d '[:space:]')

echo ""
echo "$T_MODE_TITLE"
echo "$T_MODE_1"
echo "$T_MODE_2"
echo "$T_MODE_3"
ask "$T_MODE_PROMPT" MODE_CHOICE
case "${MODE_CHOICE:-1}" in
    2) SMART_MODE="fastest" ;;
    3) SMART_MODE="manual" ;;
    *) SMART_MODE="balanced" ;;
esac

ask "$T_AUTOSTART_PROMPT" AUTOSTART
AUTOSTART=$(echo "${AUTOSTART:-Y}" | tr '[:upper:]' '[:lower:]')

# ------------------------------------------------------------
# 4. Clean re-install detection
# ------------------------------------------------------------
if [ -d "$AGENT_DIR" ] || docker ps --format '{{.Names}}' 2>/dev/null | grep -q "node-agent"; then
    echo ""
    say "$T_REINSTALL"

    if command -v docker &> /dev/null; then
        (cd "$AGENT_DIR" 2>/dev/null && docker compose down --remove-orphans 2>/dev/null) || true
        docker rm -f node-agent xray-node singbox-node 2>/dev/null || true
    fi

    if [ -d "$AGENT_DIR/configs" ]; then
        say "$T_PRESERVE"
        mkdir -p /tmp/fleet-config-backup
        cp -r "$AGENT_DIR/configs"/* /tmp/fleet-config-backup/ 2>/dev/null || true
    fi

    rm -rf "$AGENT_DIR"
    say "$T_OLD_CLEANED"
fi

mkdir -p "$AGENT_DIR/configs"
cd "$AGENT_DIR"

# ------------------------------------------------------------
# 5. Download client payloads
# ------------------------------------------------------------
echo ""
say "$T_DL_FILES"
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
    say "$T_CONFIGS_RESTORED"
fi

# Download default configs so containers start cleanly
echo ""
say "$T_DL_CONFIGS"
curl -sSL "https://__SUB_DOMAIN__/configs/xray_config.json?t=__SECRET_TOKEN__" -o configs/xray_config.json 2>/dev/null || cat > configs/xray_config.json << 'XRAYEOF'
{
  "log": { "loglevel": "warning" },
  "dns": { "servers": ["https://dns.google/dns-query", "https://cloudflare-dns.com/dns-query", "8.8.8.8", "1.1.1.1"], "queryStrategy": "UseIPv4" },
  "inbounds": [
    { "port": 6357, "listen": "0.0.0.0", "protocol": "socks", "settings": { "auth": "noauth", "udp": true }, "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": true }, "tag": "socks-in", "sockopt": { "tcpNoDelay": true, "tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 15 } },
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
say "$T_DL_CLI"
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
say "$(t_state_written)"

# ------------------------------------------------------------
# 7. Build & start
# ------------------------------------------------------------
echo ""
say "$T_BUILD"
docker compose up -d --build || docker-compose up -d --build

# Create singbox-node container so the agent can manage it later via docker start/stop
echo ""
say "$T_PREP_SING"
docker compose create singbox-node 2>/dev/null || true

# ------------------------------------------------------------
# 8. Auto-start on boot (systemd, Linux only)
# ------------------------------------------------------------
if [ -z "$AUTOSTART" ] || [ "$AUTOSTART" = "y" ] || [ "$AUTOSTART" = "yes" ]; then
    if command -v systemctl &> /dev/null; then
        echo ""
        say "$T_SYSTEMD_INSTALL"
        curl -sSL "https://__JOIN_DOMAIN__/fleet-agent.service?t=__SECRET_TOKEN__" -o /etc/systemd/system/fleet-agent.service
        systemctl daemon-reload
        systemctl enable --now fleet-agent
        say "$T_SYSTEMD_OK"
    else
        warn "$T_SYSTEMD_MISSING"
    fi
else
    echo ""
    say "$T_SKIP_AUTOSTART"
fi

echo ""
echo "$T_DONE"
echo ""
echo "$T_QUICK"
echo "   $T_Q_STATUS  cd \"$AGENT_DIR\" && bash fleet-cli.sh"
echo "   $T_Q_LOGS    docker logs -f node-agent"
echo "   $T_Q_STOP    cd \"$AGENT_DIR\" && docker compose down"
echo "   $T_Q_JOURNAL journalctl -u fleet-agent.service"
echo ""
