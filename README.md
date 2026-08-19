# Malaxis Fleet Manager

[![Release](https://img.shields.io/badge/Release-v1.4.0-brightgreen)](https://github.com/Excezzzz/malaxis-fleet/releases)
[![License](https://img.shields.io/badge/License-AGPL--3.0-blue)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8)](https://go.dev)
[![Vue](https://img.shields.io/badge/Vue-3-42b883)](https://vuejs.org)

A self-hosted control plane for orchestrating VPN nodes across remote hosts to
bypass DPI and network censorship. A single Go master server manages an
arbitrary number of Dockerized Python agents, each running Xray-core and
sing-box behind one TLS-terminating Caddy instance.

The stack is tuned for networks under active deep packet inspection: XHTTP
stream multiplexing, route-only sniffing, padding, and DoH resolution are
configured by default.

---

## Quick Start

### Prerequisites

| Requirement | Notes |
|-------------|-------|
| Linux VPS (Debian/Ubuntu) | root access, one public IPv4 |
| Docker + Docker Compose | installed automatically by `install.sh` if absent |
| Domain with 4 A records | `dash`, `api`, `join`, `sub` pointing at the VPS |

### Server installation

```bash
curl -sSL https://raw.githubusercontent.com/Excezzzz/malaxis-fleet/main/install.sh | bash
```

The installer runs as root and offers **two install modes**:

| Mode | Who is it for | What it asks |
|------|---------------|--------------|
| **Simple** (default, recommended) | Regular users — no Docker or server knowledge needed | Only your main domain (e.g. `example.com`) and an admin password. Everything else — the `dash.`/`api.`/`join.`/`sub.` subdomains, all secrets, the Docker install and the pre-built image — is configured automatically. |
| **Advanced** | Users who know what Docker is and want full control | Install method (pre-built image vs build from source), four separate subdomains, admin password, Telegram bot token/chat ID, swap file management. |

Pick the mode at the first prompt (`1` = Simple, `2` = Advanced). The installer
creates a 2 GB swap file when memory is below 2 GB (advanced mode only),
installs Docker, clones the repository into `/opt/malaxis-fleet`, writes
`.env`, creates the shared `caddy` network, and launches the stack:

```bash
docker compose up -d --build
```

If npmjs.org is slow or unreachable on your network, point the build at a
registry mirror instead:

```bash
NPM_REGISTRY=https://registry.npmmirror.com sudo bash install.sh
```

Sign in at `https://dash.yourdomain.com` (default credentials: `owner` /
`owner`). The dashboard exposes the tokenized join commands for both
platforms; copy the one matching your client host.

### Client node installation

Any host with Docker installed. The fleet secret (`SECRET_TOKEN` from the
server `.env`) is passed as a query parameter and gates every payload
download.

```bash
# Linux / macOS / Git Bash
curl -sSL https://join.yourdomain.com/?t=SECRET_TOKEN | bash
```

```powershell
# Windows PowerShell
irm https://join.yourdomain.com/join.ps1?t=SECRET_TOKEN | iex
```

Both installers run pre-flight checks (Docker daemon, Compose v2 plugin,
master reachability), prompt for install location, device name, subscription
URL and routing mode, write `configs/agent_state.json`, and start the client
stack with Docker's `restart: unless-stopped` policy. The node self-registers
with a hardware-derived identity and appears in the dashboard and Telegram
bot immediately.

---

## Architecture Overview

```
                Clients / Telegram
                        |
                        v
                Caddy (TLS :443)
    dash.*  api.*  join.*  sub.*  ->  :8000
                        |
                        v
           fleet-master (Go backend, :8000)
    PostgreSQL | task queue | Telegram bot | OTA store
                        ^
                        | HTTPS (api / sub domains)
          +-------------+-------------+
          v                           v
  Fleet node (Python agent)    Bootstrap assets
  Xray-node :6357/:6358       (join.sh, join.ps1, compose)
  singbox-node (shared netns)
```

| Component | Stack |
|-----------|-------|
| Master server | Go 1.26 backend, embedded Vue 3 SPA, PostgreSQL 16, Caddy reverse proxy |
| Client node | Python 3.11 agent managing Xray-core and sing-box via Docker DooD (`/var/run/docker.sock`) |
| Control-plane network | no host ports published by the master; all ingress is Caddy-terminated |

Nodes poll the API with a shared-secret bearer token, execute queued
commands, and report health and benchmark results. OTA updates push modular
agent code (`agent_src.zip`) and client assets fleet-wide in one action.

---

## Features & Specifications

### Transports & protocols

| Transport | Support |
|-----------|---------|
| VLESS Reality | Xray, sing-box |
| VLESS XHTTP (`xmux` stream reuse) | Xray, sing-box |
| Hysteria2 | sing-box |
| TUIC | sing-box |
| Shadowsocks | Xray, sing-box |
| Trojan | Xray, sing-box |
| WireGuard | sing-box |

Both engines expose `SOCKS5 127.0.0.1:6357` and `HTTP 127.0.0.1:6358`. The
agent generates engine configs at runtime and falls back from sing-box to
Xray for transports sing-box cannot carry natively.

### Anti-DPI & performance

- DoH DNS (`1.1.1.1` / `dns.google`, `ipv4_only` strategy) prevents plaintext
  DNS leaks.
- `tcpNoDelay` and aggressive TCP keepalive drop stale connections quickly.
- Telegram MTProto and QUIC CIDRs (`91.108.0.0/16`, `149.154.160.0/20`,
  `185.76.151.0/24`) are routed first, before sniffing rules, so media flows
  bypass sniff-based fallbacks.
- Xray sniffing uses `routeOnly`: sniffed results drive routing only and never
  rewrite the destination.
- XHTTP `xmux` multiplexes a channel over parallel HTTP requests with adaptive
  request windows, avoiding single long-lived streams.
- sing-box is the default engine allocation; Xray is used where sing-box
  lacks native support.

### Smart routing

| Mode | Selection criteria |
|------|--------------------|
| Fastest | lowest RTT from live TCP/UDP probing |
| Balanced | lowest RTT / jitter / loss |

Switches require the candidate to outperform the active server by at least
25% (hysteresis protection). Benchmark results are cached (10-minute TTL)
to avoid probe spam.

### Control interfaces

- **Web dashboard** (Vue 3): glassmorphism UI, Web IDE for live config
  editing, container log streaming, dark/light themes, audit log.
- **Telegram bot**: single-message inline UI covering nodes, users, roles,
  subscription providers, and backups; onboarding alerts; on-demand Postgres
  backup delivery as a `.zip`.

### Security

- Role hierarchy by rank: Owner (100), Admin (80), Client (30), Viewer (10).
  Enforcement is mathematical: an actor may modify a target only when
  `actorRank > targetRank`. The owner role is immutable.
- 18 granular permission flags covering nodes, users, roles, logs, and audit.
  Role creation cannot grant permissions the actor does not hold.
- All installer payloads (`join.sh`, `join.ps1`, `node_agent.py`,
  `agent_src.zip`) are served only to requests carrying `?t=<SECRET_TOKEN>`;
  unauthenticated requests receive a generic 404.
- DB backup access is hardcoded to the owner role only.
- SQL is fully parameterized; login is rate-limited per IP; agent identity is
  hardware-fingerprinted (SHA-256 of hostname, primary MAC, system serial).

---

## Environment Variables Reference

All values are consumed at runtime by the control plane and substituted into
the client deployment assets served to nodes. `.env` is git-ignored; use
`.env.example` as a template.

| Variable | Default | Description |
|----------|---------|-------------|
| `DASHBOARD_DOMAIN` | `dash.yourdomain.com` | Dashboard host (Vue 3 SPA) |
| `API_DOMAIN` | `api.yourdomain.com` | Agent poll/report API host |
| `JOIN_DOMAIN` | `join.yourdomain.com` | Bootstrap installer host (`join.sh` / `join.ps1`) |
| `SUB_DOMAIN` | `sub.yourdomain.com` | Client file and OTA delivery host |
| `ADMIN_USER` | `owner` | Initial administrator account (rank 100) |
| `ADMIN_PASS` | `owner` | Initial administrator password (change on first login) |
| `SECRET_TOKEN` | *(generated)* | Shared agent-master auth and payload-delivery token |
| `SESSION_SECRET` | *(generated)* | Web session cookie signing secret |
| `BOT_TOKEN` | *(empty)* | Telegram bot token from @BotFather |
| `ADMIN_CHAT_ID` | *(empty)* | Telegram admin chat ID for bot notifications |
| `POSTGRES_USER` | `fleet_internal` | PostgreSQL role (internal network only) |
| `POSTGRES_PASSWORD` | `changeme` | PostgreSQL password (must be overridden) |
| `POSTGRES_DB` | `fleet_db` | PostgreSQL database name |
| `POSTGRES_HOST` / `POSTGRES_PORT` | `postgres` / `5432` | DB endpoint for local runs and diagnostics |
| `SERVER_PORT` | `8000` | Internal Go HTTP port (Caddy proxies to it) |
| `LOGIN_RATE_LIMIT` | `30` | Web login attempts allowed per minute per IP |
| `MAX_BACKUP_RETENTION` | `3` | Number of `.zip` backups kept in `/app/backups` |
| `SSL_CERT_PATH` | `/cf-certs/cf.crt` | TLS certificate path (Caddy) |
| `SSL_KEY_PATH` | `/cf-certs/cf.key` | TLS key path (Caddy) |
| `LOW_RAM_MODE` | `false` | Tune container limits for small VPS instances |

---

## Development

```bash
go vet ./... && go test ./internal/...   # backend checks
cd internal/api/web && npm install && npm run build   # dashboard build
python -m py_compile internal/api/deploy/agent_src/*.py   # agent checks
```

The dashboard build output is embedded into the Go binary at compile time
(`//go:embed web/dist`), so rebuild it before deploying. Deployment from a
Windows workstation (equivalent of the Linux installer):

```powershell
.\build_and_deploy.ps1 admin@your-server-ip
```

It sanity-builds the dashboard locally, ships the source tree over SSH to
`~/malaxis-fleet`, uploads `.env`, rebuilds the image in Docker on the server
and restarts the stack. Set `NPM_REGISTRY` in your shell first when npmjs.org
is slow or blocked on your network (the value is forwarded to the remote
build): `$env:NPM_REGISTRY = "https://registry.npmmirror.com"`.

---

## License

GNU Affero General Public License v3.0. See [LICENSE](LICENSE).

Copyright (c) 2026 Excezzzz.