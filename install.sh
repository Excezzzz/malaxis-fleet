#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  Malaxis Fleet - one-shot VPS installer
#  Tested on Debian 11/12 & Ubuntu 22.04/24.04 (x86_64/ARM64)
#
#  TWO INSTALL MODES:
#    [1] Simple  - for regular users who just want everything to
#                  work. Only asks for your domain and password,
#                  auto-generates the rest, uses the pre-built
#                  Docker image. No Docker knowledge needed.
#    [2] Advanced - for users who know what Docker is: build from
#                  source, per-subdomain control, Telegram bot
#                  integration, swap control, etc.
# ============================================================

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

say()  { printf "${GREEN}[+]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[!]${NC} %s\n" "$*"; }
err()  { printf "${RED}[x]${NC} %s\n" "$*"; exit 1; }

# ------------------------------------------------------------
# 0. Language selector (first step, before any other output)
# ------------------------------------------------------------
echo ""
echo "Select installer language / Выберите язык установки:"
echo "  [1] Русский (По умолчанию / Default)"
echo "  [2] English"
read -r -p "> " LANG_CHOICE
LANG_CHOICE="${LANG_CHOICE:-1}"

if [[ "$LANG_CHOICE" == "2" || "$LANG_CHOICE" == "en" || "$LANG_CHOICE" == "EN" ]]; then
  L="en"
else
  L="ru"
fi

# Localized strings
if [[ "$L" == "en" ]]; then
  T_ROOT="Please run as root: sudo bash install.sh"
  T_MODE_TITLE="How would you like to install?"
  T_MODE_1="[1] Simple Mode (Recommended) - for regular users. Everything is configured automatically, you only enter your domain and password. Uses the pre-built Docker image."
  T_MODE_2="[2] Advanced Mode - for users who know what Docker is. Full control: build from source, custom subdomains, Telegram bot, swap management."
  T_MODE_PROMPT="Select [1-2, default 1]"
  T_SIMPLE_SEL="Selected: Simple Mode - fully automatic installation."
  T_ADV_SEL="Selected: Advanced Mode - full control."
  T_DOMAIN_PROMPT="Enter your main domain (e.g. example.com)"
  T_DOMAIN_HINT="  The subdomains dash., api., join. and sub. will be created automatically:"
  T_DOMAIN_EMPTY="Domain cannot be empty."
  T_PASS_PROMPT="Enter password for the 'owner' account (Press Enter to auto-generate)"
  T_METHOD_TITLE="How would you like to install?"
  T_METHOD_1="  [1] Pre-built Docker Image (Fast, Low RAM - Recommended)"
  T_METHOD_2="  [2] Build from Source"
  T_METHOD_PROMPT="Select [1-2, default 1]"
  T_METHOD_SRC="Selected: Build from Source."
  T_METHOD_PRE="Selected: Pre-built Docker Image (ghcr.io/excezzzz/malaxis-fleet)."
  T_DASH_PROMPT="Dashboard domain (e.g. dash.example.com)"
  T_API_PROMPT="API domain (e.g. api.example.com)"
  T_SUB_PROMPT="Subscription domain (e.g. sub.example.com)"
  T_JOIN_PROMPT="Join domain (e.g. join.example.com)"
  T_BOT_PROMPT="Telegram bot token (optional, press Enter to skip)"
  T_CHAT_PROMPT="Telegram admin chat ID (optional)"
  T_PASSGEN="Generated strong admin password:"
  T_RES_CHECK="Checking system resources..."
  T_RAM_WARN="Detected RAM: %sMB (< 2048MB)."
  T_DISK_WARN="Only %sMB free disk space. The build requires ~5GB."
  T_DISK_CLEAN="Run system cleanup (docker prune + apt autoremove) and continue? [Y/n] "
  T_ABORT_DISK="Aborted: not enough disk space."
  T_CLEANING="Cleaning up..."
  T_DISK_AVAIL="Disk: %sMB available"
  T_INST_DEP="Installing Docker + git..."
  T_ENV_SETUP="Let's configure your deployment."
  T_ENV_DONE=".env configured."
  T_ENV_KEEP=".env already exists - keeping it."
  T_SWAP_ASK="Building from source requires ~2GB RAM. Create a temporary 2GB SWAP file? [Y/n] "
  T_SWAP_CONT="Continuing without swap. Low-memory mode will be enabled by the master server."
  T_SWAP_CREATE="Creating temporary 2GB swap file..."
  T_SWAP_ADD="Temporary swap enabled. It will be removed automatically when the build finishes."
  T_SWAP_REM="Temporary SWAP removed."
  T_BUILD="Building and starting containers (this takes a few minutes)..."
  T_PULL="Pulling pre-built image and starting containers (no compilation needed)..."
  T_CLI_INST="Global command 'malaxis-master' installed."
  T_SUMMARY_TITLE="Malaxis Fleet Master Server Installed Successfully"
  T_SAVE_CRED="Save these credentials - you will need them to log in."
  T_LOG_HINT="Logs: docker logs -f fleet-master"
  T_PRE_HINT="DNS: point the A records dash, api, join, sub at this server's IP."
  T_EXIT_HINT="Press 'q' to exit the installer: "
