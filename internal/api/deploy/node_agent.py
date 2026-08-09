#!/usr/bin/env python3
# -*- coding: utf-8 -*-
import base64
import errno
import hashlib
import hmac
import json
import os
import platform
import random
import re
import requests
import signal
import shutil
import socket
import subprocess
import sys
import time
import traceback
import uuid
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Callable, Optional, Tuple, Union

try:
    import fcntl  # type: ignore[import-not-found]
except ImportError:  # Windows: module does not exist
    fcntl = None  # type: ignore[assignment]
import queue

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

NODE_ID = _load_node_id()


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

HARDWARE_HASH = get_hardware_hash()

POLL_URL = f"{SERVER_URL}/api/poll"
REPORT_URL = f"{SERVER_URL}/api/report"
CONFIG_DIR = "/app/configs"
XRAY_CONFIG = os.path.join(CONFIG_DIR, "xray_config.json")
SINGBOX_CONFIG = os.path.join(CONFIG_DIR, "singbox_config.json")
SUBCACHE = os.path.join(CONFIG_DIR, "subscription_cache.json")
AGENT_STATE = "/app/configs/agent_state.json"
ROLLBACK_DIR = os.path.join(CONFIG_DIR, ".rollback")
BENCH_FILE = os.path.join(CONFIG_DIR, "benchmark_cache.json")
APPLY_LOCK_FILE = os.path.join(CONFIG_DIR, "apply.lock")

DEFAULT_XRAY_CONFIG = {
    "log": {"loglevel": "warning"},
    "dns": {
        "servers": ["https://dns.google/dns-query", "https://cloudflare-dns.com/dns-query", "8.8.8.8", "1.1.1.1"],
        "queryStrategy": "UseIPv4",
    },
    "inbounds": [
        {
            "port": 6357, "listen": "0.0.0.0", "protocol": "socks",
            "settings": {"auth": "noauth", "udp": True, "ip": "127.0.0.1"},
            "sniffing": {"enabled": True, "destOverride": ["http", "tls", "quic"], "routeOnly": True},
            "tag": "socks-in",
            "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15, "tcpUserTimeout": 15000},
        },
        {
            "port": 6358, "listen": "0.0.0.0", "protocol": "http", "tag": "http-in",
            "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15},
        },
    ],
    "outbounds": [{"protocol": "freedom", "tag": "direct"}],
    "routing": {
        "domainStrategy": "IPIfNonMatch",
        "rules": [
            {"type": "field", "port": 53, "network": "udp", "outboundTag": "direct"},
        ],
    },
}
DEFAULT_SINGBOX_CONFIG = {
    "log": {"level": "warn"},
    "dns": {
        "servers": [
            {"tag": "resolver", "address": "https://1.1.1.1/dns-query", "detour": "direct", "strategy": "ipv4_only"},
            {"tag": "block", "address": "rcode://success"},
        ],
        "final": "resolver",
        "independent_cache": True,
    },
    "inbounds": [
        {
            "type": "socks", "tag": "socks-in", "listen": "0.0.0.0", "listen_port": 6357,
            "sniff": {"enabled": True, "override_destination": False, "route_only": True},
        },
        {
            "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358,
            "sniff": {"enabled": True, "override_destination": True, "route_only": True},
        },
    ],
    "outbounds": [{"type": "direct", "tag": "direct"}],
    "route": {
        "final": "direct",
        "auto_detect_interface": True,
    },
    "experimental": {"cache_file": {"enabled": True}},
}


def log(msg: str) -> None:
    print(f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] [agent] {msg}", flush=True)


# --- Config Persistence ---

def ensure_default_configs():
    os.makedirs(CONFIG_DIR, exist_ok=True)
    for path, default in [(XRAY_CONFIG, DEFAULT_XRAY_CONFIG), (SINGBOX_CONFIG, DEFAULT_SINGBOX_CONFIG)]:
        if not os.path.exists(path) or os.path.getsize(path) == 0:
            with open(path, "w") as f:
                json.dump(default, f, indent=2)
            log(f"Created default {os.path.basename(path)}")
    state = load_state()
    engine = state.get("active_engine", "xray")
    if engine == "singbox":
        log("Starting singbox-node (xray dummy config for network)...")
        _ensure_xray_running()
        os.system("docker start singbox-node 2>/dev/null || docker restart singbox-node 2>/dev/null || true")
    else:
        log("Stopping singbox-node to free ports 6357/6358...")
        os.system("docker stop singbox-node 2>/dev/null")
        os.system("docker start xray-node 2>/dev/null || true")


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
            "engine": s.get("engine", "xray"),
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
        import shutil
        shutil.copy2(src, dst)
        log(f"Saved rollback for {engine}")


def restore_rollback(engine: str) -> bool:
    dst = XRAY_CONFIG if engine == "xray" else SINGBOX_CONFIG
    src = os.path.join(ROLLBACK_DIR, f"{engine}_config.json")
    if os.path.exists(src):
        import shutil
        shutil.copy2(src, dst)
        log(f"Restored rollback for {engine}")
        return True
    return False


def clear_rollback(engine: str) -> None:
    path = os.path.join(ROLLBACK_DIR, f"{engine}_config.json")
    if os.path.exists(path):
        os.remove(path)


# --- HTTP ---

def _sign(body: bytes, ts: int) -> str:
    mac = hmac.new(SECRET_TOKEN.encode(), str(ts).encode() + body, hashlib.sha256)
    return mac.hexdigest()


def _request(method: str, url: str, payload: dict) -> Tuple[int, Union[dict, str]]:
    body = json.dumps(payload).encode()
    ts = int(time.time())
    headers = {
        "X-Fleet-Timestamp": str(ts),
        "X-Fleet-Signature": _sign(body, ts),
        "Content-Type": "application/json",
    }
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read()
            if resp.status == 200:
                try:
                    return resp.status, json.loads(raw)
                except json.JSONDecodeError:
                    return resp.status, raw.decode()
            return resp.status, raw.decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")
    except urllib.error.URLError as e:
        return 0, str(e)

def poll():
    payload: dict[str, Any] = {
        "id": NODE_ID,
        "hostname": platform.node(),
        "ip_lan": _lan_ip(),
        "hardware_hash": HARDWARE_HASH,
    }
    try:
        state = load_state()
        if state.get("node_name"):
            payload["name"] = state["node_name"]
        if state.get("sub_url"):
            payload["sub_url"] = state["sub_url"]
    except Exception:
        pass
    status, data = _request("POST", POLL_URL, payload)
    if status == 200 and isinstance(data, dict):
        if data.get("node_id") and data["node_id"] != NODE_ID:
            _adopt_node_id(data["node_id"])
        if "command" in data:
            log(f"Command: {data['command']}")
            return data
        if data.get("status") == "ok":
            return None
    log(f"Poll status={status}")
    return None


def report(**kw: Any) -> None:
    payload: dict[str, Any] = {"id": NODE_ID, "hardware_hash": HARDWARE_HASH}
    payload.update(kw)
    try:
        state = load_state()
        payload.setdefault("active_server", state.get("active_server", ""))
        payload.setdefault("sub_url", state.get("sub_url", ""))
        payload.setdefault("engine", state.get("active_engine", ""))
        payload.setdefault("protocol", state.get("active_proto", ""))
        payload.setdefault("name", state.get("node_name", ""))
        servers = load_cache()
        payload.setdefault("available_servers", [s.get("tag") or s.get("name") or f"Server {i + 1}" for i, s in enumerate(servers)])
    except Exception:
        pass
    code, _ = _request("POST", REPORT_URL, payload)
    if code != 200:
        log(f"Report returned {code}")


def _lan_ip() -> str:
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.settimeout(0.5)
        s.connect(("8.8.8.8", 80))
        ip = s.getsockname()[0]
        s.close()
        return ip
    except Exception:
        return ""


# --- Subscription Parsing ---

SUB_USER_AGENT = "v2rayN/6.23 Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"

def parse_subscription(sub_url: str) -> list:
    servers = []
    try:
        resp = requests.get(sub_url, headers={"User-Agent": SUB_USER_AGENT}, verify=False, timeout=15)
        log(f"Sub response status: {resp.status_code}, length: {len(resp.text)}")
        if resp.status_code != 200:
            log(f"Subscription fetch returned {resp.status_code}")
            return servers
        raw_text = resp.text
    except Exception as e:
        log(f"Subscription fetch failed for {sub_url}: {e}")
        log(traceback.format_exc())
        return servers

    stripped = raw_text.strip()
    lines = []
    try:
        padded = stripped + "=" * (-len(stripped) % 4)
        decoded = base64.b64decode(padded).decode("utf-8", errors="ignore")
        lines = decoded.splitlines()
    except Exception:
        lines = stripped.splitlines()

    for line in lines:
        line = line.strip()
        if not line or "://" not in line:
            continue
        try:
            srv = _parse_link(line)
            if srv:
                servers.append(srv)
        except Exception as e:
            log(f"Parse error: {e}")
            continue

    log(f"Parsed {len(servers)} servers from subscription")
    return servers


