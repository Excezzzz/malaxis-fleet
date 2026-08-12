#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Shared agent state: config paths, node identity, state/cache persistence.

Every other agent_src module imports this one for logging, path constants,
the immutable node identity (NODE_ID / HARDWARE_HASH) and the JSON helpers
used by the daemon, the CLI and the worker.
"""
import hashlib
import json
import os
import platform
import shutil
import sys
import time
import uuid
from typing import Union

SERVER_URL = os.environ.get("SERVER_URL", "https://__API_DOMAIN__")
SECRET_TOKEN = os.environ.get("SECRET_TOKEN", "__FLEET_SECRET__")
POLL_INTERVAL = int(os.environ.get("POLL_INTERVAL", "30"))
HEALTH_INTERVAL = int(os.environ.get("HEALTH_INTERVAL", "60"))
HEALTH_FAIL_THRESHOLD = int(os.environ.get("HEALTH_FAIL_THRESHOLD", "5"))
BENCH_TTL = int(os.environ.get("BENCH_TTL", "600"))
BENCH_PROBES = int(os.environ.get("BENCH_PROBES", "2"))
BENCH_TIMEOUT = float(os.environ.get("BENCH_TIMEOUT", "1.5"))

CONFIG_DIR = "/app/configs"
NODE_ID_FILE = os.path.join(CONFIG_DIR, "node_id.txt")
XRAY_CONFIG = os.path.join(CONFIG_DIR, "xray_config.json")
SINGBOX_CONFIG = os.path.join(CONFIG_DIR, "singbox_config.json")
SUBCACHE = os.path.join(CONFIG_DIR, "subscription_cache.json")
AGENT_STATE = "/app/configs/agent_state.json"
ROLLBACK_DIR = os.path.join(CONFIG_DIR, ".rollback")
BENCH_FILE = os.path.join(CONFIG_DIR, "benchmark_cache.json")
APPLY_LOCK_FILE = os.path.join(CONFIG_DIR, "apply.lock")


def log(msg: str) -> None:
    print(f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] [agent] {msg}", flush=True)


def load_json(path: str) -> Union[dict, list]:
    if os.path.exists(path):
        try:
            with open(path) as f:
                return json.load(f)
        except (json.JSONDecodeError, OSError):
            pass
    return {} if path.endswith(".json") else []


def save_json(path: str, data: Union[dict, list]) -> None:
    with open(path, "w") as f:
        json.dump(data, f, indent=2, ensure_ascii=False)


def load_state() -> dict:
    d = load_json(AGENT_STATE)
    if not isinstance(d, dict):
        d = {}
    return d


load_agent_state = load_state


def save_state(state: dict) -> None:
    save_json(AGENT_STATE, state)


def load_cache() -> list:
    c = load_json(SUBCACHE)
    if not isinstance(c, list):
        c = []
    return c


def save_cache(servers: list) -> None:
    normalized = []
    for idx, s in enumerate(servers):
        tag = s.get("tag", "")
        proto = s.get("type", "")
        name = tag or f"Server {idx + 1} ({proto.upper()})"
        normalized.append({
            "id": idx + 1,
            "name": name,
            "proto": proto,
            "engine": s.get("engine", "singbox"),
            "host": s.get("hostname", ""),
            "port": s.get("port", 0),
            "url": s.get("full_link", ""),
        })
    save_json(SUBCACHE, normalized)
    log(f"Saved {len(normalized)} servers to cache")


def save_rollback(engine: str) -> None:
    os.makedirs(ROLLBACK_DIR, exist_ok=True)
    src = XRAY_CONFIG if engine == "xray" else SINGBOX_CONFIG
    dst = os.path.join(ROLLBACK_DIR, f"{engine}_config.json")
    if os.path.exists(src):
        shutil.copy2(src, dst)
        log(f"Saved rollback for {engine}")


def restore_rollback(engine: str) -> bool:
    dst = XRAY_CONFIG if engine == "xray" else SINGBOX_CONFIG
    src = os.path.join(ROLLBACK_DIR, f"{engine}_config.json")
    if os.path.exists(src):
        shutil.copy2(src, dst)
        log(f"Restored rollback for {engine}")
        return True
    return False


def clear_rollback(engine: str) -> None:
    path = os.path.join(ROLLBACK_DIR, f"{engine}_config.json")
    if os.path.exists(path):
        os.remove(path)


def _load_node_id() -> str:
    if os.path.exists(NODE_ID_FILE):
        with open(NODE_ID_FILE) as f:
            val = f.read().strip()
            if val:
                return val
    val = uuid.uuid4().hex[:12]
    os.makedirs(CONFIG_DIR, exist_ok=True)
    with open(NODE_ID_FILE, "w") as f:
        f.write(val)
    return val


def _adopt_node_id(new_id: str) -> None:
    """Persist the canonical node_id handed back by the server (hardware
    fingerprint dedup): after a reinstall the device keeps its original id."""
    global NODE_ID
    if not new_id or new_id == NODE_ID:
        return
    log(f"Adopting canonical node_id {new_id} (was {NODE_ID})")
    NODE_ID = new_id
    os.makedirs(CONFIG_DIR, exist_ok=True)
    with open(NODE_ID_FILE, "w") as f:
        f.write(new_id)


def get_hardware_hash() -> str:
    """Immutable device fingerprint: sha256 of hostname + primary MAC + system
    UUID. Used by the server to dedupe re-registered nodes."""
    try:
        host = platform.node() or ""
        mac = ""
        if sys.platform.startswith("win"):
            try:
                import subprocess as _sp
                out = _sp.run(["getmac", "/FO", "CSV", "/NH"], capture_output=True, timeout=10)
                text = out.stdout.decode(errors="replace")
                for line in text.splitlines():
                    parts = line.strip().strip('"').split('","')
                    for p in parts:
                        p = p.strip().strip('"')
                        if "-" in p or ":" in p:
                            mac = p
                            break
                    if mac:
                        break
            except Exception:
                pass
            if not mac:
                mac = str(uuid.getnode())
        else:
            mac = ""
            for nic in sorted(os.listdir("/sys/class/net/")):
                try:
                    with open(f"/sys/class/net/{nic}/address") as f:
                        addr = f.read().strip()
                    if addr and addr != "00:00:00:00:00:00" and nic != "lo":
                        mac = addr
                        break
                except OSError:
                    continue
            if not mac:
                mac = str(uuid.getnode())
        uuid_str = ""
        if sys.platform.startswith("linux"):
            try:
                with open("/sys/class/dmi/id/product_uuid") as f:
                    uuid_str = f.read().strip()
            except OSError:
                pass
        if not uuid_str and sys.platform.startswith("win"):
            try:
                import subprocess as _sp
                out = _sp.run(["wmic", "csproduct", "get", "UUID"], capture_output=True, timeout=10)
                text = out.stdout.decode(errors="replace")
                for line in text.splitlines()[1:]:
                    line = line.strip()
                    if line:
                        uuid_str = line
                        break
            except Exception:
                pass
        if not uuid_str:
            uuid_str = str(uuid.getnode())
        raw = "|".join([host, mac, uuid_str])
        return hashlib.sha256(raw.encode()).hexdigest()
    except Exception:
        return ""


NODE_ID = _load_node_id()
HARDWARE_HASH = get_hardware_hash()
