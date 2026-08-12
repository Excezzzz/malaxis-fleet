#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Engine: subscription fetching, config application with rollback, server
selection (manual / fastest / balanced), benchmarking and proxy health."""
import base64
import errno
import json
import os
import socket
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
    engine = state.get("active_engine", "singbox")
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
    except Exception:
        return False


def _apply_outbound_cfg(engine: str, ob: dict) -> dict:
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
        return cfg
    finally:
        release_apply_lock()


def test_proxy() -> Tuple[bool, str]:
    state = agent.load_agent_state()
    engine = state.get("active_engine", "singbox") if state else "singbox"
    container = "singbox-node" if engine == "singbox" else "xray-node"
    status = docker_utils._docker_output(["inspect", "-f", "{{.State.Status}}", container])
    if status != "running":
        return False, f"Container not running (status: {status or 'unknown'})"
    try:
        # singbox-node shares xray-node's network namespace and has no own
        # DNS entry, so probe the shared netns (xray-node) for port 6357.
        ip = socket.gethostbyname("xray-node")
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(5.0)
        s.connect((ip, 6357))
        s.close()
        return True, "Healthy"
    except Exception:
        return False, "Socket dead"


def apply_configs(engine: str, servers: list, active_idx: int = 0) -> bool:
    if not servers:
        agent.log(f"No servers for {engine}, keeping existing config")
        return False
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
    time.sleep(3)
    ok, status = test_proxy()
    if not ok:
        agent.log(f"Proxy down after applying {engine}, rolling back...")
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
    """Fetch + apply the subscription. Returns True if a config was applied."""
    state = agent.load_state()
    sub_url = state.get("sub_url", "")
    if not sub_url:
        agent.log("No sub_url configured, skipping subscription fetch")
        return False

    agent.log(f"Fetching subscription from {sub_url}")
    servers = subscriptions.parse_subscription(sub_url)
    if not servers:
        agent.log("No servers found in subscription")
        agent.save_cache([])
        return False

    agent.save_cache(servers)

    xray_servers = [s for s in servers if s.get("engine") == "xray"]
    singbox_servers = [s for s in servers if s.get("engine") == "singbox"]

    agent.log(f"Successfully parsed {len(servers)} servers from subscription")

    state = agent.load_state()
    current_engine = state.get("active_engine", "singbox")
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
        if fallback.get("full_link"):
            state["active_url"] = fallback["full_link"]
        agent.save_state(state)
        active_name = fallback_name

    def _resolve_idx(subset: list) -> int:
        if not active_name:
            return 0
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
            if fallback.get("full_link"):
                state["active_url"] = fallback["full_link"]
            agent.save_state(state)
            idx = 0
        agent.save_rollback(engine)
        return apply_configs(engine, subset, active_idx=idx)

    applied = False
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
        url = state.get("sub_url")
    if not url:
        agent.log("No sub_url configured, cannot fetch subscription")
        return 0
    state = agent.load_state()
    state["sub_url"] = url
    agent.save_state(state)
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
    engine = srv.get("engine", "singbox")
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
    for idx, srv in enumerate(servers):
        name = srv.get("name", f"Server {idx + 1}")
        proto = srv.get("proto", "unknown")
        engine = srv.get("engine", "singbox")
        print(f" {idx + 1:2d}) {name:<35s} {proto:<10s} ({engine})")
    print()
    print(f"Total: {len(servers)} servers")
    print()