def _parse_link(link: str) -> Optional[dict]:
    scheme, _, rest = link.partition("://")
    if not scheme or not rest:
        return None

    tag = "Server"
    if "#" in link:
        tag = urllib.parse.unquote(link.split("#", 1)[1].strip())

    info = {
        "full_link": link,
        "type": scheme,
        "tag": tag,
        "pretty_name": tag,
    }

    if scheme in ("vless", "vmess"):
        _parse_vlike(link, info)
        info["protocol"] = scheme
        if scheme == "vless" and info.get("network", "tcp") == "xhttp":
            info["engine"] = "xray"
        else:
            info["engine"] = "singbox"
    elif scheme == "trojan":
        info["engine"] = "singbox"
        info["protocol"] = "trojan"
        _parse_trojan(link, info)
    elif scheme == "ss":
        info["engine"] = "singbox"
        info["protocol"] = "ss"
        _parse_ss(link, info)
    elif scheme in ("hysteria2", "hy2"):
        info["engine"] = "singbox"
        info["protocol"] = "hysteria2"
        _parse_hysteria2(link, info)
    elif scheme == "tuic":
        info["engine"] = "singbox"
        info["protocol"] = "tuic"
        _parse_tuic(link, info)
    elif scheme == "wireguard":
        info["engine"] = "singbox"
        info["protocol"] = "wireguard"
        _parse_wireguard(link, info)
    else:
        info["engine"] = "xray"

    parsed = urllib.parse.urlparse(link)
    if not info.get("hostname"):
        info["hostname"] = parsed.hostname or ""
    if not info.get("port"):
        info["port"] = str(parsed.port or 0)

    return info


def _get_param(query: str, key: str, default: str = "") -> str:
    for part in query.split("&"):
        if "=" in part:
            k, v = part.split("=", 1)
            if k == key:
                return urllib.parse.unquote(v)
    return default


def _parse_vlike(link: str, info: dict) -> None:
    parsed = urllib.parse.urlparse(link)
    info["hostname"] = parsed.hostname or ""
    info["port"] = parsed.port or 0
    info["uuid"] = parsed.username or ""

    if parsed.query:
        for k in ["flow", "encryption", "security", "fp", "pbk", "sid", "sni", "spx", "headerType", "path", "host", "alpn", "allowInsecure", "mode"]:
            v = _get_param(parsed.query, k)
            if v:
                info[k] = v
        if not info.get("sni") and info.get("host"):
            info["sni"] = info["host"]
        transport = _get_param(parsed.query, "type")
        if transport:
            info["network"] = transport
    if not info.get("encryption"):
        info["encryption"] = "none"
    if not info.get("security"):
        info["security"] = "reality"
    if not info.get("network"):
        info["network"] = "tcp"


def _parse_trojan(link: str, info: dict) -> None:
    parsed = urllib.parse.urlparse(link)
    info["hostname"] = parsed.hostname or ""
    info["port"] = parsed.port or 0
    info["password"] = parsed.username or ""
    if parsed.query:
        for k in ["sni", "peer", "security", "path", "host", "alpn", "allowInsecure"]:
            v = _get_param(parsed.query, k)
            if v:
                info[k] = v
        transport = _get_param(parsed.query, "type")
        if transport:
            info["network"] = transport
    if not info.get("security"):
        info["security"] = "tls"
    if not info.get("network"):
        info["network"] = "tcp"


def _parse_ss(link: str, info: dict) -> None:
    parsed = urllib.parse.urlparse(link)
    raw = parsed.username or ""
    try:
        decoded = base64.b64decode(raw + "=" * (-len(raw) % 4)).decode(errors="replace")
        if ":" in decoded:
            info["method"], info["password"] = decoded.split(":", 1)
    except Exception:
        info["password"] = raw
        info["method"] = "chacha20-ietf-poly1305"
    if parsed.hostname:
        info["hostname"] = parsed.hostname
    info["port"] = parsed.port or 0
    if parsed.query:
        for k in ["plugin", "obfs", "path"]:
            v = _get_param(parsed.query, k)
            if v:
                info[k] = v


def _parse_hysteria2(link: str, info: dict) -> None:
    parsed = urllib.parse.urlparse(link)
    info["hostname"] = parsed.hostname or ""
    info["port"] = parsed.port or 0
    info["password"] = parsed.username or ""
    if parsed.query:
        for k in ["sni", "insecure", "obfs", "obfs-password", "pinSHA256"]:
            v = _get_param(parsed.query, k)
            if v:
                info[k] = v
    info["insecure"] = info.get("insecure", "0") == "1"


def _parse_tuic(link: str, info: dict) -> None:
    parsed = urllib.parse.urlparse(link)
    info["hostname"] = parsed.hostname or ""
    info["port"] = parsed.port or 0
    info["uuid"] = parsed.username or ""
    info["password"] = parsed.password or ""
    if parsed.query:
        for k in ["sni", "congestion_control", "udp_relay_mode", "alpn", "allowInsecure"]:
            v = _get_param(parsed.query, k)
            if v:
                info[k] = v
    if not info.get("congestion_control"):
        info["congestion_control"] = "bbr"
    if not info.get("udp_relay_mode"):
        info["udp_relay_mode"] = "native"


def _parse_wireguard(link: str, info: dict) -> None:
    parsed = urllib.parse.urlparse(link)
    info["hostname"] = parsed.hostname or ""
    info["port"] = parsed.port or 0
    if parsed.query:
        for k in ["private_key", "public_key", "endpoint", "mtu", "reserved", "allowed_ips"]:
            v = _get_param(parsed.query, k)
            if v:
                info[k] = v
    if not info.get("private_key"):
        info["private_key"] = parsed.username or ""
    if not info.get("public_key"):
        info["public_key"] = parsed.password or ""
    if not info.get("endpoint"):
        info["endpoint"] = parsed.hostname or ""
        if parsed.port:
            info["endpoint"] += f":{parsed.port}"
    if info.get("mtu"):
        try:
            info["mtu"] = int(info["mtu"])
        except (ValueError, TypeError):
            info["mtu"] = 1420
    if info.get("reserved"):
        try:
            info["reserved"] = [int(x) for x in info["reserved"].split(",")]
        except Exception:
            pass


# --- Config Generation ---

def build_xray_config(servers: list, active_idx: int = 0) -> dict:
    cfg = {
        "log": {"loglevel": "warning"},
        "dns": {
            "servers": ["https://dns.google/dns-query", "https://cloudflare-dns.com/dns-query", "8.8.8.8", "1.1.1.1"],
            "queryStrategy": "UseIPv4",
        },
        "inbounds": [
            {
                "port": 6357, "listen": "0.0.0.0", "protocol": "socks",
                "settings": {"auth": "noauth", "udp": True, "ip": "127.0.0.1"},
                "sniffing": {"enabled": True, "destOverride": ["http", "tls", "quic"], "routeOnly": True},
                "tag": "socks-in",
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15, "tcpUserTimeout": 15000},
            },
            {
                "port": 6358, "listen": "0.0.0.0", "protocol": "http", "tag": "http-in",
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15},
            },
        ],
        "outbounds": [],
    }
    for i, srv in enumerate(servers):
        ob = _xray_outbound(srv)
        if ob:
            ob["tag"] = srv.get("tag", f"server-{i}")
            cfg["outbounds"].append(ob)
    tag = servers[active_idx].get("tag", f"server-{active_idx}") if servers else "direct"
    cfg["outbounds"].append({"protocol": "freedom", "tag": "direct", "streamSettings": {"sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15}}})
    cfg["routing"] = {
        "domainStrategy": "IPIfNonMatch",
        "rules": [
            {"type": "field", "port": 53, "network": "udp", "outboundTag": tag},
            {"type": "field", "inboundTag": ["socks-in", "http-in"], "outboundTag": tag},
            # Telegram MTProto / QUIC: matched by IP, routed through the proxy
            # WITHOUT going through slow sniffing fallbacks.
            {"type": "field", "ip": ["91.108.0.0/16", "149.154.160.0/20", "185.76.151.0/24"], "outboundTag": tag},
        ],
    }
    return cfg


def _normalize_fp(fp: str) -> str:
    normalized = fp.strip().lower()
    if normalized in ("randomized", "random", "ios", "firefox", "edge", "safari", "disabled", "none", ""):
        return "chrome"
    return fp


def _xray_outbound(srv: dict) -> Optional[dict]:
    stype = srv.get("type", "")
    host = srv.get("hostname", "")
    port = int(srv.get("port", 0))

    if stype == "vless":
        net_type = srv.get("network", "tcp")
        if net_type == "http":
            log("[VLESS patch] Correcting network type 'http' to 'tcp' for Reality.")
            net_type = "tcp"
        security_str = srv.get("security", "none")
        flow_str = srv.get("flow", "")
        sni_str = srv.get("sni", "")
        if not sni_str:
            log("[SNI patch] Empty SNI, keeping hostname")
            sni_str = srv.get("hostname", "")

        pbk_str = srv.get("pbk", "")
        fp_str = _normalize_fp(srv.get("fp", "chrome"))
        spx_str = srv.get("spx", "")
        sid_str = srv.get("sid", "")
        path_str = srv.get("path", "/")
        host_str = srv.get("hostname", "")

        log(f"[VLESS outbound] uuid={srv.get('uuid','')} host={host_str} port={srv.get('port',0)} net={net_type} sec={security_str} flow={flow_str} sni={sni_str} pbk={pbk_str} fp={fp_str} sid={sid_str} spx={spx_str} path={path_str}")

        user_spec = {"id": srv.get("uuid", ""), "encryption": "none"}
        if security_str == "reality" and flow_str:
            user_spec["flow"] = flow_str

        ob: dict = {
            "protocol": "vless",
            "settings": {
                "vnext": [{
                    "address": host_str,
                    "port": int(srv.get("port", 0)),
                    "users": [user_spec],
                }]
            },
            "streamSettings": {
                "network": net_type if net_type else "tcp",
                "security": "reality" if security_str == "reality" else "none",
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 15},
            },
        }

        if security_str == "reality":
            ob["streamSettings"]["realitySettings"] = {
                "show": False,
                "fingerprint": fp_str if fp_str else "chrome",
                "serverName": sni_str if sni_str else host_str,
                "publicKey": pbk_str,
                "shortId": sid_str if sid_str else "",
                "spiderX": spx_str if spx_str else "",
            }

        if net_type == "xhttp":
            xpadding = srv.get("x_padding_bytes", "") or "100-1000"
            ob["streamSettings"]["xhttpSettings"] = {
                "mode": "auto",
                "path": path_str if path_str else "/",
                "extra": {
                    "mode": "auto",
                    "xPaddingBytes": xpadding,
                    "xmux": {
                        "maxConnections": 16,
                        "hMaxRequestTimes": "800-900",
                        "hMaxReusableSecs": "1000-2000",
                    },
                },
            }

        ob["mux"] = {"enabled": False}
        return ob

    if stype == "vmess":
        ob: dict = {
            "protocol": "vmess",
            "settings": {
                "vnext": [{
                    "address": host,
                    "port": port,
                    "users": [{"id": srv.get("uuid", ""), "security": "auto"}],
                }]
            },
            "streamSettings": {
                "network": srv.get("network", "tcp"),
                "security": srv.get("security", "none"),
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15},
            },
        }
        return ob

    if stype == "trojan":
        ob: dict = {
            "protocol": "trojan",
            "settings": {
                "servers": [{"address": host, "port": port, "password": srv.get("password", "")}],
            },
            "streamSettings": {
                "network": "tcp",
                "security": srv.get("security", "tls"),
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15},
            },
        }
        if srv.get("sni"):
            ob["streamSettings"]["tlsSettings"] = {"serverName": srv["sni"]}
        return ob

    if stype == "ss":
        return {
            "protocol": "shadowsocks",
            "settings": {
                "servers": [{"address": host, "port": port, "method": srv.get("method", "chacha20-ietf-poly1305"), "password": srv.get("password", ""), "level": 0}],
            },
            "streamSettings": {
                "network": "tcp",
                "security": "none",
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15},
            },
        }

    return None


