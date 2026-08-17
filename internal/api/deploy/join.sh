#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  Malaxis Fleet - Lite-Touch Client Installer
#  Linux / macOS / Git Bash
#  (Native Windows users should use join.ps1)
#
#  Asks ONLY: language, installation directory, device name.
#  Subscription URLs are configured later from the Web UI /
#  Telegram bot / fleet-cli. The installer exits cleanly
#  without launching the CLI.
# ============================================================

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

say()  { printf "${GREEN}[+]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[!]${NC} %s\n" "$*"; }
err()  { printf "${RED}[x]${NC} %s\n" "$*"; exit 1; }

# `set -e` + ERR trap: any failing command reports its line and continues.
trap 'echo "[x] Installer encountered an error at line $LINENO. Continuing safely..."' ERR

# safe_read can never hang the installer: curl|bash pipes stdin into the
# script, so plain `read </dev/tty` would block forever when /dev/tty is
# missing or non-interactive. Instead: open /dev/tty once (fd4), drain stale
# input, wait up to 15s, fall back to the default on timeout/EOF.
safe_read() {
    local prompt="$1"
    local default_val="$2"
    local var_name="$3"
    local result=""
    # fd4 stays open: closing/reopening /dev/tty between prompts makes bash
    # 5.1 stop showing `read -p` prompts (read still blocks, no prompt shown).
    if [ "${FLEET_TTY_OPEN:-0}" = "1" ]; then
        # Drop stale buffered input, so a leftover newline is never read as
        # an empty answer. Bounded drain (50ms of silence) before the prompt.
        while read -t 0.05 -n 1 -r _ <&4 2>/dev/null; do :; done || true
        # `read -p` writes to STDERR, so the prompt must stay visible.
        read -t 15 -r -p "$prompt" result <&4 || result=""
    fi
    if [ -z "$result" ]; then
        result="$default_val"
    fi
    case "$var_name" in
        *[!A-Za-z0-9_]*) : ;;
        *) eval "$var_name=\"$result\"" ;;
    esac
}

# Open the controlling terminal ONCE on fd4. `{ exec; } 2>/dev/null` group is
# required: a bare `exec 4</dev/tty 2>/dev/null` still aborts on the fd error
# (bash processes redirections left to right, before applying 2>/dev/null).
FLEET_TTY_OPEN=0
if { exec 4</dev/tty; } 2>/dev/null; then
    FLEET_TTY_OPEN=1
fi

# ------------------------------------------------------------
# 0. Language selector (first step, before any other output)
# ------------------------------------------------------------
echo ""
echo "Select installer language / Выберите язык установки:"
echo "  [1] Русский (По умолчанию / Default)"
echo "  [2] English"
safe_read "> " "1" LANG_CHOICE
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
    T_DOCKER_NOT_INSTALLED="Docker is not installed!"
    T_DOCKER_DESKTOP_HINT="On this platform please install Docker Desktop from the official site: https://www.docker.com/products/docker-desktop/ , then re-run this script."
    T_DOCKER_CHOICE_TITLE="Choose the Docker variant to install:"
    T_DOCKER_CHOICE_1="[1] Docker + docker compose (v2 plugin, recommended)"
    T_DOCKER_CHOICE_2="[2] Docker + docker-compose (legacy v1)"
    T_DOCKER_CHOICE_PROMPT="Select [1-2, default 1]: "
    T_DOCKER_INSTALLING="Installing Docker (may require your password for sudo)..."
    T_DOCKER_SCRIPT_FAILED="Failed to install Docker via the official script (https://get.docker.com). Please install Docker manually and re-run this script."
    T_DOCKER_OK_GROUP="Docker is installed and ready."
    T_DOCKER_RELOGIN="Docker was installed, but the current user cannot access the daemon yet. Log out and back in (or run 'newgrp docker'), then re-run this script."
    T_DOCKER_INSTALLED="Docker is installed."
    T_DOCKER_NOT_RUNNING="Docker is not running! Please start Docker Desktop or the Docker daemon before installing."
    T_DOCKER_RUNNING="Docker daemon is running."
    T_COMPOSE_MISSING="Neither 'docker compose' nor 'docker-compose' is installed!"
    T_COMPOSE_STILL_MISSING="Docker Compose is still unavailable after the installation attempt. Please install it manually (https://docs.docker.com/compose/install/), then re-run this script."
    T_COMPOSE_OK="Docker Compose is available."
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
    T_INVALID_CHOICE="Invalid choice: "
    T_INSTALL_DIR="Installation directory: "
    T_NAME_RETRY="Device name cannot be empty - please enter a name: "
    T_NAME_DEFAULT="Using default device name: "
    T_REINSTALL="Existing installation detected. Performing clean re-install..."
    T_PRESERVE="Preserving existing configs directory..."
    T_OLD_CLEANED="Old installation cleaned."
    T_DL_FILES="Downloading client files..."
    T_CONFIGS_RESTORED="Previous configs restored."
    T_DL_CONFIGS="Downloading default proxy configs..."
    T_DL_CLI="Downloading fleet-cli utility..."
    T_BUILD="Building agent image and starting services..."
    T_PREP_SING="Preparing singbox-node container..."
    T_DONE="Malaxis Fleet Agent is running!"
    T_QUICK="Quick commands:"
    T_Q_STATUS="View status:"
    T_Q_LOGS="View logs:"
    T_Q_STOP="Stop agent:"
    T_GLOBAL_CMD="You can now run 'malaxis-fleet' from anywhere!"
    T_SUMMARY_TITLE="✅ Malaxis Fleet Agent installed successfully!"
    T_SUMMARY_SOCKS="SOCKS5 Proxy"
    T_SUMMARY_HTTP="HTTP Proxy"
