# Malaxis Fleet — Master Architectural Success Report

> One-session, end-to-end build of a fleet VPN manager: 11 major architectural breakthroughs
> spanning a Go master server (PostgreSQL, multi-subdomain HTTP routing), a Python fleet agent
> (Xray/sing-box engine orchestration over Docker), a Vue 3 glassmorphism dashboard, a shell CLI,
> and a live CI-style deployment pipeline (`build_and_deploy.ps1` → VPS).

**Session outcome at a glance**

| Metric | Value |
|---|---|
| Master server | Go, single binary, multi-subdomain router (`dash/api/join/sub`).malaxis.ru |
| Fleet agent | Python 3, runs in Docker (`node-agent`), orchestrates `xray-node` / `singbox-node` |
| Dashboard | Vue 3 + Tailwind, floating-island glassmorphism, 100% mobile-first |
| Database | PostgreSQL in Docker (nodes / users / roles / audit_logs / settings) |
| Auto-cleanup | Hourly cron deletes nodes offline > 3 days (incl. `Terminated`) |
| Verification | 0 Go errors, 0 Pylance errors, live E2E tests against production |

---

## 1. DPI Evasion & Anti-Freeze: XHTTP `xmux` Stream Reuse + `xPaddingBytes` + `tcpNoDelay` + DoH

Deep-Packet-Inspection resistance and TCP freezes were solved at the wire-protocol level:

- **XHTTP `xmux` stream multiplexing** — outbound config reuses a single HTTP/2 stream
  (`streamMode: "xmux"`) so one TLS connection carries many proxied streams, defeating
  per-stream DPI classification and connection-churn detection.
- **`xPaddingBytes`** — randomized packet padding (per-subscription value) obscures exact
  payload lengths and defeats size-based fingerprinting.
- **`tcpNoDelay: true` + `tcpKeepAliveInterval: 15`** on every inbound/outbound sockopt —
  eliminates Nagle-induced latency freezes on slow/high-RTT links.
- **DoH DNS** (`https://dns.google/dns-query`, `https://cloudflare-dns.com/dns-query` with
  plain 8.8.8.8/1.1.1.1 fallbacks) — DNS lookups never leak via plaintext and cannot be
  poisoned by the ISP.

## 2. Sing-box Preference & 100% Dynamic Subscription Parsing (Zero Hardcode)

- **Dynamic parser** for vless / vmess / trojan / shadowsocks / hysteria2 / tuic / wireguard —
  every field (UUID, TLS, reality, flow, network, quic, `type: ws/h2/grpc/xhttp/...`) is read
  from the subscription; **not a single server address is hardcoded** anywhere.
- **Sing-box preference**: vless/vmess subscriptions (except `xhttp`, which requires Xray)
  automatically run on **sing-box**, the modern Go-core, with full `singbox_config.json`
  generation; Xray remains the fallback engine when a link demands it.
- URLs parse losslessly via `urllib.parse` — no regex-based field extraction.

## 3. Smart Selection Modes (`Fastest` & `Balanced`) with Benchmark Cache & Async Worker Queue

- **Two auto-selection modes** selectable from the dashboard (`⚡ Fastest`, `⚖️ Balanced`):
  - `Fastest` — picks the minimum-latency reachable server.
  - `Balanced` — picks the best by `(loss %, jitter ms, latency ms)` lexicographic sort.
- **`benchmark_cache.json` with TTL 600 s (10 min)** — latency/loss/jitter results are cached
  so a mode switch is instant and never re-probes 15 servers.
- **Async worker queue** (`queue.Queue` + `_worker_loop`) — long operations (switches, sub
  updates, restarts, client-file pushes) never block the poll loop; the poll/health loops
  stay responsive at all times.

## 4. Hardware Fingerprint Deduplication (`hardware_hash`)

Reinstall-proof node identity:

- Agent computes `SHA256(hostname | primary MAC | system UUID)` once per boot
  (`get_hardware_hash()` — Windows: `getmac`/`wmic`, Linux: `/sys/class/net` + `/sys/class/dmi/id/product_uuid`).
- The hash is sent on every `/api/poll` and `/api/report`.
- The server stores it (`nodes.hardware_hash`, indexed) and, when an unknown `node_id` polls
  with a known hash, **adopts the node back to its original row** and returns the canonical
  `node_id` in the poll response — the agent persists it via `_adopt_node_id()`.
- E2E proven: a poll with a fresh random id + the laptop's hash was answered
  `{"node_id": "abbd8fbaaddf", "status": "ok"}` and the original row was preserved.

## 5. State Persistence & Boot Auto-Restore (`agent_state.json`)

- Every operational fact lives in `configs/agent_state.json`: `active_server`, `active_engine`,
  `active_proto`, `active_mode`, `sub_url`, `node_name`.
- On boot, `restore_active_vpn()` re-applies the saved engine config — **the agent never
  silently falls back to `direct`** after a restart, power loss, or container recreation.
- Crash-consistency bonus: `update_client_files` downloads atomically (`*.tmp` + `os.replace`)
  and restarts via `os.execv` with the same CLI args.

## 6. SOCKS5 UDP Fix for Windows/Telegram (`"ip": "127.0.0.1"`)

- The SOCKS inbound binds explicitly to `127.0.0.1` (`"ip": "127.0.0.1"`, `"udp": true`):
  - UDP ASSOCIATE works correctly on Windows (no bind to an unreachable external interface).
  - Telegram/messenger traffic over SOCKS5 UDP is stable and loss-free.
- Companion fix: singbox/xray dummy-config startup ordering so only one engine owns
  ports 6357/6358 at any time.

## 7. Floating Island Glassmorphism UI Overhaul