# --- Outbound apply helpers (anti-freeze + state persistence) ---

def _xray_cfg_with_outbound(ob: dict) -> dict:
    return {
        "log": {"loglevel": "warning"},
        "dns": {
            "servers": ["https://dns.google/dns-query", "https://cloudflare-dns.com/dns-query", "8.8.8.8", "1.1.1.1"],
            "queryStrategy": "UseIPv4",
        },
        "inbounds": [
            {
                "port": 6357, "listen": "0.0.0.0", "protocol": "socks",
                "settings": {"auth": "noauth", "udp": True, "ip": "127.0.0.1"},
                "sniffing": {"enabled": True, "destOverride": ["http", "tls", "quic"], "routeOnly": True},
                "tag": "socks-in",
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15, "tcpUserTimeout": 15000},
            },
            {
                "port": 6358, "listen": "0.0.0.0", "protocol": "http", "tag": "http-in",
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15},
            },
        ],
        "outbounds": [ob, {"protocol": "freedom", "tag": "direct", "streamSettings": {"sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15}}}],
        "routing": {
            "domainStrategy": "IPIfNonMatch",
            "rules": [
                {"type": "field", "port": 53, "network": "udp", "outboundTag": ob.get("tag", "proxy")},
                {"type": "field", "inboundTag": ["socks-in", "http-in"], "outboundTag": ob.get("tag", "proxy")},
                # Telegram MTProto / QUIC IP ranges bypass sniffing fallbacks.
                {"type": "field", "ip": ["91.108.0.0/16", "149.154.160.0/20", "185.76.151.0/24"], "outboundTag": ob.get("tag", "proxy")},
            ],
        },
    }


def _singbox_cfg_with_outbound(ob: dict) -> dict:
    return {
        "log": {"level": "warn"},
        "dns": {
            "servers": [
                {"tag": "resolver", "address": "https://1.1.1.1/dns-query", "detour": "direct", "strategy": "ipv4_only"},
                {"tag": "block", "address": "rcode://success"},
            ],
            "final": "resolver",
            "independent_cache": True,
        },
        "inbounds": [
            {
                "type": "socks", "tag": "socks-in", "listen": "0.0.0.0", "listen_port": 6357,
                "sniff": {"enabled": True, "override_destination": False, "route_only": True},
            },
            {
                "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358,
                "sniff": {"enabled": True, "override_destination": True, "route_only": True},
            },
        ],
        "outbounds": [ob, {"type": "direct", "tag": "direct", "tcp_keep_alive": "5m", "tcp_keep_alive_interval": "15s"}],
        "route": {
            "final": ob.get("tag", "proxy"),
            "auto_detect_interface": True,
        },
        "experimental": {"cache_file": {"enabled": True}},
    }


def _apply_outbound_cfg(engine: str, ob: dict) -> dict:
    acquire_apply_lock()
    try:
        if engine == "xray":
            _docker(["stop", "singbox-node"])
            cfg = _xray_cfg_with_outbound(ob)
            with open(XRAY_CONFIG, "w", encoding="utf-8") as f:
                json.dump(cfg, f, indent=2, ensure_ascii=False)
            log("Xray config: " + json.dumps(cfg, indent=2, ensure_ascii=False)[:600])
            _docker(["restart", "xray-node"])
        else:
            _docker(["stop", "xray-node"])
            cfg = _singbox_cfg_with_outbound(ob)
            with open(SINGBOX_CONFIG, "w", encoding="utf-8") as f:
                json.dump(cfg, f, indent=2, ensure_ascii=False)
            log("Singbox config: " + json.dumps(cfg, indent=2, ensure_ascii=False)[:600])
            _ensure_xray_running()
            _docker(["restart", "singbox-node"])
        return cfg
    finally:
        release_apply_lock()


def restore_active_vpn() -> None:
    state = load_state()
    engine = state.get("active_engine", "")
    server_name = state.get("active_server", "")
    url = state.get("active_url", "")
    if not engine or not server_name:
        log("No active VPN in state, keeping current config")
        return
    if not url:
        for s in load_cache():
            if s.get("name") == server_name or s.get("tag") == server_name or s.get("id") == server_name:
                url = s.get("url", "")
                break
    if not url:
        log(f"Active VPN '{server_name}' has no saved URL, cannot restore (select it again via Option 3)")
        return
    log(f"Restoring active VPN: {server_name} ({engine})")
    outbound_engine, ob = parse_url_to_outbound(url, engine=engine)
    if outbound_engine != engine:
        log(f"URL protocol {outbound_engine} doesn't match saved engine {engine}, using URL parsing result anyway")
        engine = outbound_engine
    save_rollback(engine)
    _apply_outbound_cfg(engine, ob)
    log(f"VPN config restored: {server_name}")


def build_singbox_config(servers: list, active_idx: int = 0) -> dict:
    cfg = {
        "log": {"level": "warn"},
        "dns": {
            "servers": [
                {"tag": "resolver", "address": "https://1.1.1.1/dns-query", "detour": "direct", "strategy": "ipv4_only"},
                {"tag": "block", "address": "rcode://success"},
            ],
            "final": "resolver",
            "independent_cache": True,
        },
        "inbounds": [
            {
                "type": "socks", "tag": "socks-in", "listen": "0.0.0.0", "listen_port": 6357,
                "sniff": {"enabled": True, "override_destination": False, "route_only": True},
            },
            {
                "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358,
                "sniff": {"enabled": True, "override_destination": True, "route_only": True},
            },
        ],
        "outbounds": [],
    }
    for i, srv in enumerate(servers):
        ob = _singbox_outbound(srv)
        if ob:
            ob["tag"] = srv.get("tag", f"server-{i}")
            cfg["outbounds"].append(ob)
    tag = servers[active_idx].get("tag", f"server-{active_idx}") if servers else "direct"
    cfg["route"] = {
        "final": tag,
        "auto_detect_interface": True,
    }
    cfg["experimental"] = {"cache_file": {"enabled": True}}
    cfg["outbounds"].append({"type": "direct", "tag": "direct", "tcp_keep_alive": "5m", "tcp_keep_alive_interval": "15s"})
    return cfg


def _singbox_outbound(srv: dict) -> Optional[dict]:
    host = srv.get("hostname", "")
    port = int(srv.get("port", 0) or 0)
    if not host or port <= 0:
        return None
    proto = srv.get("protocol", srv.get("type", "")).lower()
    if proto in ("hy2",):
        proto = "hysteria2"

    ob: dict = {
        "type": proto,
        "server": host,
        "server_port": port,
        # Socket hardening for all proxy outbounds: TCP keepalive keeps NAT
        # mappings and idle tunnels alive through transient link dropouts.
        # (sing-box >= 1.13; tcp_no_delay is removed there - Go enables
        # TCP_NODELAY by default.)
        "tcp_keep_alive": "5m",
        "tcp_keep_alive_interval": "15s",
    }

    if proto == "hysteria2":
        ob["password"] = srv.get("password", "")
        ob["domain_strategy"] = "ipv4_only"
        ob["tls"] = {
            "enabled": True,
            "server_name": srv.get("sni", "") or host,
            "insecure": bool(srv.get("insecure", False)),
        }
        obfs_type = srv.get("obfs", "")
        obfs_pass = srv.get("obfs-password", "")
        if obfs_type and obfs_pass:
            ob["obfs"] = {"type": obfs_type, "password": obfs_pass}
        return ob

    if proto == "tuic":
        ob["uuid"] = srv.get("uuid", "")
        ob["password"] = srv.get("password", "")
        ob["congestion_control"] = srv.get("congestion_control", "bbr")
        ob["udp_relay_mode"] = srv.get("udp_relay_mode", "native")
        ob["domain_strategy"] = "ipv4_only"
        ob["tls"] = {
            "enabled": True,
            "server_name": srv.get("sni", "") or host,
            "insecure": bool(srv.get("insecure", False)),
        }
        alpn = srv.get("alpn", "")
        if alpn:
            ob["tls"]["alpn"] = [x.strip() for x in alpn.split(",") if x.strip()]
        return ob

    if proto == "wireguard":
        ob["domain_strategy"] = "ipv4_only"
        ob["private_key"] = srv.get("private_key", "")
        ob["server_port"] = port
        local_addr = srv.get("allowed_ips", "") or "10.0.0.1/32"
        ob["local_address"] = [x.strip() for x in local_addr.split(",") if x.strip()] or ["10.0.0.1/32"]
        peer_pub = srv.get("public_key", "") or srv.get("peer_public_key", "")
        if peer_pub:
            ob["peer_public_key"] = peer_pub
        reserved = srv.get("reserved")
        if reserved:
            ob["reserved"] = reserved
        mtu = srv.get("mtu")
        if mtu:
            ob["mtu"] = mtu
        return ob

    if proto == "ss":
        ob["method"] = srv.get("method", "chacha20-ietf-poly1305")
        ob["password"] = srv.get("password", "")
        ob["domain_strategy"] = "ipv4_only"
        return ob

    if proto not in ("vless", "vmess", "trojan"):
        return None

    net_type = srv.get("network", "tcp")
    if net_type == "http":
        net_type = "tcp"
    if net_type == "xhttp":
        log(f"[singbox] {host} uses xhttp transport - sing-box cannot handle, xray fallback required")
        return None

    ob["domain_strategy"] = "ipv4_only"

    flow_val = srv.get("flow", "")
    if proto == "vless":
        ob["uuid"] = srv.get("uuid", "")
        if flow_val:
            ob["flow"] = flow_val
    elif proto == "vmess":
        ob["uuid"] = srv.get("uuid", "")
    elif proto == "trojan":
        ob["password"] = srv.get("password", "")

    security_str = srv.get("security", "tls")
    if security_str in ("reality", "tls") or srv.get("sni") or proto == "trojan":
        tls_conf: dict = {
            "enabled": True,
            "server_name": srv.get("sni", "") or host,
        }
        if proto == "vless" and security_str == "reality":
            tls_conf["utls"] = {
                "enabled": True,
                "fingerprint": _normalize_fp(srv.get("fp", "chrome")),
            }
            tls_conf["reality"] = {
                "enabled": True,
                "public_key": srv.get("pbk", ""),
                "short_id": srv.get("sid", ""),
            }
        else:
            tls_conf["insecure"] = bool(srv.get("insecure", False))
        alpn = srv.get("alpn", "")
        if alpn:
            tls_conf["alpn"] = [x.strip() for x in alpn.split(",") if x.strip()]
        ob["tls"] = tls_conf

    if net_type == "ws":
        ws: dict = {"type": "ws", "path": srv.get("path", "/")}
        host_hdr = srv.get("host", "")
        if host_hdr:
            ws["headers"] = {"Host": host_hdr}
        ob["transport"] = ws
    elif net_type == "grpc":
        grpc: dict = {"type": "grpc", "service_name": srv.get("path", "/") or "/"}
        host_hdr = srv.get("host", "")
        if host_hdr:
            grpc["authority"] = host_hdr
        ob["transport"] = grpc

    ob["multiplex"] = {"enabled": False}
    return ob


# --- URL > Outbound (pure urllib.parse) ---

def _url_to_srv(scheme: str, user_info: str, host: str, port: int, params: dict, tag: str) -> dict:
    srv: dict = {
        "type": scheme,
        "protocol": scheme,
        "hostname": host,
        "port": port,
        "tag": tag,
        "full_link": "",
    }
    if scheme in ("vless", "vmess"):
        srv["uuid"] = user_info
        net_type = params.get("type", "tcp")
        if net_type == "http":
            net_type = "tcp"
        srv["network"] = net_type
        srv["security"] = params.get("security", "reality" if scheme == "vless" else "none")
        srv["flow"] = params.get("flow", "")
        srv["fp"] = params.get("fp", "chrome")
        srv["pbk"] = params.get("pbk", "")
        srv["sid"] = params.get("sid", "")
        srv["sni"] = params.get("sni", "") or host
        srv["spx"] = params.get("spx", "")
        srv["path"] = params.get("path", "/")
        srv["host"] = params.get("host", "")
        srv["alpn"] = params.get("alpn", "")
        srv["mode"] = params.get("mode", "auto")
        srv["encryption"] = params.get("encryption", "none")
        srv["insecure"] = params.get("insecure", "0") == "1"
        srv["x_padding_bytes"] = params.get("x_padding_bytes", "")
        srv["engine"] = "xray" if (scheme == "vless" and net_type == "xhttp") else "singbox"
    elif scheme == "trojan":
        srv["password"] = user_info
        srv["sni"] = params.get("sni", params.get("peer", "")) or host
        srv["security"] = params.get("security", "tls")
        srv["network"] = params.get("type", "tcp")
        srv["path"] = params.get("path", "/")
        srv["host"] = params.get("host", "")
        srv["alpn"] = params.get("alpn", "")
        srv["insecure"] = params.get("insecure", "0") == "1"
    elif scheme == "ss":
        raw = user_info or ""
        try:
            decoded = base64.b64decode(raw + "=" * (-len(raw) % 4)).decode(errors="replace")
            if ":" in decoded:
                srv["method"], srv["password"] = decoded.split(":", 1)
            else:
                srv["password"] = raw
                srv["method"] = "chacha20-ietf-poly1305"
        except Exception:
            srv["password"] = raw
            srv["method"] = "chacha20-ietf-poly1305"
        srv["plugin"] = params.get("plugin", "")
        srv["network"] = params.get("type", "tcp")
    elif scheme in ("hysteria2", "hy2"):
        srv["password"] = user_info
        srv["sni"] = params.get("sni", "") or host
        srv["insecure"] = params.get("insecure", "1") == "1"
        srv["obfs"] = params.get("obfs", "")
        srv["obfs-password"] = params.get("obfs-password", "")
        srv["protocol"] = "hysteria2"
    elif scheme == "tuic":
        srv["uuid"] = user_info
        srv["password"] = params.get("password", "")
        srv["sni"] = params.get("sni", "") or host
        srv["congestion_control"] = params.get("congestion_control", "bbr")
        srv["udp_relay_mode"] = params.get("udp_relay_mode", "native")
        srv["alpn"] = params.get("alpn", "")
        srv["insecure"] = params.get("insecure", "0") == "1"
    elif scheme == "wireguard":
        srv["private_key"] = params.get("private_key", "") or user_info
        srv["public_key"] = params.get("public_key", "") or params.get("peer_public_key", "")
        srv["endpoint"] = params.get("endpoint", "") or f"{host}:{port}"
        srv["allowed_ips"] = params.get("allowed_ips", "")
        try:
            srv["mtu"] = int(params.get("mtu", ""))
        except (ValueError, TypeError):
            srv["mtu"] = 0
        reserved_raw = params.get("reserved", "")
        if reserved_raw:
            try:
                srv["reserved"] = [int(x) for x in reserved_raw.split(",")]
            except Exception:
                srv["reserved"] = []
    return srv


def parse_url_to_outbound(url_str: str, engine: str = "xray") -> Tuple[str, dict]:
    if not url_str:
        return "xray", {"protocol": "freedom", "tag": "direct"}
    try:
        parsed = urllib.parse.urlparse(url_str)
    except Exception:
        return "xray", {"protocol": "freedom", "tag": "direct"}
    scheme = parsed.scheme
    netloc = parsed.netloc
    user_info = netloc.split('@')[0] if '@' in netloc else ""
    host_port = netloc.split('@')[-1] if '@' in netloc else netloc
    host = host_port.split(':')[0] if ':' in host_port else host_port
    port = int(host_port.split(':')[1]) if ':' in host_port else 443
    qs = urllib.parse.parse_qs(parsed.query)
    params = {k: v[0] for k, v in qs.items()}
    tag = urllib.parse.unquote(parsed.fragment) if parsed.fragment else "proxy"

    if engine == "singbox":
        srv = _url_to_srv(scheme, user_info, host, port, params, tag)
        ob = _singbox_outbound(srv)
        if ob is None:
            log(f"[singbox] No sing-box outbound for {scheme}, falling back to xray")
            return parse_url_to_outbound(url_str, engine="xray")
        ob["tag"] = tag
        return "singbox", ob

    if scheme == "vless":
        uuid_val = user_info
        encryption = params.get("encryption", "none")
        flow_val = params.get("flow", "")
        net_type = params.get("type", "tcp")
        if net_type == "http":
            net_type = "tcp"
        security = params.get("security", "none")
        pbk = params.get("pbk", "")
        sid = params.get("sid", "")
        sni = params.get("sni", host)
        if not sni:
            log("[SNI patch] Empty SNI, keeping hostname")
            sni = host
        fp = params.get("fp", "chrome")
        path = params.get("path", "/")

        user_obj = {"id": uuid_val, "encryption": encryption}
        if security == "reality" and flow_val:
            user_obj["flow"] = flow_val

        outbound: dict = {
            "protocol": "vless",
            "tag": tag,
            "settings": {
                "vnext": [{
                    "address": host,
                    "port": port,
                    "users": [user_obj],
                }]
            },
            "streamSettings": {
                "network": net_type,
                "security": security,
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 15},
            },
        }

        if security == "reality":
            spx_str = urllib.parse.unquote(params.get("spx", ""))
            outbound["streamSettings"]["realitySettings"] = {
                "show": False,
                "fingerprint": _normalize_fp(fp),
                "serverName": sni,
                "publicKey": pbk,
                "shortId": sid,
                "spiderX": spx_str,
            }

        if net_type == "xhttp":
            xpadding = params.get("x_padding_bytes", "") or "100-1000"
            outbound["streamSettings"]["xhttpSettings"] = {
                "mode": params.get("mode", "auto"),
                "path": path,
                "extra": {
                    "mode": params.get("mode", "auto"),
                    "xPaddingBytes": xpadding,
                    "xmux": {
                        "maxConnections": 16,
                        "hMaxRequestTimes": "800-900",
                        "hMaxReusableSecs": "1000-2000",
                    },
                },
            }

        outbound["mux"] = {"enabled": False}

        return "xray", outbound

    elif scheme in ("hysteria2", "hy2"):
        password = user_info
        sni = params.get("sni", host)
        obfs_type = params.get("obfs", "")
        obfs_pass = params.get("obfs-password", "")
        insecure_val = params.get("insecure", "1") == "1"

        outbound: dict = {
            "type": "hysteria2",
            "tag": tag,
            "server": host,
            "server_port": port,
            "password": password,
            "domain_strategy": "ipv4_only",
            "tls": {
                "enabled": True,
                "server_name": sni,
                "insecure": insecure_val,
            },
        }
        if obfs_type:
            outbound["obfs"] = {"type": obfs_type, "password": obfs_pass}

        return "singbox", outbound

    return "xray", {"protocol": "freedom", "tag": "direct"}


# --- Docker Control ---

def _docker(args: list, timeout: int = 30) -> int:
    """Run a docker command with a hard timeout. Returns exit code (or -1)."""
    try:
        r = subprocess.run(["docker"] + args, capture_output=True, timeout=timeout)
        if r.returncode != 0:
            err = r.stderr.decode(errors="replace").strip()
            if err:
                log(f"docker {' '.join(args)}: {err[:300]}")
        return r.returncode
    except subprocess.TimeoutExpired:
        log(f"docker {' '.join(args)}: timed out after {timeout}s")
        return -1
    except Exception as e:
        log(f"docker {' '.join(args)}: {e}")
        return -1


def _docker_output(args: list, timeout: int = 30) -> str:
    try:
        r = subprocess.run(["docker"] + args, capture_output=True, timeout=timeout)
        return r.stdout.decode(errors="replace").strip()
    except Exception:
        return ""


_ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")
_ANSI_OSC_RE = re.compile(r"\x1b\][^\x07]*(?:\x07|\x1b\\)")
_CONTROL_RE = re.compile(r"[\x00-\x08\x0b\x0c\x0e-\x1f]")


def _clean_log_text(text: str) -> str:
    """Strip ANSI escape sequences and stray control chars from docker logs.

    `docker logs` output is full of terminal color codes (e.g. `\x1b[0m`) and
    binary noise that bloats JSON payloads and can break display/serialization.
    """
    text = _ANSI_OSC_RE.sub("", text)
    text = _ANSI_RE.sub("", text)
    text = _CONTROL_RE.sub("", text)
    return text


def _docker_logs(container: str, tail: int = 200) -> str:
    """Fetch the tail of a container's logs (stdout+stderr combined).

    Resolves the container dynamically because docker-compose renames services
    into e.g. `fleet-agent-xray-node-1` (project-scoped). `docker logs xray-node`
    would then fail, so we look up the real container id by name filter first.
    The output is cleaned of ANSI codes and decoded as UTF-8 with errors
    ignored so JSON serialization never truncates or fails.
    """
    target = _resolve_container_id(container)
    if not target:
        return f"(container '{container}' not found - is it running?)"
    try:
        r = subprocess.run(
            ["docker", "logs", target, "--tail", str(tail), "--timestamps"],
            capture_output=True, timeout=30,
        )
        out = r.stdout.decode("utf-8", errors="ignore")
        err = r.stderr.decode("utf-8", errors="ignore")
        combined = _clean_log_text((out + err)).strip()
        if combined:
            return combined
        return f"(container '{container}' has no log output)"
    except subprocess.TimeoutExpired:
        return f"(docker logs {container} timed out)"
    except Exception as e:
        return f"(failed to fetch logs for {container}: {e})"


def _resolve_container_id(name: str) -> str:
    """Find the running container id for a service name.

    Docker-compose renames services to `<project>-<service>-<counter>` (e.g.
    `fleet-agent-xray-node-1`). We search by name filter to match both the bare
    alias and the compose-prefixed variant. Returns the container id, or ''.
    """
    candidates = []
    filters = [
        f"name={name}",
        f"name={name}-",       # prefix match for compose projects
        f"name=-{name}-",      # e.g. fleet-agent-xray-node-1
        f"name=-{name}-1",
    ]
    for f in filters:
        try:
            out = _docker_output(["ps", "-q", "-f", f])
        except Exception:
            continue
        if out:
            candidates.extend(out.splitlines())
            if f == f"name={name}":
                break
    # Prefer an exact running match.
    for cid in candidates:
        if not cid:
            continue
        if _docker_output(["inspect", "-f", "{{.Name}}", cid]) in (f"/{name}", f"/{name}-1"):
            return cid
    if candidates:
        return candidates[0]
    # Fall back to `docker compose ps -q <service>` for compose v2.
    try:
        out = _docker_output(["compose", "ps", "-q", name], timeout=10)
        if out:
            return out.split()[0]
    except Exception:
        pass
    return ""


def is_container_running(engine: str) -> bool:
    name = "singbox-node" if engine == "singbox" else "xray-node"
    return _docker_output(["inspect", "-f", "{{.State.Running}}", name]) == "true"


# --- Apply lock: serialize config writes + container restarts between the
# --- daemon process and CLI invocations (docker exec) running in parallel.

_APPLY_LOCK: Any = None


def acquire_apply_lock() -> bool:
    global _APPLY_LOCK
    if fcntl is None:  # type: ignore[union-attr]
        return True
    try:
        if _APPLY_LOCK is None:
            _APPLY_LOCK = open(APPLY_LOCK_FILE, "w")
        fcntl.flock(_APPLY_LOCK, fcntl.LOCK_EX)  # type: ignore[union-attr]
        return True
    except Exception as e:
        log(f"Could not acquire apply lock: {e}")
        return False


def release_apply_lock() -> None:
    global _APPLY_LOCK
    if fcntl is None or _APPLY_LOCK is None:  # type: ignore[union-attr]
        return
    try:
        fcntl.flock(_APPLY_LOCK, fcntl.LOCK_UN)  # type: ignore[union-attr]
        _APPLY_LOCK.close()  # type: ignore[union-attr]
    except Exception as e:
        log(f"Could not release apply lock: {e}")
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


def docker_restart(container: str) -> None:
    if container == "singbox-node":
        _ensure_xray_running()
    rc = _docker(["restart", container])
    if rc != 0:
        logs = _docker_output(["logs", container, "--tail", "20"])
        log(f"{container} logs: {logs}")
    else:
        log(f"Restarted {container}")


# --- Singbox prerequisite helper ---

def _ensure_xray_running() -> bool:
    if _docker_output(["inspect", "-f", "{{.State.Running}}", "xray-node"]) == "true":
        return True
    log("xray-node not running, starting with dummy config for singbox prerequisite")
    dummy = {"log": {"loglevel": "warning"}, "inbounds": [{"port": 9999, "listen": "127.0.0.1", "protocol": "socks", "settings": {"auth": "noauth"}}], "outbounds": [{"protocol": "freedom", "tag": "direct"}]}
    with open(XRAY_CONFIG, "w", encoding="utf-8") as f:
        json.dump(dummy, f, indent=2)
    _docker(["stop", "singbox-node"])
    if _docker(["start", "xray-node"]) != 0:
        _docker(["restart", "xray-node"])
    time.sleep(2)
    log("xray-node started (dummy config)")
    return False


# --- Health Check (Docker + TCP socket) ---

def test_proxy() -> Tuple[bool, str]:
    state = load_agent_state()
    engine = state.get("active_engine", "xray") if state else "xray"
    container = "singbox-node" if engine == "singbox" else "xray-node"
    if _docker_output(["inspect", "-f", "{{.State.Running}}", container]) != "true":
        return False, "Container not running"
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


# --- Apply / Rollback ---

def apply_configs(engine: str, servers: list, active_idx: int = 0) -> bool:
    if not servers:
        log(f"No servers for {engine}, keeping existing config")
        return False
    acquire_apply_lock()
    try:
        if engine == "xray":
            _docker(["stop", "singbox-node"])
            cfg = build_xray_config(servers, active_idx)
            with open(XRAY_CONFIG, "w", encoding="utf-8") as f:
                json.dump(cfg, f, indent=2, ensure_ascii=False)
            log(f"Wrote xray config ({len(cfg['outbounds'])} outbounds)")
            log("Xray config: " + json.dumps(cfg, indent=2, ensure_ascii=False)[:600])
            _docker(["restart", "xray-node"])
        else:
            _docker(["stop", "xray-node"])
            cfg = build_singbox_config(servers, active_idx)
            with open(SINGBOX_CONFIG, "w", encoding="utf-8") as f:
                json.dump(cfg, f, indent=2, ensure_ascii=False)
            log(f"Wrote singbox config ({len(cfg['outbounds'])} outbounds)")
            log("Singbox config: " + json.dumps(cfg, indent=2, ensure_ascii=False)[:600])
            _ensure_xray_running()
            _docker(["restart", "singbox-node"])
    finally:
        release_apply_lock()
    time.sleep(3)
    ok, status = test_proxy()
    if not ok:
        log(f"Proxy down after applying {engine}, rolling back...")
        if restore_rollback(engine):
            docker_restart(f"{engine}-node")
            log("Rolled back to previous config")
        else:
            log("No rollback available")
        return False
    log(f"Proxy verified after apply, IP: {status}")
    return True


def rollback_engine(engine: str) -> bool:
    if restore_rollback(engine):
        docker_restart(f"{engine}-node")
        return True
    log(f"No rollback available for {engine}")
    return False


# --- Subscription Update ---

def update_subscription() -> bool:
    """Fetch + apply the subscription. Returns True if a config was applied."""
    state = load_state()
    sub_url = state.get("sub_url", "")
    if not sub_url:
        log("No sub_url configured, skipping subscription fetch")
        return False

    log(f"Fetching subscription from {sub_url}")
    servers = parse_subscription(sub_url)
    if not servers:
        log("No servers found in subscription")
        save_cache([])
        return False

    save_cache(servers)

    xray_servers = [s for s in servers if s.get("engine") == "xray"]
    singbox_servers = [s for s in servers if s.get("engine") == "singbox"]

    log(f"Successfully parsed {len(servers)} servers from subscription")

    state = load_state()
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
        log(f"Active server '{active_name}' no longer in subscription, falling back to '{fallback_name}'")
        state["active_server"] = fallback_name
        if fallback.get("full_link"):
            state["active_url"] = fallback["full_link"]
        save_state(state)
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
            log(f"Active server '{active_name}' not in {engine} subset, applying fallback '{fallback_name}'")
            state["active_server"] = fallback_name
            if fallback.get("full_link"):
                state["active_url"] = fallback["full_link"]
            save_state(state)
            idx = 0
        save_rollback(engine)
        return apply_configs(engine, subset, active_idx=idx)

    applied = False
    if current_engine == "xray" and xray_servers:
        applied = _apply_engine("xray", xray_servers)
    elif current_engine == "singbox" and singbox_servers:
        applied = _apply_engine("singbox", singbox_servers)
    elif xray_servers:
        log("No xray servers found for current engine, switching to xray")
        save_rollback("xray")
        apply_configs("xray", xray_servers)
        state["active_engine"] = "xray"
        applied = True
    elif singbox_servers:
        log("No singbox servers found for current engine, switching to singbox")
        save_rollback("singbox")
        apply_configs("singbox", singbox_servers)
        state["active_engine"] = "singbox"
        applied = True

    tags = [s.get("tag", "") for s in servers]
    report(
        external_ip=_lan_ip(),
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
        save_state(state)
    return applied


def fetch_subscription_now(url: Optional[str] = None) -> int:
    if not url:
        state = load_state()
        url = state.get("sub_url")
    if not url:
        log("No sub_url configured, cannot fetch subscription")
        return 0
    state = load_state()
    state["sub_url"] = url
    save_state(state)
    log(f"Fetching subscription from: {url}")
    try:
        resp = requests.get(url, headers={"User-Agent": SUB_USER_AGENT}, verify=False, timeout=15)
        log(f"HTTP status: {resp.status_code}, len: {len(resp.text)}")
        if resp.status_code != 200:
            log(f"Subscription fetch returned {resp.status_code}")
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
                srv = _parse_link(line)
                if srv:
                    servers.append(srv)
            except Exception:
                continue
        log(f"Successfully parsed {len(servers)} servers from subscription")
        save_cache(servers)
        log("Subscription saved. Use Option 3 to select a server.")
        return len(servers)
    except Exception as e:
        log(f"Error fetching subscription: {e}")
        log(traceback.format_exc())
        return 0


def select_server(idx: int, mode: str = "manual") -> int:
    servers = load_cache()
    if idx < 0 or idx >= len(servers):
        log(f"Invalid server index {idx}, have {len(servers)} servers")
        return 1
    srv = servers[idx]
    name = srv.get("name", f"Server {idx + 1}")
    engine = srv.get("engine", "xray")
    url = srv.get("url", "")
    log(f"Selecting server {idx + 1}: {name} ({engine})")
    if not url:
        log(f"No URL for server {idx + 1}, cannot build config")
        return 1

    host = srv.get("host", "")
    port = int(srv.get("port", 0) or 0)
    if host and port > 0 and not _probe_host(host, port):
        log(f"Server {name} ({host}:{port}) unreachable, keeping current selection")
        report(status="Switch failed", message=f"{name} unreachable, kept current server")
        return 1

    outbound_engine, ob = parse_url_to_outbound(url, engine=engine)
    if outbound_engine != engine:
        log(f"URL protocol {outbound_engine} doesn't match cached engine {engine}, using URL parsing result anyway")
        engine = outbound_engine

    save_rollback(engine)
    cfg = _apply_outbound_cfg(engine, ob)

    state = load_state()
    state["active_server"] = name
    state["active_engine"] = engine
    state["active_proto"] = srv.get("proto", "")
    state["active_url"] = url
    state["active_mode"] = mode
    state["last_seen"] = time.strftime("%Y-%m-%d %H:%M:%S")
    save_state(state)

    log(f"Switched to {name}")
    report(engine=engine, protocol=srv.get("proto", ""), status="Verified & Active", message=f"Switched to {name}")
    return 0


def benchmark_servers(probes: int = BENCH_PROBES, timeout: float = BENCH_TIMEOUT, progress: Optional[Callable[[int, str, str], None]] = None) -> dict:
    """TCP-probe every cached server and measure latency / jitter / loss.

    For UDP-only protocols (hysteria2/tuic/wireguard) a TCP 'connection
    refused' still proves the host is up and yields a valid RTT sample.
    Returns {cache_idx: {"latency_ms","jitter_ms","loss_pct","ok"}}.
    `progress(idx, name, line)` is called as each server finishes.
    """
    servers = load_cache()
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
            log(f"[bench] {host} resolve failed: {e}")
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
        with open(BENCH_FILE, encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return {}


def save_benchmark(results: dict, mode: str) -> None:
    try:
        with open(BENCH_FILE, "w", encoding="utf-8") as f:
            json.dump({"ts": time.time(), "mode": mode, "results": results}, f, indent=2)
        log(f"Benchmark cached for {mode} mode")
    except Exception as e:
        log(f"Failed to cache benchmark: {e}")


def get_benchmark(max_age: int = BENCH_TTL) -> Tuple[dict, float, bool]:
    """Return (results, ts, fresh). Fresh = cached within max_age seconds."""
    data = load_benchmark_cache()
    ts = float(data.get("ts", 0))
    if ts and time.time() - ts <= max_age:
        return data.get("results", {}), ts, True
    return {}, ts, False


def print_benchmark(results: dict) -> None:
    servers = load_cache()
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
        log(f"Unknown selection mode '{mode}'")
        return 1
    servers = load_cache()
    if not servers:
        log("No servers in cache, cannot auto-select")
        return 1

    results, ts, fresh = get_benchmark()
    if fresh:
        log(f"Using cached benchmark ({int(time.time() - ts)}s old)")
    else:
        log(f"Benchmarking {len(servers)} servers ({mode})...")
        results = benchmark_servers(progress=_bench_progress)
        save_benchmark(results, mode)
    print_benchmark(results)

    state = load_state()
    active = state.get("active_server", "")
    best = _switch_target(results, servers, active, mode)
    if best is None:
        log(f"{mode.capitalize()} mode: keeping current server '{active}' (no alternative at least 25% better)")
        state["active_mode"] = mode
        save_state(state)
        return 0

    if mode == "fastest":
        log(f"Fastest mode: server {best + 1} ({servers[best].get('name', '')}) at {results[str(best)]['latency_ms']} ms")
    else:
        log(f"Balanced mode: server {best + 1} ({servers[best].get('name', '')}) loss={results[str(best)]['loss_pct']:.0f}% jitter={results[str(best)]['jitter_ms']} ms")

    rc = select_server(best, mode=mode)
    if rc == 0:
        log(f"Active selection mode saved: {mode}")
    return rc


def print_server_list() -> None:
    servers = load_cache()
    if not servers:
        print("No servers in cache. Use Option 1 to update subscription first.")
        return
    print()
    print("Available VPN Servers:")
    print()
    for idx, srv in enumerate(servers):
        name = srv.get("name", f"Server {idx + 1}")
        proto = srv.get("proto", "unknown")
        engine = srv.get("engine", "xray")
        print(f" {idx + 1:2d}) {name:<35s} {proto:<10s} ({engine})")
    print()
    print(f"Total: {len(servers)} servers")
    print()


def health_loop() -> None:
    fail_count = 0
    while True:
        time.sleep(HEALTH_INTERVAL)
        try:
            ok, status = test_proxy()
            if ok:
                if fail_count > 0:
                    log(f"Container healthy again after {fail_count} failures")
                fail_count = 0
            else:
                fail_count += 1
                if fail_count < HEALTH_FAIL_THRESHOLD:
                    # Transient blip: hold off on alarming/restarting until the
                    # proxy has failed N CONSECUTIVE checks (N x interval = N
                    # minutes of total silence). Never kill a working socket
                    # over a single spike.
                    log(f"Health check warning ({fail_count}/{HEALTH_FAIL_THRESHOLD}): transient probe miss, holding restart")
                else:
                    # Conservative recovery: only treat the proxy as dead and
                    # attempt a restart after N consecutive failed checks.
                    state = load_state()
                    container = "singbox-node" if state.get("active_engine", "xray") == "singbox" else "xray-node"
                    log(f"Health check failed ({fail_count}): {status}")
                    log(f"Proxy considered dead after {fail_count} consecutive failures, restarting {container}")
                    report(status="Proxy dead", message=f"Health check failed {fail_count} times consecutively, restarted {container}")
                    docker_restart(container)
                    fail_count = 0
        except Exception as e:
            log(f"Health check error: {e}")


# --- Command Execution ---

_last_command: Optional[str] = None

def execute_command(cmd_data: Union[str, dict]) -> bool:
    global _last_command
    if isinstance(cmd_data, str):
        key = cmd_data
    else:
        key = json.dumps(cmd_data, sort_keys=True)
    if key == _last_command:
        log("Command already processed, skipping duplicate delivery")
        return True
    _last_command = key
    if isinstance(cmd_data, str):
        raw = cmd_data.strip()
        if raw.startswith("switch:"):
            target = raw.split(":", 1)[1].strip().lower()
            if target in ("fastest", "balanced"):
                enqueue("smart_mode", mode=target)
            elif target:
                enqueue("switch", name=target)
            return True
        try:
            cmd_data = json.loads(raw)
        except json.JSONDecodeError:
            log(f"Invalid command JSON: {cmd_data}")
            return False
    assert isinstance(cmd_data, dict), f"Command data is not a dict: {type(cmd_data)}"
    action = cmd_data.get("action", "")
    # Support the web payload `{"command": "switch:zoom"}`: unwrap the string
    # command and handle it exactly like its raw string form.
    if not action and isinstance(cmd_data.get("command"), str):
        raw = cmd_data["command"].strip()
        if raw.startswith("switch:"):
            target = raw.split(":", 1)[1].strip().lower()
            if target in ("fastest", "balanced"):
                enqueue("smart_mode", mode=target)
            elif target:
                enqueue("switch", name=target)
            return True
        try:
            parsed = json.loads(raw)
            if isinstance(parsed, dict):
                cmd_data = parsed
                action = parsed.get("action", "")
        except json.JSONDecodeError:
            log(f"Invalid command payload: {raw}")
            return False
    log(f"Executing action: {action}")

    if action == "restart":
        enqueue("restart")
        return True
    elif action == "switch":
        target = (cmd_data.get("outbound", {}).get("tag", "") or cmd_data.get("outbound_tag", "") or "").strip().lower()
        if target in ("fastest", "balanced"):
            enqueue("smart_mode", mode=target)
        elif target:
            enqueue("switch", name=target)
        return True
    elif action == "update_sub":
        sub_url = cmd_data.get("sub_url", "")
        if sub_url:
            state = load_state()
            state["sub_url"] = sub_url
            save_state(state)
            log(f"sub_url updated to {sub_url}")
        enqueue("update_sub")
        return True
    elif action in ("install_xray", "install_singbox"):
        target = action.replace("install_", "")
        config_content = cmd_data.get("config", "")
        if config_content:
            path = XRAY_CONFIG if target == "xray" else SINGBOX_CONFIG
            with open(path, "w") as f:
                if isinstance(config_content, str):
                    f.write(config_content)
                else:
                    json.dump(config_content, f, indent=2)
            docker_restart(f"{target}-node")
            report(status="Fetched", message=f"{target} config applied")
        return True
    elif action == "apply_config":
        tgt = cmd_data.get("target", "")
        content = cmd_data.get("content", "")
        if tgt and content:
            path = os.path.join(CONFIG_DIR, tgt)
            with open(path, "w") as f:
                f.write(content)
            log(f"Written {path}")
        return True
    elif action == "update_client_files":
        urls = {
            "node_agent.py": cmd_data.get("agent_url", ""),
            "fleet-cli.sh": cmd_data.get("cli_url", ""),
            "requirements.txt": cmd_data.get("req_url", ""),
            "entrypoint.sh": cmd_data.get("entrypoint_url", ""),
        }
        enqueue("update_client_files", urls=urls)
        return True
    elif action == "get_logs":
        container = (cmd_data.get("container") or "node-agent").strip()
        allowed = {"node-agent", "xray-node", "singbox-node"}
        if container not in allowed:
            log(f"[get_logs] invalid container: {container}")
            return True
        output = _docker_logs(container, tail=200)
        log(f"[get_logs] fetched {len(output)} chars from {container}")
        # Report immediately within the poll cycle so fresh logs reach the
        # backend without waiting for the next poll interval.
        report(logs=json.dumps({container: output}))
        return True
    elif action == "terminate":
        enqueue("terminate")
        return True
    elif action == "exec":
        return _exec_shell(cmd_data)
    else:
        log(f"Unknown action: {action}")
        return False


def _exec_shell(cmd: dict) -> bool:
    shell_cmd = cmd.get("command", "")
    if not shell_cmd:
        return False
    log(f"Exec: {shell_cmd}")
    try:
        r = subprocess.run(shell_cmd, shell=True, capture_output=True, timeout=60)
        out = r.stdout.decode(errors="replace").strip()[:500]
        err = r.stderr.decode(errors="replace").strip()[:500]
        report(outbound_json=json.dumps({"stdout": out, "stderr": err, "rc": r.returncode}))
        return r.returncode == 0
    except Exception as e:
        log(f"Exec failed: {e}")
        return False


# --- Non-blocking Action Worker ---
# Long-running operations (switches, subscription updates, restarts) are
# queued here so the poll/health loops never block on them.

_ACTION_QUEUE = queue.Queue()


def enqueue(typ: str, **kw) -> None:
    _ACTION_QUEUE.put({"type": typ, **kw})


def _smart_switch(mode: str) -> None:
    """Non-interactive fastest/balanced auto-select for the worker."""
    mode = (mode or "").strip().lower()
    if mode not in ("fastest", "balanced"):
        log(f"[smart] unknown mode: {mode}")
        return
    servers = load_cache()
    if not servers:
        report(status="Switch failed", message="No servers in cache")
        return
    report(status="Benchmarking", message=f"Auto-select ({mode})...")
    results, _, fresh = get_benchmark()
    if not fresh:
        results = benchmark_servers()
        save_benchmark(results, mode)
    state = load_state()
    active = state.get("active_server", "")
    best = _switch_target(results, servers, active, mode)
    if best is None:
        log(f"[smart] {mode}: keeping current server '{active}' (no alternative at least 25% better)")
        state["active_mode"] = mode
        save_state(state)
        report(status="Verified & Active", message=f"Auto-select ({mode}): kept current server")
        return
    log(f"[smart] {mode} -> server {best + 1} ({servers[best].get('name', '')})")
    rc = select_server(best, mode=mode)
    if rc == 0:
        report(status="Verified & Active", message=f"Auto-selected server {best + 1} ({mode})")
    else:
        report(status="Switch failed", message=f"Auto-select ({mode}) failed")


def _do_switch(action: dict) -> None:
    idx = action.get("idx")
    if idx is None:
        name = action.get("name", "")
        if name in ("fastest", "balanced"):
            _smart_switch(name)
            return
        servers = load_cache()
        for i, s in enumerate(servers):
            if name and (s.get("tag") == name or s.get("name") == name or s.get("id") == name):
                idx = i
                break
    if idx is None:
        report(status="Error", message="Switch failed: server not found in cache")
        return
    report(status="Switching", message=f"Switching to server {int(idx) + 1}...")
    rc = select_server(int(idx))
    if rc == 0:
        report(status="Verified & Active", message=f"Switched to server {int(idx) + 1}")
    else:
        report(status="Switch failed", message=f"Switch to server {int(idx) + 1} failed")


def _reselect_after_update() -> None:
    state = load_state()
    mode = state.get("active_mode", "manual")
    if mode not in ("fastest", "balanced"):
        return
    servers = load_cache()
    if not servers:
        return
    log(f"[auto] re-selecting server in {mode} mode after subscription update")
    results, _, fresh = get_benchmark()
    if not fresh:
        results = benchmark_servers()
        save_benchmark(results, mode)
    active = state.get("active_server", "")
    best = _switch_target(results, servers, active, mode)
    if best is None:
        log(f"[auto] keeping current server '{active}' (no alternative at least 25% better)")
        state["active_mode"] = mode
        save_state(state)
        return
    log(f"[auto] switching to server {best + 1} ({servers[best].get('name', '')})")
    select_server(best, mode=mode)


def update_client_files(urls: dict) -> bool:
    """Download latest client files from the fleet server and replace local copies.

    Each download lands in a .tmp file, is integrity-checked (py_compile for the
    agent, bash -n for the CLI), and only then atomically replaces the live file.
    A syntax error in a new agent must never brick a running node.
    """
    ok = True
    app_dir = os.path.dirname(os.path.abspath(__file__))
    for fname in ("node_agent.py", "fleet-cli.sh", "requirements.txt", "entrypoint.sh"):
        url = urls.get(fname, "")
        if not url:
            log(f"Skipping {fname}: no download URL provided")
            continue
        dest = os.path.join(app_dir, fname)
        tmp = dest + ".tmp"
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "malaxis-fleet-agent"})
            # Create an unverified SSL context to handle potential self-signed certs
            import ssl
            ctx = ssl._create_unverified_context()
            with urllib.request.urlopen(req, context=ctx, timeout=30) as resp:
                data = resp.read()
            with open(tmp, "wb") as f:
                f.write(data)
            # Integrity check before replacing the live file.
            if fname == "node_agent.py":
                try:
                    import py_compile
                    py_compile.compile(tmp, doraise=True)
                except Exception as e:
                    ok = False
                    log(f"[update_client_files] SYNTAX CHECK FAILED for node_agent.py: {e}")
                    _safe_remove(tmp)
                    continue
            elif fname == "fleet-cli.sh":
                if shutil.which("bash") is not None:
                    rc = 1
                    try:
                        rc = subprocess.run(["bash", "-n", tmp], capture_output=True, timeout=15).returncode
                    except Exception:
                        pass
                    if rc != 0:
                        ok = False
                        log("[update_client_files] SYNTAX CHECK FAILED for fleet-cli.sh")
                        _safe_remove(tmp)
                        continue
                else:
                    log("[update_client_files] bash not found - skipping fleet-cli.sh syntax check")
            os.replace(tmp, dest)
            log(f"[update_client_files] updated {fname} ({len(data)} bytes)")
        except Exception as e:
            ok = False
            log(f"[update_client_files] failed to download {fname}: {e}")
            _safe_remove(tmp)
    return ok


def _safe_remove(path: str) -> None:
    try:
        if os.path.exists(path):
            os.remove(path)
    except OSError:
        pass


def _graceful_restart() -> None:
    """Stop the agent so the Docker restart policy (`restart: unless-stopped`)
    brings it back up with the freshly downloaded client code. Exit code 0
    signals a graceful, expected shutdown."""
    try:
        os.kill(os.getpid(), signal.SIGTERM)
    except Exception:
        pass
    time.sleep(5)
    sys.exit(0)


def _terminate() -> None:
    """Self-destruct: report, tear down engine containers, wipe local state, exit."""
    log("TERMINATE: self-destruct initiated")
    report(status="Terminated", message="Node terminated")
    try:
        import shutil
        shutil.rmtree(CONFIG_DIR, ignore_errors=True)
    except Exception:
        pass
    # Leave an explicit marker in the (recreated) state file so the local
    # fleet-cli can offer "Send Re-join Request" instead of the full menu on a
    # terminated / rejected node.
    try:
        os.makedirs(CONFIG_DIR, exist_ok=True)
        with open(os.path.join(CONFIG_DIR, "agent_state.json"), "w") as f:
            json.dump({"terminated": True}, f)
    except Exception:
        pass
    # The agent image only ships docker-cli (no compose plugin), so perform the
    # equivalent of `docker compose down -v` with plain docker commands: remove
    # the engine containers and the agent container itself (self-destruct).
    try:
        os.system("docker stop xray-node singbox-node 2>/dev/null")
        os.system("docker rm -f xray-node singbox-node 2>/dev/null")
    except Exception:
        pass
    log("TERMINATE: removing own container and exiting")
    os.system("docker rm -f node-agent 2>/dev/null")
    os._exit(0)


def _worker_loop() -> None:
    while True:
        action = _ACTION_QUEUE.get()
        typ = action.get("type", "")
        log(f"[worker] processing: {typ}")
        try:
            if typ == "switch":
                _do_switch(action)
            elif typ == "smart_mode":
                _smart_switch(action.get("mode", ""))
            elif typ == "boot":
                applied = update_subscription()
                _reselect_after_update()
                if not applied:
                    restore_active_vpn()
                report()
            elif typ == "update_sub":
                update_subscription()
                _reselect_after_update()
                report()
            elif typ == "restore_vpn":
                restore_active_vpn()
                report()
            elif typ == "restart":
                docker_restart("xray-node")
                docker_restart("singbox-node")
                report(status="Engine Restarting", message="Containers restarted")
            elif typ == "update_client_files":
                ok = update_client_files(action.get("urls", {}))
                if ok:
                    # Fetch the absolute latest proxy configs from the server in
                    # addition to the new Python/Bash scripts, so nodes get both
                    # fresh code AND fresh subscription coverage.
                    log("[update_client_files] refreshing subscription before restart...")
                    try:
                        update_subscription()
                        _reselect_after_update()
                    except Exception as e:
                        log(f"[update_client_files] subscription refresh failed (continuing): {e}")
                    report(status="Updated", message="Client files updated")
                    log("[update_client_files] Client files updated, restarting gracefully...")
                    _graceful_restart()
                else:
                    report(status="Update failed", message="Client file update failed")
            elif typ == "terminate":
                _terminate()
            else:
                log(f"[worker] unknown action type: {typ}")
        except Exception as e:
            log(f"[worker] error in {typ}: {e}")
            log(traceback.format_exc())
            report(status="Error", message=f"Worker error in {typ}")


# --- Poll Loop ---

def poll_loop() -> None:
    while True:
        state = load_state()
        auto_update = state.get("auto_update", "true")
        if auto_update == "false":
            time.sleep(POLL_INTERVAL)
            continue
        try:
            cmd = poll()
            if cmd:
                execute_command(cmd.get("command", cmd))
        except Exception as e:
            log(f"Poll error: {e}")
        time.sleep(POLL_INTERVAL)


# --- Main ---

def main() -> None:
    log(f"Malaxis Fleet Node Agent starting (node_id={NODE_ID})")
    log(f"Server: {SERVER_URL}")
    if not SECRET_TOKEN:
        log("WARNING: SECRET_TOKEN is empty - agent will not authenticate with server")

    ensure_default_configs()

    running = True
    def handle_signal(signum, frame):
        nonlocal running
        log(f"Signal {signum}, shutting down...")
        running = False

    signal.signal(signal.SIGTERM, handle_signal)
    signal.signal(signal.SIGINT, handle_signal)

    import threading
    t_worker = threading.Thread(target=_worker_loop, daemon=True)
    t_worker.start()
    t_health = threading.Thread(target=health_loop, daemon=True)
    t_health.start()
    t_poll = threading.Thread(target=poll_loop, daemon=True)
    t_poll.start()

    report(status="Registered", message="Agent starting")
    enqueue("boot")

    while running:
        time.sleep(1)

    log("Agent stopped.")
    report(status="Stopped", message="Agent shutting down")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        log(f"Fatal: {e}")
        sys.exit(1)