else
    T_PREFLIGHT="Проверка системных требований..."
    T_DOCKER_NOT_INSTALLED="Docker не установлен!"
    T_DOCKER_DESKTOP_HINT="На этой платформе установите Docker Desktop с официального сайта: https://www.docker.com/products/docker-desktop/ , затем запустите скрипт заново."
    T_DOCKER_CHOICE_TITLE="Выберите вариант Docker для установки:"
    T_DOCKER_CHOICE_1="[1] Docker + docker compose (плагин v2, рекомендуется)"
    T_DOCKER_CHOICE_2="[2] Docker + docker-compose (старая версия v1)"
    T_DOCKER_CHOICE_PROMPT="Выберите [1-2, по умолчанию 1]: "
    T_DOCKER_INSTALLING="Установка Docker (может потребоваться пароль для sudo)..."
    T_DOCKER_SCRIPT_FAILED="Не удалось установить Docker через официальный скрипт (https://get.docker.com). Установите Docker вручную и запустите скрипт заново."
    T_DOCKER_OK_GROUP="Docker установлен и готов к работе."
    T_DOCKER_RELOGIN="Docker установлен, но текущий пользователь пока не имеет доступа к демону. Выйдите из системы и войдите снова (или выполните 'newgrp docker'), затем запустите скрипт заново."
    T_DOCKER_INSTALLED="Docker установлен."
    T_DOCKER_NOT_RUNNING="Docker не запущен! Пожалуйста, запустите Docker Desktop или службу Docker перед установкой."
    T_DOCKER_RUNNING="Docker запущен."
    T_COMPOSE_MISSING="Ни одна из утилит Docker Compose не установлена!"
    T_COMPOSE_STILL_MISSING="Docker Compose всё ещё недоступен после попытки установки. Установите его вручную (https://docs.docker.com/compose/install/), затем запустите скрипт заново."
    T_COMPOSE_OK="Docker Compose доступен."
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
    T_NAME_RETRY="Имя устройства не может быть пустым - введите имя: "
    T_NAME_DEFAULT="Использую имя устройства по умолчанию: "
    T_REINSTALL="Обнаружена существующая установка. Выполняется чистая переустановка..."
    T_PRESERVE="Сохраняю существующие конфигурации..."
    T_OLD_CLEANED="Старая установка удалена."
    T_DL_FILES="Загрузка файлов клиента..."
    T_CONFIGS_RESTORED="Предыдущие конфигурации восстановлены."
    T_DL_CONFIGS="Загрузка стандартных конфигураций прокси..."
    T_DL_CLI="Загрузка утилиты fleet-cli..."
    T_BUILD="Сборка образа агента и запуск сервисов..."
    T_PREP_SING="Подготовка контейнера singbox-node..."
    T_DONE="Malaxis Fleet Agent запущен!"
    T_QUICK="Быстрые команды:"
    T_Q_STATUS="Статус:"
    T_Q_LOGS="Логи:"
    T_Q_STOP="Остановить:"
    T_GLOBAL_CMD="Теперь команду 'malaxis-fleet' можно запускать откуда угодно!"
    T_SUMMARY_TITLE="✅ Malaxis Fleet Agent успешно установлен!"
    T_SUMMARY_SOCKS="SOCKS5 Прокси"
    T_SUMMARY_HTTP="HTTP Прокси"
fi

