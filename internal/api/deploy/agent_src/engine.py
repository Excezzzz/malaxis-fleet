#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Engine: subscription fetching, config application with rollback, server
selection (manual / fastest / balanced), benchmarking and proxy health."""
import base64
import errno
import json
import os
import socket
import threading
import time
import traceback
from typing import Callable, Optional, Tuple

from agent_src import agent, config_builder, docker_utils, network, subscriptions

try:
    import fcntl  # type: ignore[import-not-found]
except ImportError:  # Windows: module does not exist
    fcntl = None  # type: ignore[assignment]


def ensure_default_configs():
    os.makedirs(agent.CONFIG_DIR, exist_ok=True)
    for path, default in [(agent.XRAY_CONFIG, config_builder.DEFAULT_XRAY_CONFIG), (agent.SINGBOX_CONFIG, config_builder.DEFAULT_SINGBOX_CONFIG)]:
        if not os.path.exists(path) or os.path.getsize(path) == 0:
            with open(path, "w") as f:
                json.dump(default, f, indent=2)
            agent.log(f"Created default {os.path.basename(path)}")
    state = agent.load_state()
    engine = state.get("active_engine", "xray")
    if engine == "singbox":
        agent.log("Starting singbox-node (xray dummy config for network)...")
        docker_utils._ensure_xray_running()
        os.system("docker start singbox-node 2>/dev/null || docker restart singbox-node 2>/dev/null || true")
    else:
        agent.log("Stopping singbox-node to free ports 6357/6358...")
        os.system("docker stop singbox-node 2>/dev/null")
        os.system("docker start xray-node 2>/dev/null || true")


# --- Apply lock: serialize config writes + container restarts between the
# --- daemon process and CLI invocations (docker exec) running in parallel.

_APPLY_LOCK: Optional[object] = None

# In-process threading lock. The file-based apply lock (fcntl.flock) only
# serializes against EXTERNAL processes — flock is per-fd, so it cannot stop
# the health_loop thread from racing a worker switch inside this same process.
# Every engine mutation must hold this lock; health_loop try-acquires it and
# skips its cycle when a switch is in progress.
_engine_lock = threading.Lock()


def acquire_apply_lock() -> bool:
    global _APPLY_LOCK
    if fcntl is None:  # type: ignore[union-attr]
        return True
    try:
        if _APPLY_LOCK is None:
            _APPLY_LOCK = open(agent.APPLY_LOCK_FILE, "w")
        fcntl.flock(_APPLY_LOCK, fcntl.LOCK_EX)  # type: ignore[union-attr]
        return True
    except Exception as e:
        agent.log(f"Could not acquire apply lock: {e}")
        return False


def release_apply_lock() -> None:
    global _APPLY_LOCK
    if fcntl is None or _APPLY_LOCK is None:  # type: ignore[union-attr]
        return
    try:
        fcntl.flock(_APPLY_LOCK, fcntl.LOCK_UN)  # type: ignore[union-attr]
        _APPLY_LOCK.close()  # type: ignore[union-attr]
    except Exception as e:
        agent.log(f"Could not release apply lock: {e}")
    finally:
        _APPLY_LOCK = None


def _probe_host(host: str, port: int, timeout: float = 1.5) -> bool:
    """One TCP connect probe. For UDP-only protocols a refused/connected
    result still proves the host is up and yields a valid RTT sample."""
    if not host or port <= 0:
        return False
    try:
        ip = socket.gethostbyname(host)
    except Exception:
        return False
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(timeout)
        s.connect((ip, port))
        s.close()
        return True
    except ConnectionRefusedError:
        return True  # host is up, port just doesn't accept TCP (UDP-only protocol)
    except OSError as e:
        if e.errno == errno.ECONNREFUSED:
            return True
        return False
    except Exception:
        return False


def _apply_outbound_cfg(engine: str, ob: dict) -> dict:
    with _engine_lock:
        acquire_apply_lock()
        try:
            if engine == "xray":
                docker_utils._docker(["stop", "singbox-node"])
                cfg = config_builder._xray_cfg_with_outbound(ob)
                with open(agent.XRAY_CONFIG, "w", encoding="utf-8") as f:
                    json.dump(cfg, f, indent=2, ensure_ascii=False)
                agent.log("Xray config: " + json.dumps(cfg, indent=2, ensure_ascii=False)[:600])
                docker_utils._docker(["restart", "xray-node"])
            else:
                docker_utils._docker(["stop", "xray-node"])
                cfg = config_builder._singbox_cfg_with_outbound(ob)
                with open(agent.SINGBOX_CONFIG, "w", encoding="utf-8") as f:
                    json.dump(cfg, f, indent=2, ensure_ascii=False)
                agent.log("Singbox config: " + json.dumps(cfg, indent=2, ensure_ascii=False)[:600])
                docker_utils._ensure_xray_running()
                docker_utils._docker(["restart", "singbox-node"])
            # Record the engine BEFORE probing: test_proxy() reads active_engine
            # from state to pick the container, and during an engine switch the
            # stale value would probe the container that was just stopped and
            # falsely roll back a good config.
            state = agent.load_state()
            state["active_engine"] = engine
            agent.save_state(state)
            ok, status = _wait_for_proxy(max_wait=15.0)
            if not ok:
                agent.log(f"Proxy not healthy after applying {engine}: {status}")
                docker_utils.log_crash_logs("xray-node")
                docker_utils.log_crash_logs("singbox-node")
            return cfg
        finally:
            release_apply_lock()


def _socks5_probe(ip: str, port: int, timeout: float = 5.0) -> Tuple[bool, str]:
    """Full SOCKS5 handshake against a local inbound.

    A bare connect+close leaves the inbound with an aborted request and logs
    'use of closed network connection' noise on the proxy. A complete
    handshake (NO-AUTH greeting + CONNECT, reply fully read) proves the proxy
    stack responds and closes cleanly after the full reply is consumed.
    """
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(timeout)
        s.connect((ip, port))
        s.sendall(b"\x05\x01\x00")  # SOCKS5, one method: no-auth
        if s.recv(2) != b"\x05\x00":
            s.close()
            return False, "SOCKS5 greeting rejected"
        # CONNECT 127.0.0.1:1 - guaranteed-refused local target, so the
        # inbound always completes the handshake with a protocol reply.
        s.sendall(b"\x05\x01\x00\x01\x7f\x00\x00\x01\x00\x01")
        reply = s.recv(4)
        s.close()
        if len(reply) < 4 or reply[0] != 0x05:
            return False, "Invalid SOCKS5 reply"
        return True, "Healthy (socks5 handshake ok)"
    except Exception as e:
        return False, f"SOCKS5 probe failed: {e}"


def _http_probe(ip: str, port: int, timeout: float = 5.0) -> Tuple[bool, str]:
    """Clean minimal HTTP request against the local http-in.

    Sends a valid absolute-form HEAD request and reads the proxy's full
    response, so the inbound never sees an aborted connection (the source of
    'read http request: use of closed network connection' log spam).
    """
    import http.client
    try:
        conn = http.client.HTTPConnection(ip, port, timeout=timeout)
        # Absolute-form request to a refused local target: the proxy replies
        # (e.g. 502) after fully reading the request - a clean round-trip.
        conn.request("HEAD", "http://127.0.0.1:9/", headers={"Connection": "close"})
        resp = conn.getresponse()
        resp.read()
        conn.close()
        return True, f"Healthy (http {resp.status})"
    except Exception as e:
        return False, f"HTTP probe failed: {e}"


def test_proxy() -> Tuple[bool, str]:
    state = agent.load_agent_state()
    engine_name = state.get("active_engine", "xray") if state else "xray"
    container = "singbox-node" if engine_name == "singbox" else "xray-node"
    status = docker_utils._docker_output(["inspect", "-f", "{{.State.Status}}", container])
    if status != "running":
        return False, f"Container not running (status: {status or 'unknown'})"

    # Resolve the shared network namespace IP. Try xray-node DNS first (fast
    # path), fall back to docker inspect when the DNS entry is stale or the
    # container is mid-restart (Docker's embedded DNS can hang otherwise).
    ip = None
    try:
        old_timeout = socket.getdefaulttimeout()
        socket.setdefaulttimeout(2.0)
        try:
            ip = socket.gethostbyname("xray-node")
        finally:
            socket.setdefaulttimeout(old_timeout)
    except Exception:
        pass

    if not ip:
        # Fallback: read the IP directly from Docker
        ip = docker_utils._docker_output([
            "inspect", "-f",
            "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
            "xray-node",
        ])

    if not ip:
        return False, "Cannot resolve xray-node IP (DNS + docker inspect both failed)"

    ok, msg = _socks5_probe(ip, 6357)
    if not ok:
        return False, msg
    ok, msg = _http_probe(ip, 6358)
    if not ok:
        return False, msg
    return True, msg


def _wait_for_proxy(max_wait: float = 15.0, interval: float = 1.0) -> Tuple[bool, str]:
    """Poll test_proxy() until it succeeds or max_wait seconds elapse.

    On slow 1-2GB VPSes sing-box/xray can take 5-10 seconds to start listening
    on ports 6357/6358; a single fixed sleep was racing that startup and
    false-flagging healthy configs as "proxy dead". Caller is expected to hold
    _engine_lock, so the polling cannot be interleaved with another switch.
    """
    deadline = time.monotonic() + max_wait
    last_status = "timeout"
    while time.monotonic() < deadline:
        time.sleep(interval)
        ok, status = test_proxy()
        if ok:
            return True, status
        last_status = status
    return False, last_status


def apply_configs(engine: str, servers: list, active_idx: int = 0) -> bool:
    if not servers:
        agent.log(f"No servers for {engine}, keeping existing config")
        return False
    with _engine_lock:
        acquire_apply_lock()
        try:
            if engine == "xray":
                docker_utils._docker(["stop", "singbox-node"])
                cfg = config_builder.build_xray_config(servers, active_idx)
                with open(agent.XRAY_CONFIG, "w", encoding="utf-8") as f:
                    json.dump(cfg, f, indent=2, ensure_ascii=False)
                agent.log(f"Wrote xray config ({len(cfg['outbounds'])} outbounds)")
                agent.log("Xray config: " + json.dumps(cfg, indent=2, ensure_ascii=False)[:600])
                docker_utils._docker(["restart", "xray-node"])
            else:
                docker_utils._docker(["stop", "xray-node"])
                cfg = config_builder.build_singbox_config(servers, active_idx)
                with open(agent.SINGBOX_CONFIG, "w", encoding="utf-8") as f:
                    json.dump(cfg, f, indent=2, ensure_ascii=False)
                agent.log(f"Wrote singbox config ({len(cfg['outbounds'])} outbounds)")
                agent.log("Singbox config: " + json.dumps(cfg, indent=2, ensure_ascii=False)[:600])
                docker_utils._ensure_xray_running()
                docker_utils._docker(["restart", "singbox-node"])
        finally:
            release_apply_lock()
        # Record the engine BEFORE probing: test_proxy() reads active_engine
        # from state to pick the container, and during an engine switch the
        # stale value would probe the container that was just stopped and
        # falsely roll back a good config.
        state = agent.load_state()
        state["active_engine"] = engine
        agent.save_state(state)
        ok, status = _wait_for_proxy(max_wait=15.0)
        if not ok:
            agent.log(f"Proxy down after applying {engine}, rolling back...")
            docker_utils.log_crash_logs("xray-node")
            docker_utils.log_crash_logs("singbox-node")
            if agent.restore_rollback(engine):
                docker_utils.docker_restart(f"{engine}-node")
                agent.log("Rolled back to previous config")
            else:
                agent.log("No rollback available")
            return False
        agent.log(f"Proxy verified after apply, IP: {status}")
        return True


def rollback_engine(engine: str) -> bool:
    if agent.restore_rollback(engine):
        docker_utils.docker_restart(f"{engine}-node")
        return True
    agent.log(f"No rollback available for {engine}")
    return False


def restore_active_vpn() -> None:
    state = agent.load_state()
    engine = state.get("active_engine", "")
    server_name = state.get("active_server", "")
    url = state.get("active_url", "")
    if not engine or not server_name:
        agent.log("No active VPN in state, keeping current config")
        return
    if not url:
        for s in agent.load_cache():
            if s.get("name") == server_name or s.get("tag") == server_name or s.get("id") == server_name:
                url = s.get("url", "")
                break
    if not url:
        agent.log(f"Active VPN '{server_name}' has no saved URL, cannot restore (select it again via Option 3)")
        return
    agent.log(f"Restoring active VPN: {server_name} ({engine})")
    outbound_engine, ob = config_builder.parse_url_to_outbound(url, engine=engine)
    if outbound_engine != engine:
        agent.log(f"URL protocol {outbound_engine} doesn't match saved engine {engine}, using URL parsing result anyway")
        engine = outbound_engine
    agent.save_rollback(engine)
    _apply_outbound_cfg(engine, ob)
    agent.log(f"VPN config restored: {server_name}")


def update_subscription() -> bool:
    """Fetch + apply every configured subscription. Returns True if a config
    was applied. v1.2.0: supports multiple subscription URLs; every cached
    server is tagged with its provider (friendly name from the master's
    provider dictionary, or the URL host as fallback)."""
    state = agent.load_state()
    sub_urls = agent.get_sub_urls(state)
    if not sub_urls:
        agent.log("No sub_urls configured, skipping subscription fetch")
        return False

    # Friendly provider names pushed by the master on poll (domain -> name).
    provider_names = state.get("providers") or {}

    # CRITICAL: iterate the ENTIRE sub_urls array and fetch EVERY URL. The
    # master stores multiple subscriptions per node; fetching only the first
    # entry would silently drop whole providers from the cache.
    all_servers = []
    fetched_count = 0
    seen_urls = set()
    for sub_url in sub_urls:
        sub_url = str(sub_url or "").strip()
        if not sub_url:
            continue
        if sub_url in seen_urls:
            agent.log(f"Skipping duplicate subscription URL: {sub_url}")
            continue
        seen_urls.add(sub_url)
        agent.log(f"Fetching subscription from {sub_url}")
        try:
            servers = subscriptions.parse_subscription(sub_url)
        except Exception as e:
            agent.log(f"Subscription fetch failed for {sub_url}: {e}")
            servers = []
        if not servers:
            agent.log(f"No servers found in subscription {sub_url}")
            continue
        # Provider tagging: friendly name from the master mapping, else the
        # subscription URL host (www.example.com -> example.com).
        host = sub_url.split("://")[-1].split("/")[0].lower()
        provider = provider_names.get(host, "") or host
        for s in servers:
            s["provider"] = provider
        all_servers.extend(servers)
        fetched_count += 1
        agent.log(f"Parsed {len(servers)} servers from {host} ({provider})")
    agent.log(f"Fetched {len(all_servers)} servers in total from {fetched_count} subscription URL(s)")

    if not all_servers:
        agent.log("No servers found in any subscription")
        agent.save_cache([])
        return False

    servers = all_servers
    agent.save_cache(servers)

    xray_servers = [s for s in servers if s.get("engine") == "xray"]
    singbox_servers = [s for s in servers if s.get("engine") == "singbox"]

    agent.log(f"Successfully parsed {len(servers)} servers from subscription")

    state = agent.load_state()
    current_engine = state.get("active_engine", "xray")
    active_name = state.get("active_server", "")

    # VPN state retention: keep the current server when it still exists in the
    # fresh subscription; otherwise fall back to the first available server.
    active_exists = any(
        s.get("tag") == active_name or s.get("name") == active_name
        for s in servers
    )
    if active_name and not active_exists:
        fallback = servers[0]
        fallback_name = fallback.get("tag") or fallback.get("name") or ""
        agent.log(f"Active server '{active_name}' no longer in subscription, falling back to '{fallback_name}'")
        state["active_server"] = fallback_name
        state["active_provider"] = fallback.get("provider", "")
        if fallback.get("full_link"):
            state["active_url"] = fallback["full_link"]
        agent.save_state(state)
        active_name = fallback_name

    def _resolve_idx(subset: list) -> int:
        if not active_name:
            return 0
        active_provider = state.get("active_provider", "")
        for i, s in enumerate(subset):
            if (s.get("tag") == active_name or s.get("name") == active_name) and (
                not active_provider or s.get("provider", "") == active_provider
            ):
                return i
        for i, s in enumerate(subset):
            if s.get("tag") == active_name or s.get("name") == active_name:
                return i
        return -1

    def _apply_engine(engine: str, subset: list) -> bool:
        idx = _resolve_idx(subset)
        if idx < 0:
            fallback = subset[0] if subset else None
            if not fallback:
                return False
            fallback_name = fallback.get("tag") or fallback.get("name") or ""
            agent.log(f"Active server '{active_name}' not in {engine} subset, applying fallback '{fallback_name}'")
            state["active_server"] = fallback_name
            state["active_provider"] = fallback.get("provider", "")
            if fallback.get("full_link"):
                state["active_url"] = fallback["full_link"]
            agent.save_state(state)
            idx = 0
        agent.save_rollback(engine)
        return apply_configs(engine, subset, active_idx=idx)

    applied = False
    active_engine_new = ""
    if active_name:
        for s in servers:
            if s.get("tag") == active_name or s.get("name") == active_name:
                active_engine_new = s.get("engine", "")
                break
    if active_engine_new and active_engine_new != current_engine:
        subset = xray_servers if active_engine_new == "xray" else singbox_servers
        agent.log(f"Active server '{active_name}' is a {active_engine_new} server now, switching engine {current_engine} -> {active_engine_new}")
        applied = _apply_engine(active_engine_new, subset)
    if not applied:
        if current_engine == "xray" and xray_servers:
            applied = _apply_engine("xray", xray_servers)
        elif current_engine == "singbox" and singbox_servers:
            applied = _apply_engine("singbox", singbox_servers)
        elif xray_servers:
            agent.log("No xray servers found for current engine, switching to xray")
            agent.save_rollback("xray")
            apply_configs("xray", xray_servers)
            state["active_engine"] = "xray"
            applied = True
        elif singbox_servers:
            agent.log("No singbox servers found for current engine, switching to singbox")
            agent.save_rollback("singbox")
            apply_configs("singbox", singbox_servers)
            state["active_engine"] = "singbox"
            applied = True

    tags = [s.get("tag", "") for s in servers]
    network.report(
        external_ip=network._lan_ip(),
        outbound_json=json.dumps({"server_count": len(servers), "servers": tags}),
        status="Fetched",
        message=f"Subscription parsed: {len(servers)} servers",
    )

    if servers:
        if not state.get("active_server"):
            state["active_server"] = servers[0].get("tag", "")
            state["active_provider"] = servers[0].get("provider", "")
        if not state.get("active_url"):
            for s in servers:
                if s.get("tag") == state.get("active_server"):
                    state["active_url"] = s.get("full_link", "")
                    break
        state["active_proto"] = servers[0].get("type", "")
        state["last_seen"] = time.strftime("%Y-%m-%d %H:%M:%S")
        agent.save_state(state)
    return applied


def fetch_subscription_now(url: Optional[str] = None) -> int:
    if not url:
        state = agent.load_state()
        sub_urls = agent.get_sub_urls(state)
        if sub_urls:
            url = sub_urls[0]
    if not url:
        agent.log("No sub_url configured, cannot fetch subscription")
        return 0
    state = agent.load_state()
    if url not in agent.get_sub_urls(state):
        state["sub_urls"] = agent.get_sub_urls(state) + [url]
    state["sub_url"] = url
    agent.save_state(state)
    provider_names = state.get("providers") or {}
    host = url.split("://")[-1].split("/")[0].lower()
    provider = provider_names.get(host, "") or host
    agent.log(f"Fetching subscription from: {url}")
    try:
        resp = subscriptions.requests.get(url, headers={"User-Agent": subscriptions.SUB_USER_AGENT}, verify=False, timeout=15)
        agent.log(f"HTTP status: {resp.status_code}, len: {len(resp.text)}")
        if resp.status_code != 200:
            agent.log(f"Subscription fetch returned {resp.status_code}")
            return 0
        raw = resp.text.strip()
        padded = raw + "=" * (-len(raw) % 4)
        try:
            decoded = base64.b64decode(padded).decode("utf-8", errors="ignore")
            lines = decoded.splitlines()
        except Exception:
            lines = raw.splitlines()
        servers = []
        for line in lines:
            line = line.strip()
            if "://" not in line:
                continue
            try:
                srv = subscriptions._parse_link(line)
                if srv:
                    srv["provider"] = provider
                    servers.append(srv)
            except Exception:
                continue
        agent.log(f"Successfully parsed {len(servers)} servers from subscription")
        agent.save_cache(servers)
        agent.log("Subscription saved. Use Option 3 to select a server.")
        return len(servers)
    except Exception as e:
        agent.log(f"Error fetching subscription: {e}")
        agent.log(traceback.format_exc())
        return 0


def select_server(idx: int, mode: str = "manual") -> int:
    servers = agent.load_cache()
    if idx < 0 or idx >= len(servers):
        agent.log(f"Invalid server index {idx}, have {len(servers)} servers")
        return 1
    srv = servers[idx]
    name = srv.get("name", f"Server {idx + 1}")
    engine = srv.get("engine", "xray")
    url = srv.get("url", "")
    agent.log(f"Selecting server {idx + 1}: {name} ({engine})")
    if not url:
        agent.log(f"No URL for server {idx + 1}, cannot build config")
        return 1

    host = srv.get("host", "")
    port = int(srv.get("port", 0) or 0)
    if host and port > 0 and not _probe_host(host, port):
        agent.log(f"Server {name} ({host}:{port}) unreachable, keeping current selection")
        network.report(status="Switch failed", message=f"{name} unreachable, kept current server")
        return 1

    outbound_engine, ob = config_builder.parse_url_to_outbound(url, engine=engine)
    if outbound_engine != engine:
        agent.log(f"URL protocol {outbound_engine} doesn't match cached engine {engine}, using URL parsing result anyway")
        engine = outbound_engine

    agent.save_rollback(engine)
    cfg = _apply_outbound_cfg(engine, ob)

    state = agent.load_state()
    state["active_server"] = name
    state["active_provider"] = srv.get("provider", "")
    state["active_engine"] = engine
    state["active_proto"] = srv.get("proto", "")
    state["active_url"] = url
    state["active_mode"] = mode
    state["last_seen"] = time.strftime("%Y-%m-%d %H:%M:%S")
    agent.save_state(state)

    agent.log(f"Switched to {name}")
    network.report(engine=engine, protocol=srv.get("proto", ""), status="Verified & Active", message=f"Switched to {name}")
    return 0


def benchmark_servers(probes: int = agent.BENCH_PROBES, timeout: float = agent.BENCH_TIMEOUT, progress: Optional[Callable[[int, str, str], None]] = None) -> dict:
    """TCP-probe every cached server and measure latency / jitter / loss.

    For UDP-only protocols (hysteria2/tuic/wireguard) a TCP 'connection
    refused' still proves the host is up and yields a valid RTT sample.
    Returns {cache_idx: {"latency_ms","jitter_ms","loss_pct","ok"}}.
    `progress(idx, name, line)` is called as each server finishes.
    """
    servers = agent.load_cache()
    results: dict = {}
    for idx, srv in enumerate(servers):
        host = srv.get("host", "")
        port = int(srv.get("port", 0) or 0)
        entry = {"latency_ms": None, "jitter_ms": None, "loss_pct": 100.0, "ok": False}
        if not host or port <= 0:
            results[idx] = entry
            continue
        try:
            ip = socket.gethostbyname(host)
        except Exception as e:
            agent.log(f"[bench] {host} resolve failed: {e}")
            results[idx] = entry
            continue

        times_ms: list = []
        for _ in range(max(probes, 1)):
            t0 = time.monotonic()
            try:
                s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                s.settimeout(timeout)
                s.connect((ip, port))
                s.close()
                times_ms.append((time.monotonic() - t0) * 1000.0)
            except socket.timeout:
                pass
            except ConnectionRefusedError:
                times_ms.append((time.monotonic() - t0) * 1000.0)
            except OSError as e:
                if e.errno == errno.ECONNREFUSED:
                    times_ms.append((time.monotonic() - t0) * 1000.0)
            time.sleep(0.05)

        if times_ms:
            entry["latency_ms"] = round(sum(times_ms) / len(times_ms), 1)
            entry["jitter_ms"] = round(max(times_ms) - min(times_ms), 1)
            entry["loss_pct"] = round(100.0 * (1 - len(times_ms) / max(probes, 1)), 0)
            entry["ok"] = True
        results[str(idx)] = entry
        if progress is not None:
            if entry.get("ok"):
                line = f"{entry['latency_ms']:.1f} ms  jit {entry['jitter_ms']:.1f} ms  loss {entry['loss_pct']:.0f}%"
            else:
                line = "UNREACHABLE"
            progress(idx, srv.get("name", f"Server {idx + 1}"), line)
    return results


def _bench_progress(idx: int, name: str, line: str) -> None:
    print(f"  {idx + 1:2d}) {name:<35s} {line}", flush=True)


def load_benchmark_cache() -> dict:
    try:
        with open(agent.BENCH_FILE, encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return {}


def save_benchmark(results: dict, mode: str) -> None:
    try:
        with open(agent.BENCH_FILE, "w", encoding="utf-8") as f:
            json.dump({"ts": time.time(), "mode": mode, "results": results}, f, indent=2)
        agent.log(f"Benchmark cached for {mode} mode")
    except Exception as e:
        agent.log(f"Failed to cache benchmark: {e}")


def get_benchmark(max_age: int = agent.BENCH_TTL) -> Tuple[dict, float, bool]:
    """Return (results, ts, fresh). Fresh = cached within max_age seconds."""
    data = load_benchmark_cache()
    ts = float(data.get("ts", 0))
    if ts and time.time() - ts <= max_age:
        return data.get("results", {}), ts, True
    return {}, ts, False


def print_benchmark(results: dict) -> None:
    servers = agent.load_cache()
    print()
    print("Server Benchmark (latency / jitter / loss):")
    print("--------------------------------------------")
    for idx in range(len(servers)):
        name = servers[idx].get("name", f"Server {idx + 1}")
        r = results.get(str(idx), {"latency_ms": None, "jitter_ms": None, "loss_pct": 100.0, "ok": False})
        if r.get("ok"):
            print(f" {idx + 1:2d}) {name:<35s} {r['latency_ms']:>7.1f} ms  jit {r['jitter_ms']:>6.1f} ms  loss {r['loss_pct']:.0f}%")
        else:
            print(f" {idx + 1:2d}) {name:<35s}   UNREACHABLE")
    print("--------------------------------------------")


def _balanced_score(entry: dict) -> float:
    """Composite 'balanced' stability score: lower is better.

    Loss dominates, then jitter, then latency - mirrors the
    (loss, jitter, latency) ranking tuple used for selection.
    """
    loss = float(entry.get("loss_pct") or 0.0)
    jitter = float(entry.get("jitter_ms") or 0.0)
    latency = float(entry.get("latency_ms") or 0.0)
    return loss * 100.0 + jitter * 10.0 + latency


def _switch_target(results: dict, servers: list, active_name: str, mode: str, threshold: float = 0.25) -> Optional[int]:
    """Choose the server to switch to, or None to keep the current one.

    Hysteresis against flapping: the active server is only replaced when it
    is dead (missing from the reachable set) or the best alternative scores
    at least `threshold` (25%) better - by latency for 'fastest', by the
    composite stability score for 'balanced'. Transient benchmark noise can
    no longer bounce the active server back and forth.
    """
    reachable = {int(idx): r for idx, r in results.items() if r.get("ok")}
    if not reachable:
        return None
    if mode == "fastest":
        best = min(reachable, key=lambda i: reachable[i]["latency_ms"])
    else:
        best = min(reachable, key=lambda i: (reachable[i]["loss_pct"], reachable[i]["jitter_ms"], reachable[i]["latency_ms"]))
    active_idx = next(
        (i for i, s in enumerate(servers) if s.get("tag") == active_name or s.get("name") == active_name or s.get("id") == active_name),
        -1,
    )
    if active_idx < 0 or active_idx not in reachable:
        return best
    if active_idx == best:
        return None
    cur = reachable[active_idx]
    bst = reachable[best]
    if mode == "fastest":
        better = bst["latency_ms"] <= cur["latency_ms"] * (1.0 - threshold)
    else:
        better = _balanced_score(bst) <= _balanced_score(cur) * (1.0 - threshold)
    return best if better else None


def select_mode(mode: str) -> int:
    """Auto-select the best server by mode: 'fastest' or 'balanced'.

    Uses the cached benchmark when fresh (no ping spam); otherwise probes
    with live progress output so the CLI never appears frozen. The active
    server is kept unless it is dead or an alternative is at least 25%
    better (hysteresis against flapping).
    """
    mode = (mode or "manual").strip().lower()
    if mode not in ("fastest", "balanced"):
        agent.log(f"Unknown selection mode '{mode}'")
        return 1
    servers = agent.load_cache()
    if not servers:
        agent.log("No servers in cache, cannot auto-select")
        return 1

    results, ts, fresh = get_benchmark()
    if fresh:
        agent.log(f"Using cached benchmark ({int(time.time() - ts)}s old)")
    else:
        agent.log(f"Benchmarking {len(servers)} servers ({mode})...")
        results = benchmark_servers(progress=_bench_progress)
        save_benchmark(results, mode)
    print_benchmark(results)

    state = agent.load_state()
    active = state.get("active_server", "")
    best = _switch_target(results, servers, active, mode)
    if best is None:
        agent.log(f"{mode.capitalize()} mode: keeping current server '{active}' (no alternative at least 25% better)")
        state["active_mode"] = mode
        agent.save_state(state)
        return 0

    if mode == "fastest":
        agent.log(f"Fastest mode: server {best + 1} ({servers[best].get('name', '')}) at {results[str(best)]['latency_ms']} ms")
    else:
        agent.log(f"Balanced mode: server {best + 1} ({servers[best].get('name', '')}) loss={results[str(best)]['loss_pct']:.0f}% jitter={results[str(best)]['jitter_ms']} ms")

    rc = select_server(best, mode=mode)
    if rc == 0:
        agent.log(f"Active selection mode saved: {mode}")
    return rc


def print_server_list() -> None:
    servers = agent.load_cache()
    if not servers:
        print("No servers in cache. Use Option 1 to update subscription first.")
        return
    print()
    print("Available VPN Servers:")
    print()
    last_provider = None
    for idx, srv in enumerate(servers):
        provider = srv.get("provider", "") or "Other"
        if provider != last_provider:
            print(f"  [{provider}]")
            last_provider = provider
        name = srv.get("name", f"Server {idx + 1}")
        proto = srv.get("proto", "unknown")
        engine = srv.get("engine", "singbox")
        print(f" {idx + 1:2d}) {name:<35s} {proto:<10s} ({engine})")
    print()
    print(f"Total: {len(servers)} servers")
    print()