else
  T_ROOT="Запустите от root: sudo bash install.sh"
  T_MODE_TITLE="Как установить?"
  T_MODE_1="[1] Простой режим (Рекомендуется) - для обычных пользователей. Всё настраивается автоматически, нужно только ввести домен и пароль. Используется готовый Docker-образ."
  T_MODE_2="[2] Продвинутый режим - для пользователей, которые знают, что такое Docker. Полный контроль: сборка из исходников, свои поддомены, Telegram-бот, управление SWAP."
  T_MODE_PROMPT="Выберите [1-2, по умолчанию 1]"
  T_SIMPLE_SEL="Выбран: Простой режим - полностью автоматическая установка."
  T_ADV_SEL="Выбран: Продвинутый режим - полный контроль."
  T_DOMAIN_PROMPT="Введите ваш основной домен (например example.com)"
  T_DOMAIN_HINT="  Поддомены dash., api., join. и sub. будут созданы автоматически:"
  T_DOMAIN_EMPTY="Домен не может быть пустым."
  T_PASS_PROMPT="Введите пароль для учётной записи 'owner' (Enter - сгенерировать автоматически)"
  T_METHOD_TITLE="Как установить?"
  T_METHOD_1="  [1] Готовый Docker-образ (Быстро, мало RAM - Рекомендуется)"
  T_METHOD_2="  [2] Собрать из исходников"
  T_METHOD_PROMPT="Выберите [1-2, по умолчанию 1]"
  T_METHOD_SRC="Выбрано: Сборка из исходников."
  T_METHOD_PRE="Выбрано: Готовый Docker-образ (ghcr.io/excezzzz/malaxis-fleet)."
  T_DASH_PROMPT="Домен панели (например dash.example.com)"
  T_API_PROMPT="Домен API (например api.example.com)"
  T_SUB_PROMPT="Домен подписок (например sub.example.com)"
  T_JOIN_PROMPT="Домен подключения (например join.example.com)"
  T_BOT_PROMPT="Токен Telegram-бота (необязательно, Enter - пропустить)"
  T_CHAT_PROMPT="Admin chat ID для Telegram (необязательно)"
  T_PASSGEN="Сгенерирован надёжный пароль администратора:"
  T_RES_CHECK="Проверка ресурсов системы..."
  T_RAM_WARN="Обнаружено RAM: %sMB (< 2048MB)."
  T_DISK_WARN="Свободно всего %sMB. Для сборки нужно ~5GB."
  T_DISK_CLEAN="Запустить очистку системы (docker prune + apt autoremove) и продолжить? [Y/n] "
  T_ABORT_DISK="Прервано: недостаточно места на диске."
  T_CLEANING="Очистка..."
  T_DISK_AVAIL="Диск: доступно %sMB"
  T_INST_DEP="Установка Docker + git..."
  T_ENV_SETUP="Настроим ваш деплой."
  T_ENV_DONE=".env настроен."
  T_ENV_KEEP=".env уже существует - оставляем как есть."
  T_SWAP_ASK="Сборка из исходников требует ~2GB RAM. Создать временный SWAP-файл на 2GB? [Y/n] "
  T_SWAP_CONT="Продолжаем без SWAP. Мастер-сервер включит режим низкого потребления памяти."
  T_SWAP_CREATE="Создание временного SWAP-файла на 2GB..."
  T_SWAP_ADD="Временный SWAP включён. Он будет автоматически удалён после сборки."
  T_SWAP_REM="Временный SWAP удалён."
  T_BUILD="Сборка и запуск контейнеров (займёт несколько минут)..."
  T_PULL="Загрузка готового образа и запуск контейнеров (компиляция не нужна)..."
  T_CLI_INST="Глобальная команда 'malaxis-master' установлена."
  T_SUMMARY_TITLE="Malaxis Fleet Master Server успешно установлен"
  T_SAVE_CRED="Сохраните эти данные - они понадобятся для входа."
  T_LOG_HINT="Логи: docker logs -f fleet-master"
  T_PRE_HINT="DNS: укажите A-записи dash, api, join, sub на IP этого сервера."
  T_EXIT_HINT="Нажмите 'q' для выхода из установщика: "