# Prompts with values only known at ask-time (hostname, defaults)
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
        echo "Configuration written to configs/agent_state.json (node: ${NODE_NAME}, smart mode: balanced)."
    else
        echo "Конфигурация сохранена в configs/agent_state.json (узел: ${NODE_NAME}, режим: balanced)."
    fi
}

# ------------------------------------------------------------
# linux_install_docker - full Docker installation on Linux.
# Asks which flavor to install (v2 "docker compose" plugin or
# legacy v1 "docker-compose"), installs the engine via the
# official convenience script, then the chosen compose flavor.
# Re-detects availability and exits with an error if the daemon
# is still not usable by the current user.
# ------------------------------------------------------------
as_root() {
    if [ "$(id -u)" = "0" ]; then
        "$@"
    else
        sudo "$@"
    fi
}

linux_install_docker() {
    echo ""
    echo "$T_DOCKER_CHOICE_TITLE"
    echo "$T_DOCKER_CHOICE_1"
    echo "$T_DOCKER_CHOICE_2"
    safe_read "$T_DOCKER_CHOICE_PROMPT" "1" DOCKER_CHOICE
    DOCKER_CHOICE=${DOCKER_CHOICE:-1}
    case "$DOCKER_CHOICE" in
        1|2) ;;
        *) err "${T_INVALID_CHOICE}$DOCKER_CHOICE" ;;
    esac
    say "$T_DOCKER_INSTALLING"
    if ! command -v docker >/dev/null 2>&1; then
        if ! curl -fsSL https://get.docker.com -o /tmp/get-docker.sh 2>/dev/null; then
            err "$T_DOCKER_SCRIPT_FAILED"
        fi
        if ! as_root sh /tmp/get-docker.sh >/dev/null 2>&1; then
            rm -f /tmp/get-docker.sh
            err "$T_DOCKER_SCRIPT_FAILED"
        fi
        rm -f /tmp/get-docker.sh
    fi
    if [ "$DOCKER_CHOICE" = "1" ]; then
        if ! docker compose version >/dev/null 2>&1; then
            if command -v apt-get >/dev/null 2>&1; then
                as_root apt-get update -qq >/dev/null 2>&1 || true
                as_root apt-get install -y docker-compose-plugin >/dev/null 2>&1 || true
            elif command -v dnf >/dev/null 2>&1; then
                as_root dnf install -y docker-compose-plugin >/dev/null 2>&1 || true
            elif command -v yum >/dev/null 2>&1; then
                as_root yum install -y docker-compose-plugin >/dev/null 2>&1 || true
            elif command -v zypper >/dev/null 2>&1; then
                as_root zypper --non-interactive install docker-compose-plugin >/dev/null 2>&1 || true
            fi
        fi
    else
        if ! command -v docker-compose >/dev/null 2>&1; then
            if command -v apt-get >/dev/null 2>&1; then
                as_root apt-get update -qq >/dev/null 2>&1 || true
                as_root apt-get install -y docker-compose >/dev/null 2>&1 || true
            elif command -v dnf >/dev/null 2>&1; then
                as_root dnf install -y docker-compose >/dev/null 2>&1 || true
            elif command -v yum >/dev/null 2>&1; then
                as_root yum install -y docker-compose >/dev/null 2>&1 || true
            elif command -v zypper >/dev/null 2>&1; then
                as_root zypper --non-interactive install docker-compose >/dev/null 2>&1 || true
            elif command -v apk >/dev/null 2>&1; then
                as_root apk add docker-compose >/dev/null 2>&1 || true
            fi
            if ! command -v docker-compose >/dev/null 2>&1 && command -v pip3 >/dev/null 2>&1; then
                as_root pip3 install docker-compose >/dev/null 2>&1 || true
            fi
        fi
    fi
    HAVE_COMPOSE_V2=false
    HAVE_COMPOSE_V1=false
    if docker compose version >/dev/null 2>&1; then
        HAVE_COMPOSE_V2=true
    fi
    if command -v docker-compose >/dev/null 2>&1 && docker-compose version >/dev/null 2>&1; then
        HAVE_COMPOSE_V1=true
    fi
    as_root usermod -aG docker "$USER" 2>/dev/null || true
    if docker info >/dev/null 2>&1; then
        say "$T_DOCKER_OK_GROUP"
    else
        err "$T_DOCKER_RELOGIN"
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
    if [ "$(uname -s)" = "Linux" ]; then
        warn "$T_DOCKER_NOT_INSTALLED"
        linux_install_docker
        if ! docker compose version >/dev/null 2>&1 && ! { command -v docker-compose >/dev/null 2>&1 && docker-compose version >/dev/null 2>&1; }; then
            err "$T_COMPOSE_STILL_MISSING"
        fi
    else
        err "$T_DOCKER_DESKTOP_HINT"
    fi
