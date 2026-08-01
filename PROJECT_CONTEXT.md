# 🛡️ Malaxis Fleet Manager - Project Master Context & Migration Guide

## 🎯 Task Instructions for AI Coding Agent
You are an expert Senior DevOps and Golang Software Engineer.
Your goal is to migrate and refactor the existing working Python-based system into a high-performance, enterprise-grade, layered **Golang Master Server (`malaxis-fleet`)**.

---

## 🏛️ System Concept & Features to Implement

### 1. Universal Device Fleet Management
- Support for multiple device categories: **Universal Nodes** (e.g., Windows PCs, Linux Servers / VPS).
- Real-time online/offline status tracking (`🟢 Online` / `🔴 Offline`) based on a 5-second agent heartbeat (`last_seen`).
- Local LAN IP detection and external VPN IP tracking.

### 2. High-Performance Golang Architecture
- **Layered Clean Architecture**:
  - `main.go` (Entrypoint)
  - `internal/config/` (`.env` Loader with zero hardcoded credentials)
  - `internal/domain/` (Structs: Device, User, AuditLog, Outbound, Command)
  - `internal/repository/` (Thread-safe PostgreSQL database implementation)
  - `internal/service/` (Subscription parser, auto-sync worker, backup service)
  - `internal/api/` (REST API for client agents with HMAC token security)
  - `internal/bot/` (Telegram Bot with BotFather single-message HTML UI)
  - `web/` (Embedded Vue 3 + Tailwind CSS Web Dashboard via `go:embed`)

### 3. Web Dashboard UI (`https://hub.malaxis.ru`)
- Served via Caddy Reverse Proxy (`host.docker.internal:8080`).
- Modern dark-themed, mobile-responsive SPA built with **Vue 3 + Tailwind CSS** (embedded inside the Go binary).
- Real-time device cards, 1-click VPN switching, network diagnostics, and global controls.

### 4. Fleet-Wide Actions & Offline Queue
- **"Update All Devices" Button (`🔄 Update All Devices`)**:
  - Triggers an instant 3x-ui sub refresh for all devices.
  - **Online Devices**: Receive the update command immediately via `/api/poll` (within 5 sec), restart Docker, and report status.
  - **Offline Devices**: The update command is saved in the database `pending_command` queue. When the device comes online and checks in, it fetches the latest config automatically.

### 5. Deep Full-Config Auto-Sync Task (1-Hour Loop)
- Background worker runs every 60 minutes.
- Performs a deep canonical JSON comparison (`sort_keys=True`) of the complete outbound config (including SNI, Reality public keys, Short ID, UUID, Flow, Path, Host, Port, and IP).
- If ANY parameter changes on 3x-ui, it automatically queues an update for the device and notifies Telegram.

### 6. Settings Submenu & Database Backups (`⚙️ Settings`)
- Configurable auto-sync intervals (1h, 3h, 6h, 12h, 24h, Disabled).
- **Database Backups**:
  - Scheduled automated backups.
  - **"Send Backup to Telegram"** button: Sends a zip archive of the database dump directly into the Telegram admin chat!

### 7. Over-The-Air (OTA) Remote Agent Auto-Updater
- Master server can trigger updates for the client-side `node-agent`.
- Agents automatically fetch the new Docker image or script, restart the `node-agent.service` and sibling containers seamlessly.

### 8. Universal Zero-Dependency Client Deployment (DooD)
- `docker-compose.yml` (Version 3.3) running **`xray-node`**, **`singbox-node`**, and the **`node-agent`**.
- **Engine Classification**:
  - `Xray-core`: VLESS XHTTP, VLESS Vision Reality, VMess, Trojan.
  - `Sing-box`: Hysteria2, TUIC, QUIC, WireGuard.
- Exposes SOCKS5 on port `6357` and HTTP on port `6358`.
- Local state saving (`agent_state.json`) for auto-boot into last active VPN server upon device restart.
- Automatic rollback: Tests local proxy (`socks5h://127.0.0.1:6357`); if connection test fails, automatically reverts to previous working profile.

---

## 🛠️ Security & Environment Variables (.env)

All user-facing credentials must be loaded from `.env`. The database connection is configured separately in `docker-compose.yml` and the application's default settings.

### `.env.example` Template
```ini
# Telegram Bot
BOT_TOKEN="YOUR_BOT_TOKEN_HERE"
ADMIN_CHAT_ID="YOUR_ADMIN_CHAT_ID_HERE"

# Initial Admin User
ADMIN_USER="admin"
ADMIN_PASS="YOUR_STRONG_PASSWORD"

# Agent Communication & API Security
SECRET_TOKEN="YOUR_FLEET_SECRET_HERE"

# Web UI Session
SESSION_SECRET="YOUR_RANDOM_SESSION_SECRET"
```