#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""HTTP transport: HMAC-signed requests to the master, node poll/report."""
import hashlib
import hmac
import json
import platform
import socket
import time
import urllib.error
import urllib.request
from typing import Any, Tuple, Union

from agent_src import agent

POLL_URL = f"{agent.SERVER_URL}/api/poll"
REPORT_URL = f"{agent.SERVER_URL}/api/report"


def _sign(body: bytes, ts: int) -> str:
    mac = hmac.new(agent.SECRET_TOKEN.encode(), str(ts).encode() + body, hashlib.sha256)
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
        "id": agent.NODE_ID,
        "hostname": platform.node(),
        "ip_lan": _lan_ip(),
        "hardware_hash": agent.HARDWARE_HASH,
    }
    try:
        state = agent.load_state()
        if state.get("node_name"):
            payload["name"] = state["node_name"]
        if state.get("sub_url"):
            payload["sub_url"] = state["sub_url"]
    except Exception:
        pass
    status, data = _request("POST", POLL_URL, payload)
    if status == 200 and isinstance(data, dict):
        if data.get("node_id") and data["node_id"] != agent.NODE_ID:
            agent._adopt_node_id(data["node_id"])
        if "command" in data:
            agent.log(f"Command: {data['command']}")
            return data
        if data.get("status") == "ok":
            return None
    agent.log(f"Poll status={status}")
    return None


def report(**kw: Any) -> None:
    payload: dict[str, Any] = {"id": agent.NODE_ID, "hardware_hash": agent.HARDWARE_HASH}
    payload.update(kw)
    try:
        state = agent.load_state()
        payload.setdefault("active_server", state.get("active_server", ""))
        payload.setdefault("sub_url", state.get("sub_url", ""))
        payload.setdefault("engine", state.get("active_engine", ""))
        payload.setdefault("protocol", state.get("active_proto", ""))
        payload.setdefault("name", state.get("node_name", ""))
        servers = agent.load_cache()
        payload.setdefault("available_servers", [s.get("tag") or s.get("name") or f"Server {i + 1}" for i, s in enumerate(servers)])
    except Exception:
        pass
    code, _ = _request("POST", REPORT_URL, payload)
    if code != 200:
        agent.log(f"Report returned {code}")


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