fi
say "$T_DOCKER_INSTALLED"

if ! docker info >/dev/null 2>&1; then
    err "$T_DOCKER_NOT_RUNNING"
fi
say "$T_DOCKER_RUNNING"

# Compose v2 plugin / v1 standalone detection (silent):
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
    warn "$T_COMPOSE_MISSING"
    if [ "$(uname -s)" = "Linux" ]; then
        linux_install_docker
        if [ "$HAVE_COMPOSE_V2" = true ]; then
            COMPOSE_CMD="docker compose"
        elif [ "$HAVE_COMPOSE_V1" = true ]; then
            COMPOSE_CMD="docker-compose"
        else
            err "$T_COMPOSE_STILL_MISSING"
        fi
    else
        err "$T_COMPOSE_DESKTOP_HINT"
    fi
fi
say "${T_COMPOSE_OK} ($COMPOSE_CMD)"

say "$T_CHECK_MASTER"
if curl -fsS --max-time 5 "https://__API_DOMAIN__/api/health" >/dev/null 2>&1; then
    say "$T_MASTER_OK"
else
    warn "$T_MASTER_WARN"
fi

# ------------------------------------------------------------
# 2. Installation directory selector
# ------------------------------------------------------------
echo ""
echo "$T_DIR_TITLE"
echo "$T_DIR_1"
echo "$T_DIR_2"
echo "$T_DIR_3"
echo "$T_DIR_4"
safe_read "$(t_dir_prompt)" "$DEFAULT_CHOICE" DIR_CHOICE
DIR_CHOICE=${DIR_CHOICE:-$DEFAULT_CHOICE}

BASE_DIR=""
case "$DIR_CHOICE" in
    1) BASE_DIR="$HOME/Documents" ;;
    2) BASE_DIR="$HOME/Desktop" ;;
    3) BASE_DIR="$HOME" ;;
    4)
        safe_read "$T_CUSTOM_PATH" "" CUSTOM_DIR
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
mkdir -p "$AGENT_DIR" 2>/dev/null || err "Failed to create installation directory: $AGENT_DIR"
say "${T_INSTALL_DIR}$AGENT_DIR"

# ------------------------------------------------------------
# 3. Device name prompt
# ------------------------------------------------------------
# Device name: asked on EVERY install, and never silently discarded - an empty
# answer (accidental Enter) triggers a second prompt while a terminal is
# available, and only a fully non-interactive run falls back to the hostname.
safe_read "$(t_node_prompt)" "" NODE_NAME
NODE_NAME=$(echo "$NODE_NAME" | tr -d '[:space:]')
if [ -z "$NODE_NAME" ]; then
    if [ "${FLEET_TTY_OPEN:-0}" = "1" ]; then
        warn "$T_NAME_RETRY"
        safe_read "$(t_node_prompt)" "" NODE_NAME
        NODE_NAME=$(echo "$NODE_NAME" | tr -d '[:space:]')
    fi
fi
if [ -z "$NODE_NAME" ]; then
    say "${T_NAME_DEFAULT}$HOSTNAME_CURRENT"
fi
NODE_NAME=${NODE_NAME:-$HOSTNAME_CURRENT}