fi

# ------------------------------------------------------------
# 1. Sanity checks
# ------------------------------------------------------------
if [[ $EUID -ne 0 ]]; then
  err "$T_ROOT"
fi

if ! command -v curl >/dev/null 2>&1; then
  apt-get update -y >/dev/null
  apt-get install -y curl >/dev/null
fi

echo -e "\n${CYAN}================================================${NC}"
echo -e "${CYAN}     Malaxis Fleet - Master Server Installer${NC}"
echo -e "${CYAN}================================================${NC}\n"

# ------------------------------------------------------------
# 2. Mode selection
# ------------------------------------------------------------
echo -e "${CYAN}$T_MODE_TITLE${NC}"
echo "$T_MODE_1"
echo "$T_MODE_2"
read -r -p "$T_MODE_PROMPT: " MODE
MODE="${MODE:-1}"
INSTALL_MODE="simple"
if [[ "$MODE" == "2" ]]; then
  INSTALL_MODE="advanced"
  say "$T_ADV_SEL"
else
  say "$T_SIMPLE_SEL"
fi

# ------------------------------------------------------------
# 3. Resource checks
# ------------------------------------------------------------
say "$T_RES_CHECK"

TOTAL_RAM_MB=$(free -m 2>/dev/null | awk '/^Mem:/{print $2}' || echo 4096)
DISK_AVAIL_MB=$(df -Pm / 2>/dev/null | awk 'NR==2 {print $4}' || echo 10240)
TOTAL_RAM_MB="${TOTAL_RAM_MB:-4096}"
DISK_AVAIL_MB="${DISK_AVAIL_MB:-10240}"

if [[ "$TOTAL_RAM_MB" -lt 2048 ]]; then
  warn "$(printf "$T_RAM_WARN" "$TOTAL_RAM_MB")"
fi

if [[ "$DISK_AVAIL_MB" -lt 5120 ]]; then
  warn "$(printf "$T_DISK_WARN" "$DISK_AVAIL_MB")"
  read -r -p "$T_DISK_CLEAN" CLEAN_ANS
  if [[ "$CLEAN_ANS" =~ ^[Nn]$ ]]; then
    err "$T_ABORT_DISK"
  fi
  say "$T_CLEANING"
  (command -v docker >/dev/null 2>&1 && docker system prune -af >/dev/null 2>&1) || true
  apt-get autoremove -y >/dev/null || true
  apt-get clean >/dev/null || true
fi
say "$(printf "$T_DISK_AVAIL" "$DISK_AVAIL_MB")"

# ------------------------------------------------------------
# 4. Install dependencies
# ------------------------------------------------------------
say "$T_INST_DEP"
apt-get update -y >/dev/null

# On Debian the `docker-compose-plugin` package only exists in Docker's
# official repository (the distro repos ship docker.io but no compose v2
# plugin). Add the Docker repo first when the plugin is missing so the
# install below succeeds everywhere. Ubuntu ships it in universe.
if ! apt-cache show docker-compose-plugin >/dev/null 2>&1; then
  . /etc/os-release
  CODENAME="${VERSION_CODENAME:-$(apt-cache policy docker.io | awk '/Candidate/{print $2; exit}')}"
  if [[ -n "${ID:-}" && -n "$CODENAME" ]]; then
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$ID $CODENAME stable" > /etc/apt/sources.list.d/docker.list
    apt-get update -y >/dev/null
  fi
fi

apt-get install -y git curl docker.io docker-compose-plugin >/dev/null
systemctl enable --now docker >/dev/null 2>&1 || true

# ------------------------------------------------------------
# 5. Clone the repository
# ------------------------------------------------------------
INSTALL_DIR="${INSTALL_DIR:-/opt/malaxis-fleet}"
if [[ ! -d "$INSTALL_DIR/.git" ]]; then
  mkdir -p "$INSTALL_DIR"
  say "Cloning malaxis-fleet into $INSTALL_DIR..."
  git clone https://github.com/Excezzzz/malaxis-fleet.git "$INSTALL_DIR" 2>/dev/null || {
    git clone git@github.com:Excezzzz/malaxis-fleet.git "$INSTALL_DIR"
  }
