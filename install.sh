#!/usr/bin/env bash
set -euo pipefail

# ============================================================
#  Malaxis Fleet - one-shot VPS installer
#  Tested on Debian 11/12 & Ubuntu 22.04/24.04 (x86_64/ARM64)
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
# 0. Sanity checks
# ------------------------------------------------------------
if [[ $EUID -ne 0 ]]; then
  err "Please run as root: sudo bash install.sh"
fi

if ! command -v curl >/dev/null 2>&1; then
  apt-get update -y >/dev/null
  apt-get install -y curl >/dev/null
fi

echo -e "\n${CYAN}================================================${NC}"
echo -e "${CYAN}     Malaxis Fleet - Master Server Installer${NC}"
echo -e "${CYAN}================================================${NC}\n"

# ------------------------------------------------------------
# 1. Resource checks
# ------------------------------------------------------------
say "Checking system resources..."

TOTAL_RAM_MB=$(free -m | awk '/^Mem:/{print $2}')
DISK_AVAIL_MB=$(df -Pm / | awk 'NR==2 {print $4}')

if [[ "$TOTAL_RAM_MB" -lt 2048 ]]; then
  warn "Detected RAM: ${TOTAL_RAM_MB}MB (< 2048MB recommended)."
  read -r -p "Create a 2GB swap file to continue? [Y/n] " SWAP_ANS
  if [[ "$SWAP_ANS" =~ ^[Nn]$ ]]; then
    warn "Continuing without swap. Low-memory mode will be enabled by the master server."
  else
    say "Creating 2GB swap file..."
    fallocate -l 2G /swapfile 2>/dev/null || dd if=/dev/zero of=/swapfile bs=1M count=2048
    chmod 600 /swapfile
    mkswap /swapfile >/dev/null
    swapon /swapfile
    grep -q "/swapfile" /etc/fstab || echo "/swapfile none swap sw 0 0" >> /etc/fstab
    say "Swap enabled."
  fi
else
  say "RAM: ${TOTAL_RAM_MB}MB"
fi

if [[ "$DISK_AVAIL_MB" -lt 5120 ]]; then
  warn "Only ${DISK_AVAIL_MB}MB free disk space. The build requires ~5GB."
  read -r -p "Run system cleanup (docker prune + apt autoremove) and continue? [Y/n] " CLEAN_ANS
  if [[ "$CLEAN_ANS" =~ ^[Nn]$ ]]; then
    err "Aborted: not enough disk space."
  fi
  say "Cleaning up..."
  (command -v docker >/dev/null 2>&1 && docker system prune -af >/dev/null 2>&1) || true
  apt-get autoremove -y >/dev/null || true
  apt-get clean >/dev/null || true
fi
say "Disk: ${DISK_AVAIL_MB}MB available"

# ------------------------------------------------------------
# 2. Install dependencies
# ------------------------------------------------------------
say "Installing Docker + git..."
apt-get update -y >/dev/null
apt-get install -y git curl docker.io docker-compose-plugin >/dev/null
systemctl enable --now docker >/dev/null 2>&1 || true

# ------------------------------------------------------------
# 3. Clone the repository
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
# 4. Configure .env
# ------------------------------------------------------------
if [[ ! -f .env ]]; then
  cp .env.example .env
  say "Let's configure your deployment."

  gen_secret() { openssl rand -hex 32 2>/dev/null || head -c 32 /dev/urandom | xxd -p; }

  read -r -p "Dashboard domain (e.g. dash.example.com): " DASH_DOMAIN
  read -r -p "API domain (e.g. api.example.com): " API_DOMAIN
  read -r -p "Subscription domain (e.g. sub.example.com): " SUB_DOMAIN
  read -r -p "Join domain (e.g. join.example.com): " JOIN_DOMAIN
  read -r -p "Enter password for the default 'owner' user (Press Enter to use default 'owner'): " ADMIN_PASS
  ADMIN_PASS="${ADMIN_PASS:-owner}"
  read -r -p "Telegram bot token (optional, press Enter to skip): " BOT_TOKEN
  read -r -p "Telegram admin chat ID (optional): " ADMIN_CHAT_ID

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
  say ".env configured."
else
  say ".env already exists - keeping it."
fi

# Resolve credentials for the summary box (fresh install uses the prompt above;
# existing deployments read the values back from .env).
ADMIN_USER="${ADMIN_USER:-owner}"
if [[ -z "${ADMIN_PASS:-}" ]] && [[ -f .env ]]; then
  ADMIN_PASS=$(sed -n 's/^ADMIN_PASS="\?\([^"]*\)"\?$/\1/p' .env | head -n1)
fi
ADMIN_PASS="${ADMIN_PASS:-owner}"
DASH_DOMAIN="${DASH_DOMAIN:-dash.yourdomain.com}"

# ------------------------------------------------------------
# 5. Build & start
# ------------------------------------------------------------
say "Building and starting containers (this takes a few minutes)..."
docker compose up -d --build

# ------------------------------------------------------------
# 6. Summary & exit
# ------------------------------------------------------------
clear
echo -e "\n${GREEN}========================================================${NC}"
echo -e "${GREEN}    Malaxis Fleet Master Server Installed Successfully${NC}"
echo -e "${GREEN}========================================================${NC}"
echo ""
echo -e "${CYAN}  Dashboard URL :${NC} https://${DASH_DOMAIN}"
echo -e "${CYAN}  Username      :${NC} ${ADMIN_USER}"
echo -e "${CYAN}  Password      :${NC} ${ADMIN_PASS}"
echo ""
echo -e "${YELLOW}  Save these credentials — you will need them to log in.${NC}"
echo ""
echo -e "${GREEN}  Logs: docker logs -f fleet-master${NC}"
echo -e "${GREEN}========================================================${NC}\n"

while true; do
    read -n 1 -s -r -p "Press 'q' to exit the installer: " key
    if [[ $key == "q" || $key == "Q" ]]; then echo ""; break; fi
done
