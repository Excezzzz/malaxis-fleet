# Malaxis Fleet Manager

Malaxis Fleet Manager is a self-hosted fleet orchestration platform for
deploying and operating VPN proxy nodes at scale. A single Go control plane
manages an arbitrary number of Dockerized Python agents, each collocated with
Xray-core and sing-box engines, behind one TLS-terminating Caddy instance.

The system is designed for networks under active DPI (deep packet inspection):
transport configuration is tuned for XHTTP stream multiplexing, padding,
route-only sniffing, and DoH resolution to maintain stable media traffic over
aggressive filtering.

---

## Table of Contents

- [Overview & Architecture](#overview--architecture)
- [Features](#features)
- [Server Administration Guide](#server-administration-guide)
- [End-User Guide](#end-user-guide)
- [Security & RBAC](#security--rbac)
- [Telegram Bot Reference](#telegram-bot-reference)
- [Project Layout](#project-layout)
- [Development](#development)
- [License](#license)

---

## Overview & Architecture

The deployment is split into four DNS names, all pointed at a single Caddy
server that routes by Host header and terminates TLS:

| Domain | Role |
|--------|------|
| `dash.yourdomain.com` | Web dashboard (embedded Vue 3 SPA) |
| `api.yourdomain.com`  | Agent polling API, subscription validation |
| `join.yourdomain.com` | `join.sh` bootstrap script and client tooling |
| `sub.yourdomain.com`  | Client files: compose, Dockerfile, engine configs, OTA assets |

```
                          Clients / Telegram
                                  |
                                  v
                          Caddy (TLS, :443)
              dash.*  api.*  join.*  sub.*  ->  :8000
                                  |
                                  v
                     fleet-master (Go, :8000)
              Postgres  |  task queue  |  bot  |  OTA store
                                  ^
                                  | HTTPS (api / sub domains)
                    +-------------+-------------+
                    v                           v
            Fleet node (Python agent)    Bootstrap assets
            Xray-node :6357/:6358        (join.sh, compose)
            singbox-node (shared netns)
```

Nodes self-register with a hardware-derived identity, poll the API for pending
commands on a shared-secret bearer token, and report health and benchmark
results at regular intervals. The control plane never exposes a host port:
PostgreSQL and the Go backend are reachable only over internal bridge networks.

## Features

### XHTTP stream multiplexing (xmux)

Xray outbound connections use XHTTP `xmux` with `maxConnections: 4` and
adaptive request windows (800–900 ms). This eliminates the single-stream media
bottleneck by multiplexing a channel over multiple parallel HTTP requests, at
the cost of a bounded increase in memory per connection.

### DPI evasion

- **`xPaddingBytes`** and randomized TLS fingerprints defeat passive
  fingerprinting.
- **Route-only sniffing** (`routeOnly`, `destOverride: [http, tls, quic]`)
  leaves the destination untouched; Telegram MTProto and QUIC bypass
  sniff-based fallbacks entirely.
- **Telegram path pinning** routes `91.108.0.0/16`, `149.154.160.0/20`, and
  `185.76.151.0/24` directly through the proxy without sniffing.
- **DoH DNS** with `ipv4_only` strategy prevents plaintext DNS leaks.
- **Aggressive dead-link detection** (`tcpNoDelay`, `tcpKeepAliveInterval`,
  `tcpUserTimeout`) drops stale connections in ~15 seconds.

### Dual engine orchestration

Xray and sing-box run collocated on a Docker-in-Docker bridge network. The
agent generates engine configs at runtime for VLESS, VMess, Trojan,
Shadowsocks, Hysteria2, TUIC, and WireGuard across tcp/ws/grpc/kcp/quic/
httpupgrade/xhttp transports, and falls back from sing-box to Xray for
transports sing-box cannot carry natively. Both engines expose a local
`SOCKS5 :6357` / `HTTP :6358` interface.

### Hardware deduplication

Each node computes a SHA-256 hardware fingerprint from the hostname, primary
MAC, and system serial. Reinstalls keep the canonical node ID: duplicate
registrations are merged instead of creating ghost nodes, which prevents
double-counting and state confusion.

### Smart routing modes

- **Fastest** — selects the lowest-latency server via live TCP/UDP probing.
- **Balanced** — selects the lowest jitter/loss.
- Cached benchmark results (TTL 10 min) avoid ping spam on every switch.

### Operational tooling

- **OTA client updates** — push fresh `node_agent.py`, compose, Dockerfile,
  and engine configs fleet-wide from the dashboard or bot in one action.
- **Terminate & self-destruct** — remote wipe for decommissioned hardware.
- **Audit trail** — every administrative action is recorded with actor,
  target, and timestamp.
- **Backups** — on-demand PostgreSQL ZIP backup, downloadable from the
  dashboard or Telegram.
- **Task queuing** — per-node commands queue with live pipeline status
  (`queued / running / done / failed`).

### Stealth Mode & Active Probing Defense

- 🥷 **Stealth Mode & Active Probing Defense** — The web dashboard is fully
  camouflaged. Unauthenticated visitors see a generic, unnamed "System Sign
  In" page with no branding, preventing automated DPI scanners or government
  firewalls from identifying the VPN panel.
- 🔐 **Tokenized Payload Delivery** — Client installation scripts
  (`node_agent.py`, `join.sh`) cannot be downloaded without a dynamically
  generated `SECRET_TOKEN`, protecting the infrastructure from unauthorized
  access and reverse engineering.

## Server Administration Guide

### Prerequisites

| Requirement | Version / Notes |
|-------------|-----------------|
| Go toolchain | 1.22+ (for cross-compiling the control plane) |
| Node.js + npm | 18+ (for building the Vue 3 dashboard) |
| VPS / dedicated server | Docker Engine + Docker Compose; one public IPv4 |
| DNS | `dash`, `api`, `join`, `sub` A records pointing at the server |
| TLS | Caddy terminates TLS automatically (with a CF cert pair if you use Cloudflare proxying; paths configured via `SSL_CERT_PATH` / `SSL_KEY_PATH`) |

The server requires no exposed ports beyond `443` (Caddy). PostgreSQL and the
control plane live on internal Docker networks only.

### 1. Configure the environment

```bash
cp .env.example .env
```

Set at minimum the following values:

```ini
BOT_TOKEN="1234567890:AA..."          # Telegram bot token
ADMIN_CHAT_ID="123456789"             # admin chat ID for bot notifications
ADMIN_USER="admin"                    # initial administrator account
ADMIN_PASS="replace-with-strong-pass" # initial administrator password
SECRET_TOKEN="replace-with-random"    # shared agent auth secret
SESSION_SECRET="replace-with-random"  # session signing secret
POSTGRES_PASSWORD="replace-with-strong-pass"

API_DOMAIN="api.yourdomain.com"       # DNS A records -> Caddy server
DASHBOARD_DOMAIN="dash.yourdomain.com"
JOIN_DOMAIN="join.yourdomain.com"
SUB_DOMAIN="sub.yourdomain.com"
```

`.env` is git-ignored and must never be committed. Use `.env.example` as the
template only. Every value above is consumed at runtime by the control plane
and substituted into the client deployment assets served to nodes. The
`SECRET_TOKEN` doubles as the payload-delivery key: the bootstrap script and
all client templates are only served to requests carrying
`?t=<SECRET_TOKEN>`, and the token is injected into the script at serve time.

### 2. Deploy

**Option A — One-line server installer (Debian/Ubuntu VPS):**

```bash
curl -sSL https://raw.githubusercontent.com/Excezzzz/malaxis-fleet/main/install.sh | bash
```

The installer (run as root) verifies the account, checks RAM and disk space
(offering to create a 2 GB swap file below 2 GB RAM and running a cleanup
pass below 5 GB free disk), installs `git`, `curl`, and Docker, clones the
repository into `/opt/malaxis-fleet`, prompts for your four domains and the
master credentials, generates random `SECRET_TOKEN` / `SESSION_SECRET` /
`POSTGRES_PASSWORD` values into `.env`, and launches the fleet with
`docker compose up -d --build`.

**Option B — From a Windows workstation with Go and Node.js installed:**

```powershell
.\build_and_deploy.ps1 admin@your-server-ip
```

The script performs the following steps:

1. Installs dependencies and builds the Vue 3 frontend.
2. Cross-compiles the Go backend for `linux/amd64`.
3. Copies the binary, Dockerfile, compose file, and `.env` to the server.
4. Starts `fleet-master` and `fleet-postgres` on an internal network, with
   Caddy forwarding the four subdomains to `fleet-master:8000`.

### 3. Log in

Open `https://dash.yourdomain.com` and sign in with the `ADMIN_USER` /
`ADMIN_PASS` values from `.env` (defaults: `admin` / `admin` if not set).
The seeded account is the fleet owner (rank 100) and is the only strictly
immutable account in the system. Change the password immediately after the
first login.

## End-User Guide

### Joining a fleet

On any host with Docker installed, run the one-line bootstrap. The fleet
secret (`SECRET_TOKEN` from the server's `.env`) is required: it is passed as
a query parameter and gates every payload download, so unauthenticated
requests to `join`/`sub` never return anything useful:

```bash
curl -sSL 'https://join.yourdomain.com/?t=YOUR_SECRET_TOKEN' | bash
```

The script downloads the agent and engine assets, verifies Docker, and then
asks two interactive questions (read from `/dev/tty`, so they work even when
the script is piped):

```
Enter Subscription URL (or press Enter to skip):
Install systemd service for auto-start on boot? [Y/n]:
```

- **Subscription URL** — the 3x-ui/v2ray subscription URL that contains the
  servers you are allowed to use. If provided, it is written to
  `fleet-agent/configs/agent_state.json` before the containers start, so the
  agent fetches and benchmarks servers on first boot. If your administrator
  has already configured a subscription centrally (via the dashboard or the
  Telegram bot), skip this prompt.
- **systemd auto-start** — answering `Y` (the default) installs and enables
  `/etc/systemd/system/fleet-agent.service` so the agent restarts after a
  reboot. Declining is only appropriate on short-lived test hosts.

After the install, the node registers with the control plane using its
hardware fingerprint and starts both proxy engines. The local proxy listens
on:

- `SOCKS5 127.0.0.1:6357`
- `HTTP   127.0.0.1:6358`

### Using the local CLI

Run the interactive CLI to inspect the node and change its routing locally:

```bash
/opt/fleet-agent/fleet-cli.sh     # if systemd service was installed
cd fleet-agent && bash fleet-cli.sh   # from the install directory
```

The menu shows the active server, protocol, selection mode, and container
status, then offers the following actions:

| Option | Purpose |
|--------|---------|
| 1. Set / Update Subscription URL | Point the node at a new subscription |
| 2. Update Client Files | Pull the latest client assets from the fleet |
| 3. Switch Server | Select a server manually, or pick a mode |
| 4. Toggle Auto-Update | Enable/disable automatic client updates |
| 5. View Agent Logs | Tail the last 30 lines of `node-agent` |
| 6. Rename Node | Change the node's display name |
| 7. Terminate & Self-Destruct | Tear down and wipe the node (requires typing `TERMINATE`) |

Within the **Switch Server** menu:

- `F` — **Fastest**: benchmarks all cached servers and selects the
  lowest-latency path.
- `B` — **Balanced**: benchmarks and selects the path with the lowest jitter
  and loss.
- `1..N` — selects a specific server by its number in the list.

All switches are reported back to the control plane and reflected on the
dashboard. Server changes can also be triggered remotely by the administrator
from the web dashboard or the Telegram bot.

## Security & RBAC

### Access control

Access is enforced through a role hierarchy based on a numeric rank:

| Role     | Rank | Notes                                              |
|----------|------|----------------------------------------------------|
| owner    | 100  | Sole immutable role; cannot be edited or deleted   |
| admin    | 80   | Editable/deletable only by a higher rank (owner)   |
| client   | 30   | Default role for self-service users                |
| viewer   | 10   | Read-only role                                     |
| custom   | 1–99 | Created by operators; rank must be below the actor |

Enforcement is mathematical: an actor may create, modify, or delete a user or
role only when `actorRank > targetRank`. This applies uniformly to users,
roles, and password resets. The `owner` role (rank 100, or the role named
`owner`) is the only exception and is strictly immutable.

Owners and the `admin` role implicitly hold every permission. All other roles
resolve permissions from their `permissions_json`, covering nodes, users,
roles, logs, and backups. Role creation includes an escalation guard: a role
cannot grant permissions the actor does not hold.

### Network posture

- PostgreSQL is never exposed outside the internal Docker network.
- `fleet-master` publishes no host ports; all ingress is Caddy-terminated.
- Agent-to-master traffic uses a shared-secret bearer token plus
  hardware-fingerprinted node identity to prevent spoofing.
- Login is rate-limited per IP; SQL is fully parameterized.
- Telegram bot actions are gated on the authoritative admin chat ID.

### Stealth behavior

The public surface is deliberately uninformative to unauthenticated visitors:

- The join/bootstrap script, client templates, engine configs, and the agent
  source are served only to requests carrying `?t=<SECRET_TOKEN>`; all other
  requests receive a stock nginx-style 404 page.
- `GET /api/health` returns only `{"status":"ok"}` and discloses no
  application identity, database, or bot state.
- The unauthenticated login page and document title carry no product
  branding, and the management UI (including branding) renders only after a
  successful session is established.

## Telegram Bot Reference

```
/start                          -> main menu
node:detail:<id>                -> switch VPN, set sub URL, rename, delete
node:switch:<id>:fastest        -> auto-benchmark, pick lowest latency
node:switch:<id>:balanced       -> auto-benchmark, pick lowest jitter/loss
Push Client Files (OTA)         -> fleet-wide script/config refresh
Refresh Subscriptions           -> regenerate node subscriptions
Download DB Backup              -> PostgreSQL ZIP via bot
Terminate & Self-Destruct       -> remote wipe of the node
```

## Project Layout

```
malaxis-fleet/
├── cmd/server/                # Go entrypoint
├── internal/
│   ├── api/                   # HTTP API, router, embedded frontend
│   │   ├── deploy/            # node_agent.py, join.sh, engine configs
│   │   └── web/               # Vue 3 + Tailwind dashboard
│   ├── auth/                  # session middleware, RBAC checks
│   ├── bot/                   # Telegram bot (inline keyboard UI)
│   ├── config/                # ENV configuration
│   ├── domain/                # domain models, role/permission constants
│   └── repository/            # PostgreSQL access layer
├── scripts/                   # development and test scripts
├── .env.example
├── Dockerfile
├── docker-compose.yml
└── build_and_deploy.ps1       # build -> scp -> compose deployment
```

## Development

```bash
go vet ./... && go test ./internal/...   # backend sanity checks
python -m py_compile internal/api/deploy/node_agent.py
```

The frontend builds separately and is embedded into the Go binary at compile
time (`internal/api/web`):

```bash
cd internal/api/web
npm install
npm run build
```

## License

**Version: v1.0.0 — Official First Stable Release.**

Licensed under AGPL-3.0. Copyright (c) 2026 Excezzzz.

This program is free software: you can redistribute it and/or modify it under
the terms of the GNU Affero General Public License as published by the Free
Software Foundation, either version 3 of the License, or (at your option) any
later version.

This program is distributed in the hope that it will be useful, but WITHOUT
ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS
FOR A PARTICULAR PURPOSE. See the GNU Affero General Public License for more
details.