fi
cd "$INSTALL_DIR"

# ------------------------------------------------------------
# 6. Configure .env
# ------------------------------------------------------------
gen_secret() { openssl rand -hex 32 2>/dev/null || head -c 32 /dev/urandom | xxd -p; }

if [[ ! -f .env ]]; then
  cp .env.example .env
  say "$T_ENV_SETUP"

  if [[ "$INSTALL_MODE" == "simple" ]]; then
    # ---- Simple mode: only the domain + password, everything else automatic
    while [[ -z "${BASE_DOMAIN:-}" ]]; do
      read -r -p "$T_DOMAIN_PROMPT: " BASE_DOMAIN
      if [[ -z "$BASE_DOMAIN" ]]; then
        warn "$T_DOMAIN_EMPTY"
      fi
    done
    BASE_DOMAIN="${BASE_DOMAIN// /}"
    DASH_DOMAIN="dash.$BASE_DOMAIN"
    API_DOMAIN="api.$BASE_DOMAIN"
    SUB_DOMAIN="sub.$BASE_DOMAIN"
    JOIN_DOMAIN="join.$BASE_DOMAIN"
    echo "$T_DOMAIN_HINT"
    say "  $DASH_DOMAIN"
    say "  $API_DOMAIN"
    say "  $SUB_DOMAIN"
    say "  $JOIN_DOMAIN"

    read -r -p "$T_PASS_PROMPT: " ADMIN_PASS
    if [[ -z "$ADMIN_PASS" ]]; then
      ADMIN_PASS=$(gen_secret | head -c 16)
      say "$T_PASSGEN $ADMIN_PASS"
    fi

    BUILD_FROM_SOURCE=0
    BOT_TOKEN=""
    ADMIN_CHAT_ID=""
  else
    # ---- Advanced mode: full control
    echo ""
    echo -e "${CYAN}$T_METHOD_TITLE${NC}"
    echo "$T_METHOD_1"
    echo "$T_METHOD_2"
    read -r -p "$T_METHOD_PROMPT: " METHOD
    METHOD="${METHOD:-1}"
    BUILD_FROM_SOURCE=0
    if [[ "$METHOD" == "2" ]]; then
      BUILD_FROM_SOURCE=1
      say "$T_METHOD_SRC"
    else
      say "$T_METHOD_PRE"
    fi

    read -r -p "$T_DASH_PROMPT: " DASH_DOMAIN
    read -r -p "$T_API_PROMPT: " API_DOMAIN
    read -r -p "$T_SUB_PROMPT: " SUB_DOMAIN
    read -r -p "$T_JOIN_PROMPT: " JOIN_DOMAIN
    read -r -p "Enter password for the default 'owner' user (Press Enter to use default 'owner'): " ADMIN_PASS
    ADMIN_PASS="${ADMIN_PASS:-owner}"
    read -r -p "$T_BOT_PROMPT: " BOT_TOKEN
    read -r -p "$T_CHAT_PROMPT: " ADMIN_CHAT_ID
  fi

  sed -i "s|^DASHBOARD_DOMAIN=.*|DASHBOARD_DOMAIN=${DASH_DOMAIN}|" .env
  sed -i "s|^API_DOMAIN=.*|API_DOMAIN=${API_DOMAIN}|" .env
  sed -i "s|^SUB_DOMAIN=.*|SUB_DOMAIN=${SUB_DOMAIN}|" .env
  sed -i "s|^JOIN_DOMAIN=.*|JOIN_DOMAIN=${JOIN_DOMAIN}|" .env
  sed -i "s|^ADMIN_USER=.*|ADMIN_USER=owner|" .env
  sed -i "s|^ADMIN_PASS=.*|ADMIN_PASS=${ADMIN_PASS}|" .env
  sed -i "s|^BOT_TOKEN=.*|BOT_TOKEN=${BOT_TOKEN}|" .env
  sed -i "s|^ADMIN_CHAT_ID=.*|ADMIN_CHAT_ID=${ADMIN_CHAT_ID}|" .env
  sed -i "s|^SECRET_TOKEN=.*|SECRET_TOKEN=$(gen_secret)|" .env
  sed -i "s|^SESSION_SECRET=.*|SESSION_SECRET=$(gen_secret)|" .env
  sed -i "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$(gen_secret)|" .env
  say "$T_ENV_DONE"
else
  say "$T_ENV_KEEP"
fi