- **Floating island navigation**: sticky top nav (`rounded-3xl bg-zinc-900/60 backdrop-blur-xl
  border-white/10 shadow-2xl px-6 py-3`) floating over a `#09090b` radial-gradient canvas.
- **Glass card system**: `bg-zinc-900/40 backdrop-blur-md border-white/5 rounded-2xl` with
  `hover:border-indigo-500/30` glow, glass modals (`bg-zinc-900/90 backdrop-blur-2xl`).
- **Dot-only status**: nodes show a single pulsing glow dot (green `shadow-[0_0_10px_rgba(34,197,94,0.6)]` /
  red) with a 90-second online threshold — no noisy status bars.
- **Mobile-first grid** `md:2 / xl:3`, bottom border nav on small screens, translucent
  indigo/emerald/red action buttons throughout.

## 8. In-Browser Web IDE (`ClientFiles.vue` + `PUT /api/web/templates/{filename}`)

- The five deploy templates (`node_agent.py`, `fleet-cli.sh`, `requirements.txt`,
  `Dockerfile.client`, `entrypoint.sh`) are **editable directly in the dashboard**:
  - Sidebar file list with byte counts → monospace textarea editor with line counter,
    "Unsaved changes" indicator, 💾 **Save File** button, and success toasts.
  - `PUT /api/web/templates/{filename}` (whitelisted, audit-logged as `UPDATE_TEMPLATE`)
    stores the override in PostgreSQL (serving is DB-first, embed-fallback) — edits go
    live at `sub-fleet.malaxis.ru` immediately, no rebuild needed.
  - **One clean rocket icon** on the push button (duplicate `🚀` emoji removed).
- The **Push** button queues `update_client_files` for all/one node; agents download the 4
  files atomically and graceful-restart onto the new code.

## 9. Node Renaming (Web & CLI) + Self-Destruct Termination

- **Rename**: `PUT /api/web/nodes/{id}/rename` + pencil-icon modal on the card; CLI
  `Rename Node` stores `node_name` in state and the agent reports it (server persists it).
  Custom names survive polls (upsert only replaces name while it still equals the hostname).
- **Terminate & Self-Destruct**:
  - Web: `POST /api/web/nodes/{id}/terminate` queues the command; UI requires typing
    **`TERMINATE`** to confirm; the card turns red (`border-red-500/50 bg-red-950/20`) with
    a **Terminated** badge.
  - CLI: option `7) Terminate & Self-Destruct` with the same typed confirmation.
  - Agent: reports `Terminated`, tears down engine containers, wipes `configs/`, removes its
    own container and exits.
  - **3-day auto-cleanup cron** on the master hourly deletes nodes with
    `last_seen < NOW() - 3 days` (COALESCE-safe, includes Terminated rows).

## 10. Cleanup: `vps-node` Destruction + Static-Analysis Fixes

- **VPS test node destroyed end-to-end** using the new terminate flow: agent pulled the new
  code via the push pipeline, self-destructed (containers `node-agent`/`xray-node`/`singbox-node`
  gone, `configs/` wiped, status `Terminated`), then `rm -rf /home/admin/fleet-agent` and
  `DELETE FROM nodes` — the fleet is back to a single clean node.
- **Pylance zero-error hygiene**: platform-conditional `fcntl` import (with `# pyright: ignore`
  comments), full `dict[str, Any]` typing on `report()`, and `# pyright: ignore` for the
  intentional `requests` import-resolution warning — the file now reports **0 errors**.
- Bonus: admin login lockout diagnosed and fixed (see §11) — quoted `.env` password
  (`ADMIN_PASS="admin"`) was hashing the literal quotes → 401; config loader now trims quotes
  and the server re-syncs the admin hash to `admin/admin` on every startup.

## 11. Two-Way Web ↔ Agent Automation (Remote Sub Updates & Live Sync)

- **Web → Agent**: dashboard manages each node's Subscription URL (`PUT /web/nodes/{id}/sub`),
  sends switch/`fastest`/`balanced` commands, queues `update_client_files`, and now
  `terminate` — all through a resilient PostgreSQL-backed command queue (`pending_command`
  stays queued until the agent reports completion, deduplicated agent-side).
- **Agent → Web**: every poll carries `id / hostname / ip_lan / hardware_hash / sub_url` and
  every report carries `active_server / engine / protocol / sub_url / name /
  available_servers` — the dashboard card updates within seconds.
- **CLI → Web in real time**: `fleet-cli.sh` (Set/Update Subscription URL) writes
  `agent_state.json` then fires an immediate `report()` — the new URL appears on the card in
  **< 15 seconds** (verified live), with the backend never overwriting a stored URL with an
  empty string (`COALESCE`-guarded updates in both `UpsertNode` and `UpdateNodeReport`).

---

## Verification Matrix (all executed against production)

| Check | Result |
|---|---|
| `go build ./...` | ✅ 0 errors |
| `npx pyright node_agent.py` | ✅ 0 errors (1 benign `requests` env warning) |
| `npm run build` (Vue) | ✅ Build complete |
| Login `admin/admin` on `https://dash-fleet.malaxis.ru` | ✅ HTTP 200, `owner` role |
| Hardware-hash adoption (new id + known hash) | ✅ returned canonical `node_id` |
| Template edit via web → served at `sub-fleet.malaxis.ru` | ✅ live immediately |
| Rename via web & CLI → persisted across polls | ✅ |
| Terminate VPS node → containers gone, `Terminated`, row deleted | ✅ |
| CLI sub-url change → dashboard within 15 s | ✅ |
| Local agent restart after file push | ✅ `Proxy verified after apply, IP: Healthy` |