# ------------------------------------------------------------
# 4. Clean re-install detection
# ------------------------------------------------------------
if [ -d "$AGENT_DIR" ] || docker ps --format '{{.Names}}' 2>/dev/null | grep -q "node-agent"; then
    echo ""
    say "$T_REINSTALL"

    if command -v docker &> /dev/null; then
        (cd "$AGENT_DIR" 2>/dev/null && $COMPOSE_CMD down --remove-orphans 2>/dev/null) || true
        docker rm -f node-agent xray-node singbox-node 2>/dev/null || true
    fi

    if [ -d "$AGENT_DIR/configs" ]; then
        say "$T_PRESERVE"
        mkdir -p /tmp/fleet-config-backup 2>/dev/null || true
        cp -r "$AGENT_DIR/configs"/* /tmp/fleet-config-backup/ 2>/dev/null || true
    fi

    # Root-owned files from a previous install block plain rm -rf; reuse the
    # old node-agent image (also root) via a throwaway container to purge.
    if ! rm -rf "$AGENT_DIR" 2>/dev/null; then
        warn "Old install contains root-owned files - cleaning via docker..."
        agent_img="$(cd "$AGENT_DIR" 2>/dev/null && $COMPOSE_CMD config 2>/dev/null | awk -F': ' '/^  node-agent:/{f=1} f&&/^    image:/{print $2; exit}')" || true
        agent_img="${agent_img:-malaxis-fleet-client_node-agent}"
        if docker run --rm -v "$AGENT_DIR":/app --entrypoint /bin/sh "$agent_img" -c 'rm -rf /app/* /app/.[!.]*; exit 0' 2>/dev/null && rm -rf "$AGENT_DIR" 2>/dev/null; then
            say "$T_OLD_CLEANED"
        else
            err "Cannot remove the old install at $AGENT_DIR (root-owned files). Remove it manually: sudo rm -rf $AGENT_DIR"
        fi
    else
        say "$T_OLD_CLEANED"
    fi
fi

mkdir -p "$AGENT_DIR/configs" 2>/dev/null || err "Failed to create $AGENT_DIR/configs"
cd "$AGENT_DIR" 2>/dev/null || err "Failed to enter $AGENT_DIR"

# ------------------------------------------------------------
# 5. Download client payloads
# ------------------------------------------------------------
echo ""
say "$T_DL_FILES"
# Payload downloads carry the fleet secret (?t=); unauthenticated requests
# get a generic 404. A failed download is reported, never silent.
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
say "$T_DL_CLI"
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
# 6. Persist onboarding choices BEFORE starting the stack.
#    Subscription URLs are intentionally left EMPTY - they are
#    set from the Web UI / Telegram bot / fleet-cli afterwards.
# ------------------------------------------------------------
json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }

{
    echo "{"
    echo "  \"node_name\": \"$(json_escape "$NODE_NAME")\","
    echo "  \"active_mode\": \"balanced\","
    echo "  \"compose_cmd\": \"$(json_escape "$COMPOSE_CMD")\","
    echo "  \"sub_url\": \"\","
    echo "  \"sub_urls\": []"
    echo "}"
} > configs/agent_state.json
say "$(t_state_written)"

# compose v1 fails on FIRST up: singbox-node shares xray-node's netns
# (network_mode: container:) and v1 validates the whole project before
# creating anything. Bootstrap xray-node from a stripped compose file, then
# bring the full stack up. singbox-node must stay the LAST service before
# `networks:` for the awk strip to work.
compose_up() {
    if ! $COMPOSE_CMD up -d --build; then
        warn "docker-compose v1 ordering detected - bootstrapping xray-node first..."
        awk '/^  singbox-node:/{skip=1} /^networks:/{skip=0} !skip{print}' docker-compose.yml > .compose-xray-only.yml
        # Keep bootstrap stderr visible: it explains why "up" failed.
        $COMPOSE_CMD -f .compose-xray-only.yml up -d xray-node || true
        $COMPOSE_CMD up -d --build || err "Docker Compose up failed - review the output above and re-run this script"
    fi
}

# ------------------------------------------------------------
# 7. Build & start
# ------------------------------------------------------------
echo ""
say "$T_BUILD"
compose_up

# Create singbox-node container so the agent can manage it later via docker start/stop
echo ""
say "$T_PREP_SING"
$COMPOSE_CMD create singbox-node 2>/dev/null || true

echo ""
echo "$T_DONE"
echo ""

# Success summary box with local proxy ports
box_line() { printf '║  %s' "$1"; local pad=$((BOX_WIDTH - ${#1} - 2)); printf '%*s║\n' "$pad" ""; }
BOX_WIDTH=$(( $(printf '%s\n' "$T_SUMMARY_TITLE" "" "  $T_SUMMARY_SOCKS : 127.0.0.1:6357" "  $T_SUMMARY_HTTP  : 127.0.0.1:6358" "  $T_GLOBAL_CMD" | wc -L) + 4 ))
printf '╔'
printf '═%.0s' $(seq 1 "$BOX_WIDTH")
printf '╗\n'
box_line "$T_SUMMARY_TITLE"
box_line ""
box_line "  $T_SUMMARY_SOCKS : 127.0.0.1:6357"
box_line "  $T_SUMMARY_HTTP  : 127.0.0.1:6358"
box_line ""
box_line "  $T_GLOBAL_CMD"
printf '╚'
printf '═%.0s' $(seq 1 "$BOX_WIDTH")
printf '╝\n'

echo ""
echo "$T_QUICK"
echo "   $T_Q_STATUS  cd \"$AGENT_DIR\" && bash fleet-cli.sh"
echo "   $T_Q_LOGS    docker logs -f node-agent"
echo "   $T_Q_STOP    cd \"$AGENT_DIR\" && $COMPOSE_CMD down"
echo ""

# NOTE: the installer intentionally exits WITHOUT launching the CLI. Set the
# subscription URL from the Web UI, the Telegram bot, or by running
# `bash fleet-cli.sh` manually.