# Temporary swap lifecycle: created only for source builds on low-RAM hosts,
# never persisted in /etc/fstab, and ALWAYS removed when the script exits
# (success or failure).
SWAP_CREATED=0
cleanup_swap() {
  if [[ "$SWAP_CREATED" == "1" ]]; then
    swapoff /swapfile 2>/dev/null || true
    rm -f /swapfile 2>/dev/null || true
    say "$T_SWAP_REM"
  fi
}
trap cleanup_swap EXIT

# Resolve credentials for the summary box (fresh install uses the prompt above;
# existing deployments read the values back from .env).
env_get() { sed -n "s/^$1=\"\?\([^\"]*\)\"\?\$/\1/p" .env 2>/dev/null | head -n1; }

ADMIN_USER="${ADMIN_USER:-$(env_get ADMIN_USER)}"
ADMIN_USER="${ADMIN_USER:-owner}"
ADMIN_PASS="${ADMIN_PASS:-$(env_get ADMIN_PASS)}"
ADMIN_PASS="${ADMIN_PASS:-owner}"
DASH_DOMAIN="${DASH_DOMAIN:-$(env_get DASHBOARD_DOMAIN)}"
DASH_DOMAIN="${DASH_DOMAIN:-dash.yourdomain.com}"

# ------------------------------------------------------------
# 7. Ensure the shared Caddy network exists
# ------------------------------------------------------------
# docker-compose.yml attaches fleet-master to the external "caddy" network
# (the TLS-terminating reverse proxy). On a fresh host that network does not
# exist yet, and `docker compose up` fails with
# "network caddy declared as external, but could not be found".
if ! docker network inspect caddy >/dev/null 2>&1; then
  docker network create caddy >/dev/null
  say "Created Docker network 'caddy' (required by docker-compose.yml)."
fi

# ------------------------------------------------------------
# 8. Build & start
# ------------------------------------------------------------
if [[ "${BUILD_FROM_SOURCE:-0}" == "1" ]]; then
  if [[ "$TOTAL_RAM_MB" -lt 2048 ]]; then
    echo ""
    read -r -p "$T_SWAP_ASK" SWAP_ANS
    if [[ "$SWAP_ANS" =~ ^[Nn]$ ]]; then
      warn "$T_SWAP_CONT"
    else
      say "$T_SWAP_CREATE"
      fallocate -l 2G /swapfile 2>/dev/null || dd if=/dev/zero of=/swapfile bs=1M count=2048
      chmod 600 /swapfile
      mkswap /swapfile >/dev/null
      swapon /swapfile
      SWAP_CREATED=1
      say "$T_SWAP_ADD"
    fi
  fi
  say "$T_BUILD"
  docker compose up -d --build
else
  say "$T_PULL"
  docker compose up -d
fi

# Global "malaxis-master" convenience CLI
ln -sf "$INSTALL_DIR/scripts/master-cli.sh" /usr/local/bin/malaxis-master
chmod +x "$INSTALL_DIR/scripts/master-cli.sh"
say "$T_CLI_INST"

# ------------------------------------------------------------
# 9. Summary & exit
# ------------------------------------------------------------
clear 2>/dev/null || true
echo -e "\n${GREEN}========================================================${NC}"
echo -e "${GREEN}    $T_SUMMARY_TITLE${NC}"
echo -e "${GREEN}========================================================${NC}"
echo ""
echo -e "${CYAN}  Dashboard URL :${NC} https://${DASH_DOMAIN}"
echo -e "${CYAN}  Username      :${NC} ${ADMIN_USER}"
echo -e "${CYAN}  Password      :${NC} ${ADMIN_PASS}"
echo ""
echo -e "${YELLOW}  $T_SAVE_CRED${NC}"
echo ""
echo -e "${GREEN}  $T_LOG_HINT${NC}"
echo -e "${CYAN}  $T_PRE_HINT${NC}"
if [[ "$INSTALL_MODE" == "simple" ]]; then
  echo -e "${CYAN}  DNS: ${DASH_DOMAIN}, ${API_DOMAIN}, ${JOIN_DOMAIN}, ${SUB_DOMAIN}${NC}"
fi
echo -e "${GREEN}========================================================${NC}\n"

# Wait for 'q' only when a real terminal is attached (piped/CI runs finish
# immediately instead of looping on EOF).
if [[ -t 0 ]]; then
  while true; do
      read -n 1 -s -r -p "$T_EXIT_HINT" key
      if [[ $key == "q" || $key == "Q" ]]; then echo ""; break; fi
  done
fi