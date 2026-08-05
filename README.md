<div align="center">

# 🛰️ Malaxis Fleet Manager

### *v2.1-BETA — The Anti-DPI VPN Fleet Console*

**A premium, self-hosted Enterprise VPN Fleet Manager** designed for absolute **DPI evasion**, **anti-freeze stability**, and **seamless client orchestration** across unlimited nodes — all from a single glassmorphic dashboard or a Telegram bot.

---

![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![Vue](https://img.shields.io/badge/Vue%203-4FC08D?style=for-the-badge&logo=vue.js&logoColor=white)
![Tailwind](https://img.shields.io/badge/Tailwind%20CSS-06B6D4?style=for-the-badge&logo=tailwindcss&logoColor=white)
![Python](https://img.shields.io/badge/Python-3.12-3776AB?style=for-the-badge&logo=python&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?style=for-the-badge&logo=postgresql&logoColor=white)
![Docker](https://img.shields.io/badge/Docker-2496ED?style=for-the-badge&logo=docker&logoColor=white)

**Open-Source** · **Self-Hosted** · **DPI-Safe** · **Anti-Freeze**

</div>

---

## 📖 Description

Malaxis Fleet Manager is a **turnkey VPN fleet orchestrator** that fuses a hardened **Go** control-plane, an embedded **Vue 3 dashboard**, a **Docker-in-Docker Python agent** for every node, and a **PostgreSQL** backend into one binary-less, subdomain-routed product.

Point a handful of **A-records** at one Caddy server, run a single PowerShell command, drop a `curl` one-liner on each node — and the fleet is live. From there you can:

- 🧲 **Onboard unlimited nodes** with hardware-fingerprinted identity
- 🧪 **Auto-benchmark** servers and hot-switch nodes to the fastest path
- 🕹️ **Drive everything** from a Telegram bot with a single inline message
- ☁️ **Push OTA updates** to gun the entire fleet's client scripts in one click

Built to withstand hostile networks: **XHTTP + xPaddingBytes**, **route-only sniffing**, **DoH resolution**, **MTProto/QUIC bypass rules**, and **sub-second TCP timeouts** keep Telegram & video calls smooth even under aggressive DPI.

---

## ✨ Key Features

### 🛡️ Anti-DPI & Anti-Freeze
- **XHTTP stream multiplexing (`xmux`)** — `maxConnections: 4` + adaptive request windows (`800-900` ms) eliminate the single-stream media bottleneck.
- **`xPaddingBytes`** padding + randomized TLS fingerprints to defeat passive DPI.
- **`tcpNoDelay` + `tcpKeepAliveInterval` + `tcpUserTimeout`** — dead links are detected and dropped in ~15 s, stopping infinite micro-freezes.
- **Route-only sniffing** (`routeOnly`, `destOverride: [http, tls, quic]`) — destination is never rewritten, so Telegram's **MTProto / QUIC** bypass slow sniff-based DNS fallbacks entirely.
- **Telegram MTU IP bypass routing** — `91.108.0.0/16`, `149.154.160.0/20`, `185.76.151.0/24` are pinned to the proxy path without sniffing.
- **DoH DNS (DNS-over-HTTPS)** with `ipv4_only` strategy — no plaintext DNS leaks, IPv4-only path for fewer handshake stalls.

### 🌐 Sing-box First
- Native, first-class orchestration of **both [Xray](https://github.com/XTLS/Xray-core) and [sing-box](https://sing-box.sagernet.org)** engines collocated on a **Docker-in-Docker** bridge network.
- Config generation at runtime for **VLESS, VMess, Trojan, Shadowsocks, Hysteria2, TUIC, WireGuard** — over **tcp / ws / grpc / kcp / quic / httpupgrade / xhttp** transports.
- Both engines expose a `SOCKS5 :6357` / `HTTP :6358` interface that local apps point at.
- Graceful sing-box→Xray fallback for transports sing-box can't natively carry (e.g. xhttp).

### 🧠 Smart Modes
- **Auto-benchmarking** on the node (`fastest` / `balanced`) probe latency, jitter, and loss via real TCP/UDP pings.
- **⚡ Fastest** — selects lowest-latency server. · **⚖️ Balanced** — selects lowest jitter/loss.
- **Cached benchmark** (TTL 10 min) — no ping spam on every switch or worker tick.
- One-tap hot-switch from the dashboard or bot: `node:switch:<id>:fastest` / `:balanced` / `:<server>`.

### 🎨 Premium UI
- **Vue 3 + Tailwind CSS** — modern **Floating Island Glassmorphism** design with glass cards, glassy stat tiles, and a buttery dark gradient.
- **Vite-embedded** — the compiled front-end ships *inside* the Go binary; zero static-file server to run.
- Real-time node health, pipeline statuses (`queued / running / done / failed`), and toast feedback for every action.

### 🔗 Hardware Deduplication
- Each node computes a **SHA-256 hardware fingerprint** (hostname + primary MAC + system serial).
- Reinstalls **keep their canonical node ID** — ghost/duplicate nodes are automatically merged, never double-billed, never confused.

### 🤖 Telegram Bot
- **Single-message inline UI**: one message, all the buttons, no spam.
- Full admin control: list nodes, per-node details, switch server/mode, rename, delete, soft-delete.
- 💥 **Terminate & Self-Destruct** — remote wipe mode for decommissioned hardware.
- **Task queuing** — action queuing with live status for every node (`SetPendingCommand`).
- **PG Backup on demand** — `.zip` PostgreSQL backup, downloadable straight from Telegram.
- **ONE-button OTA** for pushing client scripts / subscriptions fleet-wide.

### 📦 Remote OTA Updates
- **1-click client update**: the agent pulls fresh `node_agent.py`, `join.sh`, compose, Dockerfile, entrypoint, and default xray/sing-box configs from the fleet's OSS hosted on the same subdomain.
- **1-click subscription refresh** — regenerates and pushes all node subscriptions without touching NGINX.

---

## 🛠️ Tech Stack

| Layer          | Technology                                        |
|----------------|---------------------------------------------------|
| **Backend**    | Go 1.22+ (single self-contained binary, `net/http`) |
| **Frontend**   | Vue 3 + Tailwind CSS (embedded in the Go binary)  |
| **Agent**      | Python 3.12 (D0D Docker agent, runs in Docker)    |
| **Database**   | PostgreSQL 16 (internal Docker network only)      |
| **Proxy core** | Xray-core & sing-box (Docker-in-Docker)           |
| **Ingress**     | Caddy 2 (auto TLS terminator to Cloudflare/Grey)  |

> **Anti-leak:** no port is ever published on `fleet-master` — PostgreSQL and the Go backend are reached *only* over internal bridge networks + Caddy. Not a single host port exposed.

---

## 🚀 Quickstart

### 1. Configure

```bash
cp .env.example .env
```

Fill in your repo:

```ini
POSTGRES_USER="fleet_internal"       # you will change this
POSTGRES_PASSWORD="your-strong-pass"
ADMIN_USER="admin"
ADMIN_PASS="your-strong-pass"
BOT_TOKEN="1234567890:AA..."  # your Telegram bot
ADMIN_CHAT_ID="123456789"
API_DOMAIN="api.yourdomain.com"        # → your VPS DNS A records
DASHBOARD_DOMAIN="dash.yourdomain.com" #    (point to the Caddy server)
JOIN_DOMAIN="join.yourdomain.com"
SUB_DOMAIN="sub.yourdomain.com"
```

> ⚠️ **Never commit** `.env` — it's git-ignored. Use `.env.example` for the template.

### 2. Deploy the Fleet Server

```powershell
.\build_and_deploy.ps1 root@your-server-ip
```

The script will:

1. `npm install` + build the Vue frontend
2. Cross-compile the Go backend (`GOOS=linux`)
3. `scp` the binary, Dockerfile, compose, and `.env` up
4. `docker-compose up -d --build` — **fleet-master** + **fleet-postgres** on an internal net,
5. Caddy on the external `caddy` network forwards `dash/api/join/sub` subdomains to `fleet-master:8000`.

### 3. Onboarding Nodes (one-line)

On any machine with Docker:

```bash
curl -sSL https://join.yourdomain.com/join.sh | bash
```

The agent registers itself by hardware hash, pulls configs, and starts Xray + sing-box on the node. It talks back to the API over HTTPS with a shared secret token, polls for pending commands and benchmark tasks, and reports health every 60s.

### 4. Log in

```text
https://dash.yourdomain.com
```

Log in with the `ADMIN_USER` / `ADMIN_PASS` you set — then manage everything from the dashboard or the Telegram bot.

---

## 🤖 Telegram Quick Reference

```
/start                          → fresh main menu
💻 Manage Nodes                 → list all nodes, health, current engine
node:detail:<id>                → actions: Switch VPN, Set Sub URL, Rename, Delete
node:switch:<id>:fastest        → auto-bench, pick lowest-latency
node:switch:<id>:balanced       → auto-bench, pick lowest jitter/loss
🚀 Push Client Files (OTA)      → fleet-wide script/sub refresh
🔄 Refresh Subscriptions        → re-push subscriptions
📦 Download DB Backup           → .zip of the Postgres DB via bot
💥 Terminate & Self-Destruct    → remotely wipe the node
```

---

## 🔧 Under the Hood

```
┌─────────────────────────────────────────────────────────────┐
│                          CLIENT / TELEGRAM                  │
└───────────────┬─────────────────────────────┬───────────────┘
                │ (dash / telegram / curl)    │
┌───────────────▼─────────────────────────────▼───────────────┐
│                        CADDY  (TLS, 443)                    │
│  dash.yourdomain.com · api.* · join.* · sub.*   → :8000     │
└───────────────┬─────────────────────────────────────────────┘
                │  internal caddy bridge network (no host ports)
┌───────────────▼─────────────────────────────────────────────┐
│              fleet-master  (Go · :8000, internal)           │
│           Postgres · Tasks · Bot · OTA store · OSS         │
└───────────────┬─────────────────────────────────────────────┘
                │ HTTPS (api / sub domains)
        ┌───────▼───────┐        ┌──────────────────────────┐
        │   Fleet Node   │        │  join.sh → docker-compose │
        │  (Python agent)│ ◄─────►│  xray-node :6357/:6358    |
        │  benchmarks    │        │  singbox-node (net:cont)  │
        └───────────────┘        └──────────────────────────┘
```

---

## 📁 Project Layout

```
malaxis-fleet/
├── cmd/server/               # Go entrypoint
├── internal/
│   ├── api/                  # HTTP API, templates, agent deploy assets
│   ├── deploy/               # node_agent.py, join.sh, docker/client-compose
│   ├── bot/                 # Telegram bot (inline keyboard UI)
│   ├── config/               # ENV configuration
│   ├── store/                # PostgreSQL storage
├── scripts/                  # dev & test scripts
├── .env.example              # heavily documented env template
├── Dockerfile
├── docker-compose.yml        # master + postgres (internal networks)
└── build_and_deploy.ps1      # one-shot build → scp → compose deploy
```

---

## 🔐 Security

- Secrets live **only** in `.env` (git-ignored); the repo ships zero real credentials.
- Postgres **never exposed** — internal bridge only; strong password expected via `.env`.
- `fleet-master` **publishes no host ports**; all ingress is Caddy-terminated.
- SQL is fully **parameterized**; API inputs are strictly validated; login is rate-limited per IP.
- Agent↔master auth via Bearer `SECRET_TOKEN`. Hardware-fingerprinted node identity prevents spoofing.
- Telegram admin-only: chat ID is authoritatively checked; all bot actions gated.

---

## 🧪 Testing & CI

```bash
go vet ./... && go test ./internal/...   # backend sanity
python -m py_compile internal/api/deploy/node_agent.py
```

---

## 🛡️ License

**MIT** — commercial-friendly, self-hosted forever.

---

<div align="center">

**Built for operators who won't tolerate DPI jitters.**

⭐ Star the repo and let the fleet fly.

</div>