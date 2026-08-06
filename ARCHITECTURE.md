# Malaxis Fleet Manager v2.1-BETA — Master Project Context & Architecture

> **The brain of this repository.** Every subsystem, port, permission, state file and
> protocol in this document is derived from the actual source code (not from
> assumptions). Read this file before touching any component.

| | |
|---|---|
| **Product** | Malaxis Fleet Manager — self-hosted Enterprise VPN Fleet Console ("Anti-DPI VPN Fleet Manager") |
| **Version** | v2.1-BETA (release commits `4ee88c0`, `bec1d2b`) |
| **Go module** | `malaxis-fleet`, `go 1.26.5` |
| **Frontend** | Vue 3 SPA (Vue CLI 5), Tailwind CSS (JIT), `lucide-vue-next`, `axios`. No Vue Router — view switching via a `currentView` ref. |
| **Node agent** | Python 3.11 (embedded in binary + served over the wire), Docker-out-of-Docker (DooD) |
| **Database** | PostgreSQL 16 (alpine), internal Docker network only |
| **Proxy** | Caddy (external, reverse proxy host header) |

---

## Table of Contents

1. [System Architecture & Topology](#1-system-architecture--topology)
2. [DPI Evasion & Network Hardening](#2-dpi-evasion--network-hardening)
3. [Smart Modes & Agent State](#3-smart-modes--agent-state)
4. [Over-the-Air (OTA) Updates & Command Queue](#4-over-the-air-ota-updates--command-queue)
5. [Security & RBAC](#5-security--rbac)
6. [Web UI (Vue 3) & Telegram Bot](#6-web-ui-vue-3--telegram-bot)
7. [Source Tree / File Map](#7-source-tree--file-map)
8. [Networking / Ports / Domains Reference](#8-networking--ports--domains-reference)
9. [Known Contradictions & Operational Notes](#9-known-contradictions--operational-notes)

---

## 1. System Architecture & Topology

### 1.1 High-Level Topology

```
                        INTERNET
                           │
                           ▼
                     ┌─────────────┐
                     │    Caddy    │  TLS :443 (external, not part of this repo)
                     │ reverse-proxy│  Host-header routing:
                     └──────┬──────┘
        dash.yourdomain.com │ api.yourdomain.com │ join / sub .yourdomain.com
                            │
   ┌────────────────────────┴───────────────────────────────┐
   │                  fleet-master  (Go, :8000)             │
   │                                                         │
   │   • embedded Vue 3 dist  (go:embed web/dist)            │
   │   • embedded deploy payloads (go:embed deploy)          │
   │   • REST API (agent poll/report, web UI, owner/admin)   │
   │   • Telegram Bot (tgbotapi) + AutoSync + Backup engine  │
   │   • Postgres, Tasks/Command queue, OTA file store, OSS  │
   └───────────────┬─────────────────────────────────────────┘
                   │  shared "caddy" docker network — NO host ports published
                   │
             ┌─────┴──────────────────────┐
             │ fleet-postgres  (pgx/v5)    │  fleet-postgres:5432, internal only
             │ backend connects over the   │
             │ internal docker network     │
             └─────────────────────────────┘

   ──  Any number of remote client hosts ("nodes")  ─────────────────────────

   curl -sSL https://join.<d>.com/join.sh | bash
   ┌─────────────────────────────────────────────────────────────┐
   │ /opt/fleet-agent (or ./fleet-agent)                          │
   │  docker-compose.yml  →  builds "node-agent" image            │
   │              │ Docker-out-of-Docker (DooD): the agent        │
   │              │ mounts /var/run/docker.sock and drives        │
   │              │ its own engine containers.                    │
   │              ├── node-agent     (Python; polls master)       │
   │              ├── xray-node      (Xray 1.8.x; real VPN)       │
   │              └── singbox-node   (sing-box; net_mode=container:xray-node)
   │  SOCKS5 127.0.0.1:6357 │ HTTP 127.0.0.1:6358 wiring           │
   └─────────────────────────────────────────────────────────────┘
```

### 1.2 Go Backend (fleet-master)

- Entry point: `cmd/server/main.go`.
- Startup sequence:
  1. `config.LoadConfig()` from `.env` (`github.com/joho/godotenv`).
  2. Auto-generates `SECRET_TOKEN` / `SESSION_SECRET` (crypto/rand, 64-byte hex) if missing; persists them into the `settings` table and re-reads them on next boot so secrets survive restarts.
  3. `auth.InitStore(cfg)` — gorilla/session cookie store (`fleet-session`, 7-day `MaxAge`, `HttpOnly`, `Secure` when not `localhost`, `SameSite=Lax`).
  4. `repository.NewRepository(cfg)` → `repo.Init()` — creates/ migrates the Postgres schema (see §1.5).
  5. `createInitialAdmin()` — **unconditional upsert** of the `admin` user; the bcrypt hash is force-synced from `ADMIN_USER`/`ADMIN_PASS` (defaults `admin`/`admin`) on *every* startup, with a comment explaining the naive "IF NOT EXISTS" seed caused stale-hash 401s.
  6. `seedDefaultRoles()` if roles table empty — seeds `owner`, `admin`, `client`, `viewer`.
  7. `server.NewServer(cfg, repo)` → `Start()`.
- **Background goroutines:**
  - `srv.Start()` (HTTP listener on `WEB_PORT=8000`).
  - Stale-node auto-cleanup: every 1 hour, deletes nodes offline **> 3 days** (`repo.DeleteOfflineNodes(3)`), including self-destructed "Terminated" nodes.
- `internal/server/server.go` start order:
  1. `setupMasterLogFile(cfg.MasterLogFile)` — default `data/logs/master.log`; adds an `io.MultiWriter(os.Stderr, file)` to the global logger (this is what the "Logs & Audit" master tab reads).
  2. `autoSyncService.Start()` — subscription auto-sync ticker (see §4.4).
  3. `bot.Start()` in a goroutine.
  4. Router middleware: `api.PanicRecoveryMiddleware(s.bot)` — the single global middleware; any Go panic is logged, pinged to the admin via Telegram (`SendAdminMessage`), and returned as HTTP 500.
  5. `http.ListenAndServe(":8000")`.

### 1.3 Docker Compose (fleet side)

`docker-compose.yml` (`version: '3.8'`):
- **`fleet-master`** — build `.` (Dockerfile: `alpine:latest` + `ca-certificates tzdata postgresql-client docker-cli`; binary copied as `master_server`; `EXPOSE 8000`). **No host ports published** — reached only via Caddy over the shared external `caddy` network. Volumes: `./data:/app/data`, `./backups:/app/backups`, and critically `/var/run/docker.sock` (used by master-side `docker logs` for the Logs & Audit tab; also present for parity).
- **`postgres`** — `postgres:16-alpine`, **not published**, internal network only. Credentials from `.env` (`POSTGRES_USER=${POSTGRES_USER:-fleet_internal}`, `POSTGRES_PASSWORD=${POSTGRES_PASSWORD:-changeme}`, `POSTGRES_DB=${POSTGRES_DB:-fleet_db}`). Data volume `./data/postgres`.
- Network `caddy` is `external: true` and must pre-exist.
- Both services set Docker json-file logging capped at 5MB x 2 files.

### 1.4 Domain Routing & .env Abstraction

All domain routing is **Host-header based** (`gorilla/mux` `router.Host(...)`):

| Domain | Role |
|---|---|
| `DASHBOARD_DOMAIN` (dash.*) | Serves the embedded Vue dashboard + `/api/web/*` + `/api/auth/*` + `/admin` + `/owner` |
| `API_DOMAIN` (api.*) | Agent-facing: `/api/poll`, `/api/report`, `/api/nodes/rename`, `/api/agent/latest`, `/api/health`, `/api/subscription/validate`, `/api/client/*` |
| `JOIN_DOMAIN` (join.*) | Bootstrap payloads: `/` (join.sh), `/fleet-cli`, `/fleet-agent.service` |
| `SUB_DOMAIN` (sub.*) | Client/O.S. file delivery: `docker-compose.yml`, `Dockerfile.client`, `requirements.txt`, `entrypoint.sh`, `node_agent.py`, `fleet-cli.sh`, `configs/xray_config.json`, `configs/singbox_config.json` |

- The server embeds `internal/api/deploy` and `internal/api/web/dist` with `go:embed`.
- **Domain placeholders**: deployed templates use `__API_DOMAIN__`, `__SUB_DOMAIN__`, `__JOIN_DOMAIN__`, `__DASH_DOMAIN__`; `applyDomainPlaceholders()` rewrites them to the configured `.env` values at serve time, so operators NEVER touch the templates.
- `serveDockerCompose` additionally interpolates the live fleet `SECRET_TOKEN` into `${FLEET_SECRET:-changeme_fleet_secret}` inside the served client `docker-compose.yml`.
- SSL/TLS cert paths are env-configurable (`SSL_CERT_PATH`/`SSL_KEY_PATH`, default `/cf-certs/cf.crt|.key`) — the Go server itself is plain HTTP `:8000`; TLS is terminated by Caddy in front.

### 1.5 PostgreSQL Schema (auto-migrated in `repository/postgres.go:Init`)

```
users       (id SERIAL PK, username VARCHAR(255) UNIQUE, password_hash TEXT,
             role VARCHAR(50), created_at TIMESTAMP, color_hex VARCHAR(7) DEFAULT '')
nodes       (id TEXT PK, name, hostname, device_type TEXT DEFAULT 'node', ip_lan TEXT,
             sub_url TEXT, active_server TEXT, active_engine TEXT, active_proto TEXT,
             active_ip_ext TEXT, active_outbound_json TEXT,
             available_servers TEXT NOT NULL DEFAULT '[]',
             last_seen TIMESTAMP,
             pending_command TEXT,          -- offline command queue (§4)
             pending_msg_id BIGINT,
             pipeline_status TEXT, status_message TEXT,
             user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
             hardware_hash TEXT,            -- fingerprint dedup (§3.3)
             node_logs TEXT)                -- JSON map container->log tail (§6.4)
roles       (id SERIAL PK, name VARCHAR(255) UNIQUE, color_hex VARCHAR(7) DEFAULT '#6B7280',
             owner_id TEXT, permissions_json TEXT DEFAULT '{}', rank INT DEFAULT 10,
             created_at TIMESTAMPTZ DEFAULT NOW())
audit_logs  (id SERIAL PK, timestamp TIMESTAMP, actor_username, action, target_device, details)
settings    (key TEXT PK, value TEXT, updated_at TIMESTAMP)
```

- Idempotent migrations run on every boot: `hardware_hash` + `idx_nodes_hardware_hash`, `node_logs`, `user_id`, backfills ranks for system roles (`owner=100, admin=80, client=30, viewer=10`), force-inserts missing system roles, migrates old `bot_*` settings keys to `tg_*`.

---

## 2. DPI Evasion & Network Hardening

The entire proxy-plane runs on the **client node box**, in three containers:
- `xray-node` (`ghcr.io/xtls/xray-core:latest`) — the workhorse.
- `singbox-node` (`ghcr.io/sagernet/sing-box:latest`) — shares the **xray-node network namespace** (`network_mode: "container:xray-node"`); only ever enabled as an engine when sing-box-native protocols (Hysteria2/TUIC/WireGuard) are used.
- `node-agent` (`python:3.11-alpine`, built from the served `Dockerfile.client`) — the Python control loop.

Important detail on singbox: because it shares xray-node's netns, `test_proxy()` probes `socket.gethostbyname("xray-node")` on port **6357** (sing-box effectively replaces Xray "in place" on the same loopback ports).

### 2.1 Xray XHTTP — `xmux`, `xPaddingBytes`

When a `vless://…` link uses `type=xhttp`, the agent builds Xray with (in `_xray_outbound`, used by `build_xray_config`):

```json
"streamSettings": {
  "network": "xhttp",
  "xhttpSettings": {
    "mode": "auto",
    "path": "<path or />",
    "extra": {
      "mode": "auto",
      "xPaddingBytes": "100-1000",
      "xmux": { "maxConnections": 4, "hMaxRequestTimes": "800-900", "hMaxReusableSecs": "1000-2000" }
    }
  }
}
```

- **`xmux.maxConnections = 4`** — XHTTP muxing multiplexes multiple real TCP streams over up to 4 persistent HTTP/2 requests to the server, dramatically flattening traffic fingerprints vs. one-request-per-connection. Each request is kept alive/reused for a random `800–900 ms` window (`hMaxRequestTimes`) up to `1000–2000 s` of reuse (`hMaxReusableSecs`).
- **`xPaddingBytes: "100-1000"`** — random padding bytes in the range 100–1000 are appended to each chunk, making all tunneled connections look like mundane HTTP chunked payloads and defeating size-based DPI heuristics.
- The *single-outbound* path (`parse_url_to_outbound` used for Click-to-select) writes `xmux.maxConnections: 1`; the full-fleet multi-server config (`build_xray_config`) uses **4** — the documented/tuned production value.
- Fingerprint for Reality is normalized to `chrome` via `_normalize_fp()` (any of `randomized/ios/firefox/edge/safari/disabled/none` → `chrome`); its parent patch coerces scheme `network=http` → `tcp`.

### 2.2 SOCKS5 UDP, TCP buffers, DNS

**SOCKS5 inbound (port 6357):**
```json
"inbounds", port 6357, "protocol": "socks",
 "settings": {"auth": "noauth", "udp": true, "ip": "127.0.0.1"},
 "sniffing": {"enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": true},
 "sockopt": {"tcpNoDelay": true, "tcpKeepAliveInterval": 15, "tcpUserTimeout": 15000}
```
- **`ip: 127.0.0.1`** — the SOCKS inbound binds to loopback so UDP relaying is only reachable from local processes (whatever uses `socks5://127.0.0.1:6357`); it is never exposed to the LAN/Internet. Combined with `auth: noauth`, it is intentionally a trusted local proxy sink.
- **`udp: true`** — full UDP-associate enabled (needed for QUIC/voice/games).
- **TCP buffers / keepalive hardening:** `tcpNoDelay: true` (disable Nagle → low latency), `tcpKeepAliveInterval: 15` (detect dead peers fast), `tcpUserTimeout: 15000` (Linux `TCP_USER_TIMEOUT` — an unresponsive link is dropped in ~15 s rather than lingering; the repo comment: "dead links are detected and dropped in ~15 s").
- HTTP inbound on **port 6358** (`timeout: 0`, same sockopts) for plain-HTTP proxy clients.
- **DNS = DNS-over-HTTPS**, IPv4 strict:
  - Xray dns servers: `https://dns.google/dns-query` → `https://cloudflare-dns.com/dns-query` → fallback plain `8.8.8.8`, `1.1.1.1`, with `queryStrategy: "UseIPv4"`.
  - sing-box dns: DoH resolver `https://1.1.1.1/dns-query` with `detour: direct` + `strategy: ipv4_only`, a `block` server `rcode://success`, `final: resolver`, `independent_cache: true`.
  - Xray routing also pins `{port 53, udp}` to `direct` so host/device DNS cannot leak into the tunnel loop.

### 2.3 Telegram MTProto bypass routing

Both engines pin the entire Telegram IP estate directly to the proxy outbound **and pre-empt any sniffing fallback** (so Telegram traffic is never mis-sniffed into a slow path):

```
{"type":"field","ip":["91.108.0.0/16","149.154.160.0/20","185.76.151.0/24"], "outboundTag":"<proxy>"}
```

- `91.108.0.0/16` (Telegram DC ranges), `149.154.160.0/20` (Telegram DC2/DC3 group), `185.76.151.0/24` (Telegram messaging infra). These CIDRs are routed via the encrypted tunnel unchanged; comments in code: *"Telegram MTProto / QUIC — matched by IP, requests routed through the proxy WITHOUT going through slow sniffing fallbacks."*

### 2.4 Sniffing — routeOnly

- Xray socks: `destOverride:["http","tls","quic"]`, `routeOnly: true` — sniffed metadata is used **for routing decisions only**, the destination is never overwritten (respects the client's actual address).
- sing-box socks: `{"enabled":true, "override_destination":false, "route_only":true}`; HTTP inbound: `override_destination:true` (the classic HTTP-proxy behavior).

### 2.5 Sing-box First policy & IPv4-only strictness

- **`First` policy / engine-lock.** A node has a single active engine at a time (`agent_state.json` `active_engine`). When `singbox` is active, Xray is left running with a **dummy socks-only config on :9999** (`ensure_xray_running()`) purely to hold the shared netns and networking, while sing-box actually serves :6357/:6358. When a protocol the other engine can't handle is requested, the agent falls back engine-wise (see §3).
- **IPv4-only strictness:** every DNS entry is DoH with `strategy: ipv4_only`; the Xray config uses `"queryStrategy": "UseIPv4"`; sing-box outbounds set `"domain_strategy": "ipv4_only"` (hysteria2/tuic/ss/vless/vmess/trojan all get it). `auto_detect_interface` on sing-box route and `UseIPv4` DNS ensure no IPv6 attempts leak — IPv6 simply isn't used anywhere in the transit stack.
- A `xhttp` line for sing-box is refused: `log("[singbox] ... uses xhttp transport - sing-box cannot handle, xray fallback required")` → returns `None` → the engine falls back to Xray outbound.

---

## 3. Smart Modes & Agent State

### 3.1 Fastest vs. Balanced

Implemented in `node_agent.py` (`benchmark_servers`, `select_mode`, `_smart_switch`, `_reselect_after_update`).

- **Bench = TCP connect probes** to every cached server's `host:port` (via `socket.gethostbyname` + `connect` with `BENCH_TIMEOUT=1.5s`, `BENCH_PROBES=2`, 50 ms between probes). For UDP-only protocols (hysteria2/tuic/wg) a *TCP connect-refused still counts* — it proves the host is up and yields a valid RTT sample (comment: "For UDP-only protocols a refused/connected result still proves the host is up and yields a valid RTT sample.").
- Per-server metrics: `latency_ms` (avg RTT), `jitter_ms` (max−min), `loss_pct` (100 × (1 − success/probes)).
- **Fastest** → pick `min(latency_ms)`.
- **Balanced** → pick `min( (loss_pct, jitter_ms, latency_ms) )` — a **tuple**, i.e. primary loss, then jitter, then latency (a stable-but-slightly-slower server beats a fast-and-flappy one; jitter is the anti-freeze metric).
- Benchmark cached to `configs/benchmark_cache.json` with `BENCH_TTL=600`s (10 min) so re-iterations don't ping-spam. Fresh cached data is used when available.

### 3.2 agent_state.json — VPN persistence & auto-recovery

Single source of truth on-box: `/app/configs/agent_state.json`. Keys written: `sub_url`, `active_server`, `active_engine`, `active_proto`, `active_mode`, `active_url` (`node_name`, `auto_update`, `last_seen`).

- On `boot` (worker `typ == "boot"`, fired on agent start): `update_subscription()` → `_reselect_after_update()` → if nothing could be applied, **`restore_active_vpn()`**.
- `restore_active_vpn()`: reads `active_engine`/`active_server`/`active_url` from state, re-derives the outbound from the saved URL, `save_rollback(engine)`, `_apply_outbound_cfg(engine, ob)` — **so VPN persistence survives docker/systemd restarts** automatically.
- In `update_subscription()`, if the currently active server (by tag/name) no longer exists in the freshly parsed subscription, the state **falls back to the first available server** and it is applied (see §4.3 — that's the "fallback to first server" semantic).
- Robustness: configs are written **atomically** (temp file + `os.replace` for client files), config writes and container restarts are serialized with a flock `apply.lock` (`fcntl.flock`, `/app/configs/apply.lock`) shared with docker `exec` CLI invocations. `start` also saves a rollback before every apply; if the proxy is dead 3 s after apply, the last-good config is restored (`restore_rollback` + restart).

### 3.3 Hardware Fingerprint — SHA-256 dedup

`get_hardware_hash()` computes `sha256(hostname | primary_MAC | system_UUID)`:
- **hostname**: `platform.node()`.
- **MAC**: first real NIC address from `/sys/class/net/*/address` (Linux) or `getmac /FO CSV /NH` (Windows); `00:00:00:00:00:00` and `lo` skipped; falls back to `uuid.getnode()`.
- **UUID**: `/sys/class/dmi/id/product_uuid` (Linux) or `wmic csproduct get UUID` (Windows); falls back to `uuid.getnode()`.

Server usage (`registerOrUpdateNode` → `repo.UpsertNode`):
- If another node already has this `hardware_hash`, the **existing canonical node_id wins** and is returned; the agent *adopts* it (`_adopt_node_id` persists `node_id.txt`), so a reinstalled box keeps the same name/history row.
- `hardware_hash` is stored (`SetNodeHardwareHash`) and indexed (`idx_nodes_hardware_hash`).
- Node `id` is otherwise a uuid4 hex (12 chars) persisted in `configs/node_id.txt`.

---

## 4. Over-the-Air (OTA) Updates & Command Queue

### 4.1 The `pending_command` column & Poll cycle

The master stores exactly **ONE queued command per node** in `nodes.pending_command` (plus `pending_msg_id` for telegram). Flow:

1. **Queue (master side)**: agent-facing `SetPendingCommand(nodeID, cmdJSON, messageID)` sets `pending_command`, sets `pipeline_status='Queued'`. HTTP interface `SendCommandHandler` (permission `can_switch_vpn`) accepts either `{"command":"switch:zoom"}` or `{"action":"switch","outbound_tag":"zoom"}`; queue also comes from the Telegram bot, the web UI, the auto-sync service, and OTA handlers.
2. **Poll (agent side)**: every `POLL_INTERVAL=30`s the agent `POST /api/poll` with `{id, hostname, ip_lan, hardware_hash}` (+state `name`/`sub_url`). The server: `registerOrUpdateNode` → reads `GetPendingCommand` → if non-empty responds `{"command": "<json>"}` and sets `pipeline_status='Fetched'` — but does **NOT clear** `pending_command`; the command stays resilient if the agent dies mid-execution.
3. **Execute**: agent `execute_command()` — deduplicates identical command keys (`_last_command`), dispatches to the non-blocking action worker, and for `get_logs` reports immediately within the poll cycle.
4. **Done**: agent `report()` → server `ReportHandler` → `ClearPendingCommand` (pending_command → NULL, pipeline back to cleared) + persist status/message/hardware_hash/name/logs. The command acts like a leash — if the report fails (network drop), the same still-queued command is delivered again on the next poll.

This two-phase design (`Fetched ≠ Cleared`) is the resilience seam of the whole OTA system.

### 4.2 `update_client_files` (OTA)

- Master `UpdateClientFilesHandler` (web poll `POST /api/web/nodes/update-client-files`, and `mass-update-client` alias) builds:
  ```json
  {"action":"update_client_files",
   "agent_url":"https://<sub>/node_agent.py",
   "cli_url":"https://<sub>/fleet-cli.sh",
   "req_url":"https://<sub>/requirements.txt",
   "entrypoint_url":"https://<sub>/entrypoint.sh"}
  ```
  and queues it for one (`node_id`) or every node.
- Agent `update_client_files(urls)`: each file → `*.tmp` → **integrity check** → atomic `os.replace`:
  - `node_agent.py`: `py_compile.compile(tmp, doraise=True)` — a syntax error in the new agent must never brick a running node.
  - `fleet-cli.sh`: `bash -n` (if bash present).
  - failure → tmp removed, marked failed, live file untouched.
- On success the worker also refreshes the subscription (`update_subscription` + `_reselect_after_update`), reports `status="Updated"`, then **`_graceful_restart()`** — sends `SIGTERM` to itself; the docker/systemd `restart` policy brings it back up with the new code. `Exit code 0` signals the expected graceful shutdown.
- Separate worker job types handle `restart` (re-restarts `xray-node` + `singbox-node`) and `terminate` (self-destruct: wipes `configs/`, removes engine containers and its own `node-agent` container, then exits).

### 4.3 `update_sub`

- `update_subscription()` reads `state["sub_url"]`, `parse_subscription(sub_url)` (base64 or raw lines, parses vless/vmess/trojan/ss/hysteria2/tuic/wg via the proper helpers), separates xray-engine vs singbox-engine servers, keeps the active server when still present, else **falls back to the first server**, applies with engine selection and rollback, then `save_rollback` + `apply_configs`.
- Web: `PUT /api/web/nodes/{id}/sub` (single), mass `POST /api/web/nodes/mass-update-sub`, `POST /api/web/devices/mass-update-sub` (legacy), `POST /api/web/devices/mass-update-domain` (rewrites only the domain part of `sub_url` via `replaceDomain`).
- Telegram: `/task:refresh_subs` = batch `update_sub` for all nodes.
- On-box CLI (`fleet-cli.sh`): Option 1 calls `node_agent.fetch_subscription_now()` which only downloads + saves the cache (applies on server selection), Option 3's switch uses the cache.

### 4.4 Auto-sync service (master-side)

`internal/service/autosync.go` — every `autosync_interval_minutes` (default 60 min; DB-regulatable 1h/3h/6h/12h/24h/disabled via `SetSyncInterval`):
- fetch each node's `sub_url`, decode base64/plain, parse links into `domain.Outbound`.
- if the active server's outbound canonical JSON differs from `nodes.active_outbound_json`, queue a `{"action":"switch"}` command with `pending_msg_id=0`.

---

## 5. Security & RBAC

### 5.1 Role hierarchy

| Name | Rank | Notes |
|---|---|---|
| `owner` | **100** | Original admin account only; immutable; the ONLY role that may manage users/roles |
| `admin` | **80** | Implicitly all perms; may not touch owner/ranks ≥ self |
| `client` (custom default) | **30** | default fallback for any unknown role name |
| `viewer` | **10** | view-only |

- `domain.RoleRank(name)` fallback table; every role row also carries a configurable `rank` (authorization criteria: `roleRank()` prefers DB rank, falls back to the built-in table).
- **Rank rules (enforced at the API AND mirrored in the bot & Vue UI):**
  - An actor may only create/edit/delete users with rank **strictly lower** than their own.
  - A target role assigned may never have rank ≥ actor's rank.
  - The `owner` role can never be assigned, renamed, deleted, or re-ranked.
  - System roles (`owner_id='system'`) can never be renamed/deleted.
- **Self-demotion protection**: `PUT /api/web/users/{id}` rejects when `actorID == id` → "Cannot modify your own account through this endpoint"; `DELETE` similarly "Cannot delete your own account". Even an owner cannot change their own privileges there.
- **Privilege escalation guard** `rolePermissionsAllowed`: a role manager may only grant permissions they themselves hold (owner/admin bypass).

### 5.2 Exact granular permissions (17)

| Permission | Meaning |
|---|---|
| `can_view_nodes` | list nodes (nodes list visible) |
| `can_switch_vpn` | send `switch:*` / clear queued commands |
| `can_edit_sub` | update sub URLs (incl. mass/mass-domain update, delete node) |
| `can_rename_node` | rename URL |
| `can_terminate_node` | queue self-destruct |
| `can_update_client` | OTA client-files push |
| `can_purge_nodes` | purge offline nodes |
| `can_view_users` | list users |
| `can_create_users` | create users |
| `can_edit_users` | edit users |
| `can_delete_users` | delete users |
| `can_view_roles` | list roles |
| `can_manage_roles` | create/edit/delete roles |
| `can_view_node_logs` | node container logs |
| `can_view_master_logs` | master/postgres `docker logs` |
| `can_view_audit_logs` | audit trail |
| `can_export_backups` | download DB backup |

- Deprecated/compat: `can_manage_users` (coarse) is mapped via `PermissionsGrantedBy` to the four granular user perms.
- Owner + `admin` + the literal `admin` username bypass every check (they hold `domain.AllPermissions`).

### 5.3 Layered protection & 403s

1. **Auth**: `auth.Middleware` — gorilla session cookie, CORS headers (origin = `DashboardDomain`), puts `user_id` (& `role`) into request context.
2. **Role gate** (`auth.RequireRole`) — used on `/api/client` (client/admin/owner) and `/admin`/`/owner` subrouters.
3. **Permission gate** (`auth.RequirePermission(repo, "<perm>")`) — function-level middleware subtraction for every `/api/web/*` route.
4. **In-handler hard checks** (`enforcePermission`, `requireOwner`, `writeForbidden`) as defense-in-depth, plus per-actor rank comparisons in user/role handlers.
5. **Agent-authent HTTP**: `AgentTokenMiddleware` — HMAC-SHA256 signature over `str(timestamp) + body`, headers `X-Fleet-Timestamp`/`X-Fleet-Signature`, hex-encoded, `hmac.Equal` constant-time compare, ±60 s window. This protects `/api/poll`, `/api/report`, `/api/nodes/rename`.
6. **Login brute-force**: per-IP `rate.Limiter` (default 30/min) via `RateLimit` middleware on `/api/auth/login`; throttle returns `Retry-After: 60`.
7. **Panic recovery** (500 + Telegram alert); secrets are bcrypt-phashed; agent/master auth is via shared `SECRET_TOKEN`.

---

## 6. Web UI (Vue 3) & Telegram Bot

### 6.1 "Floating Island" Glassmorphism UI

Design language (concrete examples from `App.vue`/`Nodes.vue`/`NodeCard.vue`):

- **Background**: zinc-950 `#09090b`, everything else floats over it.
- **Island recipe**: `bg-{zinc-900/40..90} backdrop-blur-md/xl/2xl border border-white/5..10 rounded-2xl/3xl shadow-2xl`.
- **Nav capsules**: `bg-zinc-900/80 backdrop-blur-xl border border-white/10 rounded-full shadow-2xl shadow-black/40`. Desktop: top pill nav; Mobile: **floating bottom island** (`fixed bottom-4 left-4 right-4 z-50`, icons-only + active dot with glow `shadow-[0_0_8px_2px_rgba(129,140,248,0.6)]`).
- **Accent buttons** (translucent status colors): indigo primary (`bg-indigo-500/15 … border-indigo-500/30 text-indigo-100`), emerald success, red destructive, purple role/mgmt; geometry `px-4 py-2 rounded-xl`.
- **Modals**: `fixed inset-0 z-[999] bg-black/70 backdrop-blur-md` overlay + `bg-zinc-900/90 backdrop-blur-2xl border border-white/10 rounded-2xl shadow-2xl`.
- **Toasts**: `fixed bottom-20 md:bottom-6 right-4 md:right-6 z-50 rounded-xl backdrop-blur-md shadow-2xl`, auto-dismiss 4000 ms.
- **"`[Commands]`" bracketed language**: headings `[Nodes]`, `[Client Files]`, `[Settings]`, `[Logs & Audit]` — `<span class="font-mono text-indigo-400">[</span>Title<span…>]</span>`; buttons `[Manage Sub URL]`, `[Switch VPN]`, `[View Logs]`, `[Task Queue (N)]`, `[Update All Devices]`, `[Mass Update Domain]`, `[Refresh List]`, `[Saving...]`/`[Save Bot Settings]`, `[Delete Node]` (red). Nav labels `[Nodes]` etc. in mono on desktop.
- Everything is pure Tailwind class composition — no custom theme (clean `tailwind.config.js`), one custom utility: `.scrollbar-none`.

**State & permission-driven navigation** (`App.vue`): `authCtx` provided globally with `hasPermission` shortcuts; nav shows Server/Nodes (perm `can_view_nodes`), Client Files (`can_edit_sub || can_update_client`), Fleet Users (`can_view_users`), Roles & Permissions (`can_view_roles`), Logs & Audit (`can_view_audit_logs || can_view_master_logs`), Settings (owner only). Read-only banner when no manage perms.

### 6.2 Real-time log streaming & Web IDE

**Nodes list (live)**: `Nodes.vue` polls `GET /api/web/nodes` every **5000ms**; online threshold = `last_seen` within **90s**; filters: all/online/offline + search by name/hostname/ip_lan; global action bar: `[Refresh List]` (with spinning `[Refreshing…]`), `[Mass Update Sub]`, `[Mass Update Domain]`, `[Purge Offline]`.

**Per-node logs + task queue** (`NodeCard.vue`): containers `['node-agent','xray-node','singbox-node']` as segmented control; `GET /api/web/nodes/{id}/logs?container=<name>`; full-replace render in a `bg-black … font-mono text-xs` pane; auto-refresh toggle with interval options **3s / 5s / 10s / 30s** (default 3 s), manual `[Refresh]`, `[Copy]`, and "auto-scroll to bottom". Logging backend: on fetch, when no other command is queued the server enqueues `{"action":"get_logs", "container":…}`; agent runs `docker logs --tail 200 --timestamps`, strips ANSI (`_clean_log_text`), and `report(logs=json)`, persisted in `nodes.node_logs`.

**Master logs** (`AuditLogs.vue`): containers `['fleet-master','fleet-postgres']`, `GET /api/web/logs/master?container=<c>`, `h-[36rem]`, `[Refresh]` + `[Copy Logs]`; reads `data/logs/master.log` first, falls back to `docker logs`. Also an **Audit Trail** tab (`GET /api/web/audit`, table of Timestamp/Actor/Action/Target/Details) with CSV export `[Export Logs]` (`audit_logs_<date>.csv`). Note: there is **no** dedicated "node logs" tab — node logs live in the per-node modal.

**Web IDE / ClientFiles tab** (`ClientFiles.vue`): list pane + `<textarea .mono min-h-[70vh]` editor, dirty-flag/toggle `[Save File]`, full PUT `PUT /api/web/templates/{filename}`; `[Push Latest Client Files to Devices]` = `POST /api/web/nodes/update-client-files` (queues OTA for all). Backed by `GetTemplatesHandler`/`UpdateTemplateHandler` (with `isTemplate` allowlist).

### 6.3 Telegram Bot state machine

Engine: `github.com/go-telegram-bot-api/telegram-bot-api/v5` in `internal/bot/bot.go` (~2000 LoC), single admin chat. It maintains **ONE dynamic bot message** per chat (`mainMenuID`) that is repeatedly re-edited (`editMessage`), and deletes the admin's text messages right after processing (`deleteMessage`) — the chat stays clean.

**Poller resilience**: `fetchUpdates` long-polls (timeout 60 s) with explicit `offset`; on Telegram `409 Conflict` (another consumer/webhook) backs off 30 s instead of hammering; generic errors back off 5 s. `pollUpdates` is strict-security: **any update from a chat ≠ `tg_admin_chat_id` is dropped**. Enabling/disabling bot, token + chat id come from the DB (`tg_bot_enabled`/`tg_bot_token`/`tg_admin_chat_id`, legacy `bot_*` read as fallback); the owner can change them live from the dashboard Settings tab with an instant `Reboot()`.

**In-memory state machine (`userState`)** for free-form text steps: `rename_node`, `set_sub`/`set_sub_url`, `terminate_confirm`, `add_user_creds`, `add_user_role`, `add_role_name`, `add_role_rank`, `user_pw`, `role_rename`, `role_rank`. Prompt → capture → process → re-render (text messages are deleted). `[❌ Cancel]` clears the state.

**Handle tree** (all via concise `data` callbacks, full CRUD of nodes, users and roles in-chat):
- Nodes: `nodes:list` → per-node `node:detail` → `node:vpn` (Fastest / Balanced per-server), `node:sub` (URL), `node:logs` (`node:logs_show`/`node:logs_refresh`), `node:queue` (+`node:queue_cancel`), `node:rename`, `node:delete` (→`node:softdelete` | `node:terminate`).
- Fleet-level: `ota:all = all OTA`, `task:refresh_subs = all update_sub`, `purge:go`, `backup:download` (pg_dump+gzip doc), daily 24h auto-backup `sendBackupDocument`.
- Users (`handleUsersMenu`): `user:detail` → `user:role` role-picker (owner excluded), `user:setrole:<uid>:<rid>`, `user:pw` (next text = new password), `user:del` → `user:delconfirm`; add user via `users:add` (format `"username password"`, then a role-picker step).
- Roles (`handleRolesMenu`): `role:detail` (rank + assigned-user count) → `role:rename`, `role:rank` (1–99), `role:del` → `role:delconfirm`; `roles:add` with name + rank; reserved names (`owner/admin/client/viewer`) and system roles are protected server & client side.
- Online/offline counters in the main menu use the same `onlineWindow = 90 * time.Second` as the web.

**New-node onboarding notification** (`NotifyNewNode` fired from `register_node` when `isNew`): text "🖥️ NEW DEVICE CONNECTED!" + buttons `[🔗 Set Sub URL]` / `[⚖️ Set Balanced]` / `[❌ Reject & Delete]`, enabling instant onboarding straight from chat.

---

## 7. Source Tree / File Map

```
cmd/server/main.go                     bootstrapping, admin seed, role farm, cleanup ticker
internal/domain/models.go              User/Node/CustomRole/AuditLog/Outbound/Command + Perm/Role consts
internal/config/config.go              .env → Config (subdomains, secrets, limits)
internal/server/server.go              HTTP assembly, master log, autosync+bot goroutines
internal/api/router.go                 mux wiring, subdomain routers, RateLimit, embed servers
internal/api/middleware.go             PanicRecoveryMiddleware
internal/api/handlers.go               ~2550 LOC — all REST handlers, agent auth, RBAC
internal/api/deploy/…                  served client payloads:
  node_agent.py (2385 LOC)             the Python agent: poll/report, sub parse, config build, benchmark, OTA, worker
  fleet-cli.sh                        interactive CLI (8 options)
  join.sh                             onboarding
  client-docker-compose.yml           node engine compose (xray-node :6357/6358/udp, singbox netns)
  Dockerfile.client / requirements.txt / entrypoint.sh
  configs/xray_config.json, singbox_config.json
  fleet-agent.service                 systemd unit
internal/domain/models.go                              domain types
internal/repository/repository.go      Repository interface (full method list)
internal/repository/postgres.go        pgx/v5 impl, schema + migrations + node upsert/fingerprint
internal/audit/audit.go               audit actions + IP capture
internal/backup/backup.go             pg_dump→zip / gzip, retention 3
internal/service/autosync.go          periodic subscription sync
internal/service/utils.go             misc helpers
internal/bot/bot.go                    Telegram state machine (~2000 LoC)
internal/api/web/…                    Vue3 SPA (src/components: App.vue, Nodes, NodeCard, ClientFiles, AdminUsers, RoleManager, AuditLogs, Settings, Login)
scripts/test_api.sh                   smoke-test matrix (14 endpoints)
build_and_deploy.ps1                  Vue build → Go cross-compile → scp/ssh → compose up
```

---

## 8. Networking / Ports / Domains Reference

| Item | Value | Where |
|---|---|---|
| HTTP listen | `:8000` (`WEB_PORT`) | config.go / server.go |
| Dashboard API base | `https://dash.<d>:/api/web/*` | router.go |
| Locked HTTPS | external Caddy (443) → :8000 | — |
| Agent SOCKS5 | `127.0.0.1:6357` (TCP+UDP) | xray/singbox inbound |
| Agent HTTP proxy | `127.0.0.1:6358` | same |
| Xray dummy port (singbox netns) | `127.0.0.1:9999` | agent `ensure_xray_running()` |
| DB | `fleet-postgres:5432`, `fleet_db` | compose |
| Agent poll | every `POLL_INTERVAL` (30 s) | node_agent.py |
| Agent health report | every `HEALTH_INTERVAL` (60 s) | " |
| Dashboard refresh | 5 s | Nodes.vue |
| Online window | 90 s | both web + bot `onlineWindow` |
| Log refresh | 3 s default | NodeCard.vue |
| Agent benchmark TTL | 600 s | `BENCH_TTL` |
| Login rate limit | 30/min/IP | RateLimit |
| Backup retention | `MAX_BACKUP_RETENTION=3` | backup.go |
| Purging offline | >3 d (auto), >7 d (web UI button) | main.go/backup.go + Nodes |

**Domains**: `DASHBOARD_DOMAIN` (dash), `API_DOMAIN` (api), `JOIN_DOMAIN` (join), `SUB_DOMAIN` (sub) + the equivalent `*_URL` https:// normalized.

---

## 9. Known Contradictions & Operational Notes

1. **Heartbeat interval**: README/poll docs variously say "5s heartbeat" (PROJECT_CONTEXT) vs "reports health every 60 s" (README) — the actual code polls every **30 s**, health-reports every **60 s**, and the dashboard re-polls the node list every **5 s**.
2. **Node purge threshold**: server auto-cleanup = 3 days; the web UI button says >7 days; the Telegram bot uses 3 days.
3. **xmux maxConnections**: full-fleet multi-server config uses **4**; the manual single-outbound path uses **1** (that is the documented fleet value of 4).
4. `.env` has exactly the variables in `.env.example` + the domain/ports; `POSTGRES_PASSWORD` default is `changeme` — change it in production.
5. This documentation is only correct for v2.1-BETA. The repo also builds a `server.exe` (Windows local binary) — production target is the linux `go build … master_server` from `build_and_deploy.ps1`.

---

*Generated from the v2.1-BETA source. Last updated: 2026-08-07.*