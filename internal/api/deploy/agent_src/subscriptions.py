#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Subscription parsing: fetch a 3x-ui subscription URL and convert every
proxy link (vless/vmess/trojan/ss/hysteria2/tuic/wireguard) into a uniform
server descriptor consumed by the config builders."""
import base64
import traceback
import urllib.parse
from typing import Optional

import requests

from agent_src import agent

SUB_USER_AGENT = "v2rayN/6.23 Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"


def parse_subscription(sub_url: str) -> list:
    servers = []
    try:
        resp = requests.get(sub_url, headers={"User-Agent": SUB_USER_AGENT}, verify=False, timeout=15)
        agent.log(f"Sub response status: {resp.status_code}, length: {len(resp.text)}")
        if resp.status_code != 200:
            agent.log(f"Subscription fetch returned {resp.status_code}")
            return servers
        raw_text = resp.text
    except Exception as e:
        agent.log(f"Subscription fetch failed for {sub_url}: {e}")
        agent.log(traceback.format_exc())
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
            agent.log(f"Parse error: {e}")
            continue

    agent.log(f"Parsed {len(servers)} servers from subscription")
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
        # The sing-box image is built without shadowsocks outbound support
        # (unknown outbound type: ss), so ss servers must run through xray.
        info["engine"] = "xray"
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
