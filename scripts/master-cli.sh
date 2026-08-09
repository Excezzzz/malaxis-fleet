#!/usr/bin/env bash
# Malaxis Fleet - Master Server Convenience CLI
# Provides quick access to logs, stack restart, and manual DB backups.
# Installed globally as 'malaxis-master' by install.sh.

set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/malaxis-fleet}"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

say()  { printf "${GREEN}[+]${NC} %s\n" "$*"; }
warn() { printf "${YELLOW}[!]${NC} %s\n" "$*"; }
err()  { printf "${RED}[x]${NC} %s\n" "$*"; }

view_master_logs() {
  echo ""
  say "Streaming fleet-master logs (Ctrl+C to stop)..."
  docker logs -f fleet-master || docker logs fleet-master --tail 50
  pause
}

view_caddy_logs() {
  echo ""
  say "Streaming Caddy proxy logs (Ctrl+C to stop)..."
  docker logs -f caddy || docker logs caddy --tail 50
  pause
}

restart_stack() {
  echo ""
  say "Restarting master server stack..."
  cd "$INSTALL_DIR"
  docker compose restart
  say "Stack restarted."
  pause
}

backup_db() {
  echo ""
  say "Creating manual database backup..."
  local env_file="$INSTALL_DIR/.env"
  local pg_user pg_db
  pg_user=$(sed -n 's/^POSTGRES_USER="\?\([^"]*\)"\?$/\1/p' "$env_file" | head -n1)
  pg_db=$(sed -n 's/^POSTGRES_DB="\?\([^"]*\)"\?$/\1/p' "$env_file" | head -n1)
  pg_user="${pg_user:-fleet_internal}"
  pg_db="${pg_db:-fleet_db}"

  local stamp out
  stamp=$(date +%Y%m%d-%H%M%S)
  mkdir -p "$INSTALL_DIR/backups"
  out="$INSTALL_DIR/backups/manual-$stamp.sql"
  if docker exec fleet-postgres pg_dump -U "$pg_user" "$pg_db" > "$out" 2>/dev/null; then
    say "Backup saved: $out"
  else
    err "Database backup failed. Is the stack running?"
  fi
  pause
}

pause() {
  echo ""
  read -r -p "Press Enter to return to the menu..."
}

show_menu() {
  while true; do
    clear
    echo -e "${CYAN}============================================${NC}"
    echo -e "${CYAN}      Malaxis Master CLI${NC}"
    echo -e "${CYAN}============================================${NC}"
    echo ""
    echo " 1) View Master Logs"
    echo " 2) View Caddy Proxy Logs"
    echo " 3) Restart Master Server Stack"
    echo " 4) Manual Database Backup"
    echo " 5) Exit"
    echo ""
    read -r -p "Select option [1-5]: " choice
    case "$choice" in
      1) view_master_logs ;;
      2) view_caddy_logs ;;
      3) restart_stack ;;
      4) backup_db ;;
      5) clear; exit 0 ;;
      *) ;;
    esac
  done
}

if [[ ! -d "$INSTALL_DIR/.git" ]] && [[ ! -f "$INSTALL_DIR/docker-compose.yml" ]]; then
  err "Fleet not found at $INSTALL_DIR. Set INSTALL_DIR or re-run install.sh."
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  err "Docker is not installed or not in PATH."
  exit 1
fi

show_menu
