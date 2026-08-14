#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Config generation: builds xray / sing-box JSON configs from parsed server
descriptors, converts a raw proxy URL into an outbound object, and ships the
default bootstrap configs."""
import base64
import re
import urllib.parse
from typing import Optional, Tuple

from agent_src import agent

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
            "tag": "socks-in",
        },
        {
            "port": 6358, "listen": "0.0.0.0", "protocol": "http", "tag": "http-in",
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

DUMMY_XRAY_CONFIG = {
    "log": {"loglevel": "warning"},
    "inbounds": [
        {
            "port": 9999, "listen": "127.0.0.1", "protocol": "socks",
            "settings": {"auth": "noauth"},
            "tag": "dummy-in",
        },
    ],
    "outbounds": [{"protocol": "freedom", "tag": "direct"}],
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
            "tcp_fast_open": True,
        },
        {
            "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358,
            "tcp_fast_open": True,
        },
    ],
    "outbounds": [{"type": "direct", "tag": "direct", "tcp_fast_open": True}],
    "route": {
        "final": "direct",
        "auto_detect_interface": True,
        # sing-box >= 1.13: inbound sniff options were removed entirely;
        # sniffing is expressed as a route rule action instead.
        "rules": [
            # Telegram MTProto / QUIC: route by IP BEFORE the sniff action,
            # so proxy-bound MTProto flows are never fingerprinted.
            {"action": "route", "inbound": ["socks-in", "http-in"], "ip_cidr": ["91.108.0.0/16", "149.154.160.0/20", "185.76.151.0/24"], "outbound": "direct"},
            {"action": "sniff", "inbound": ["socks-in", "http-in"]},
        ],
    },
    "experimental": {"cache_file": {"enabled": True}},
}


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
                # destOverride http/tls for routing decisions only; keep the
                # block minimal - routeOnly is rejected by some Xray builds.
                "sniffing": {"enabled": True, "destOverride": ["http", "tls"]},
                "tag": "socks-in",
                "sockopt": {"tcpKeepAliveInterval": 15},
            },
            {
                "port": 6358, "listen": "0.0.0.0", "protocol": "http", "tag": "http-in",
                "sockopt": {"tcpKeepAliveInterval": 15},
            },
        ],
        "outbounds": [],
    }
    for i, srv in enumerate(servers):
        ob = _xray_outbound(srv)
        if ob:
            ob["tag"] = srv.get("tag", f"server-{i}")
            cfg["outbounds"].append(ob)
    tag = "direct"
    if servers:
        idx = active_idx if 0 <= active_idx < len(servers) else 0
        tag = servers[idx].get("tag", f"server-{idx}")
    cfg["outbounds"].append({"protocol": "freedom", "tag": "direct", "streamSettings": {"sockopt": {"tcpKeepAliveInterval": 15}}})
    cfg["routing"] = {
        "domainStrategy": "IPIfNonMatch",
        "rules": [
            # Telegram MTProto / QUIC: matched by IP FIRST so these flows are
            # routed through the proxy without stalling on HTTP/TLS sniffing.
            {"type": "field", "ip": ["91.108.0.0/16", "149.154.160.0/20", "185.76.151.0/24"], "outboundTag": tag},
            {"type": "field", "port": 53, "network": "udp", "outboundTag": tag},
            {"type": "field", "inboundTag": ["socks-in", "http-in"], "outboundTag": tag},
        ],
    }
    _purge_route_only(cfg)
    return cfg


_VALID_NETWORKS = {
    "tcp", "raw", "kcp", "mkcp", "grpc", "ws", "websocket",
    "xhttp", "splithttp", "httpupgrade", "hysteria",
}
_VALID_SECURITY = {"none", "tls", "reality"}
_VALID_FLOWS = {"", "xtls-rprx-vision", "xtls-rprx-vision-udp443"}
_VALID_FP = {"chrome", "firefox", "edge", "safari", "ios", "android", "360", "qq", "random", "randomized"}
_UUID_RE = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")


def _is_valid_uuid(uuid_str: str) -> bool:
    return bool(uuid_str) and bool(_UUID_RE.match(uuid_str))


def _valid_reality_pbk(pbk: str) -> bool:
    if not pbk:
        return False
    try:
        decoded = base64.urlsafe_b64decode(pbk + "=" * (-len(pbk) % 4))
        return len(decoded) == 32
    except Exception:
        return False


def _sanitize_network(net: str) -> str:
    net = (net or "").strip().lower()
    if net in _VALID_NETWORKS:
        return net
    return "tcp"


def _purge_route_only(obj) -> None:
    """Failsafe: recursively strip `routeOnly` from every sniffing block.

    Some Xray builds hard-reject the key at config load, so no generated
    config may ever contain it, no matter where it was introduced.
    """
    if isinstance(obj, dict):
        for key, val in list(obj.items()):
            if key == "sniffing" and isinstance(val, dict):
                val.pop("routeOnly", None)
            _purge_route_only(val)
    elif isinstance(obj, list):
        for item in obj:
            _purge_route_only(item)


def _normalize_fp(fp: str) -> str:
    # Xray rejects any fingerprint outside the known uTLS set with a hard
    # startup error; everything unrecognized falls back to chrome.
    normalized = (fp or "").strip().lower()
    if normalized in _VALID_FP:
        return normalized
    return "chrome"


def _xray_outbound(srv: dict) -> Optional[dict]:
    stype = srv.get("type", "")
    host = srv.get("hostname", "")
    port = int(srv.get("port", 0))

    if stype == "vless":
        host_str = srv.get("hostname", "")
        port = int(srv.get("port", 0) or 0)
        if not host_str or port <= 0:
            agent.log(f"[VLESS sanitize] Skipping server with invalid host/port: {host_str}:{port}")
            return None
        uuid_val = srv.get("uuid", "")
        if not _is_valid_uuid(uuid_val):
            agent.log(f"[VLESS sanitize] Skipping server with invalid uuid: {uuid_val}")
            return None

        net_type = _sanitize_network(srv.get("network", "tcp"))
        security_str = (srv.get("security") or "none").strip().lower()
        if security_str not in _VALID_SECURITY:
            agent.log(f"[VLESS sanitize] Unsupported security '{security_str}', using 'none'")
            security_str = "none"
        flow_str = srv.get("flow", "")
        if flow_str not in _VALID_FLOWS:
            agent.log(f"[VLESS sanitize] Unsupported flow '{flow_str}', removing it")
            flow_str = ""
        sni_str = srv.get("sni", "") or host_str
        pbk_str = (srv.get("pbk") or "").strip()
        fp_str = _normalize_fp(srv.get("fp", "chrome"))
        spx_str = urllib.parse.unquote(srv.get("spx") or "").strip()
        sid_str = (srv.get("sid") or "").strip()
        path_str = srv.get("path", "/") or "/"

        agent.log(f"[VLESS outbound] uuid={uuid_val} host={host_str} port={port} net={net_type} sec={security_str} flow={flow_str} sni={sni_str} pbk={pbk_str} fp={fp_str} sid={sid_str} spx={spx_str} path={path_str}")

        user_spec = {"id": uuid_val, "encryption": "none"}
        if security_str == "reality":
            if not _valid_reality_pbk(pbk_str):
                agent.log("[VLESS sanitize] Reality requires a valid 32-byte publicKey; falling back to 'none'")
                security_str = "none"
            else:
                if len(sid_str) > 16 or (sid_str and not re.fullmatch(r"[0-9a-fA-F]+", sid_str)):
                    agent.log("[VLESS sanitize] Invalid shortId, dropping it")
                    sid_str = ""
                if not spx_str.startswith("/"):
                    agent.log("[VLESS sanitize] Invalid spiderX, dropping it")
                    spx_str = ""
                if flow_str:
                    user_spec["flow"] = flow_str

        ob: dict = {
            "protocol": "vless",
            "settings": {
                "vnext": [{
                    "address": host_str,
                    "port": port,
                    "users": [user_spec],
                }]
            },
            "streamSettings": {
                "network": net_type,
                "security": security_str,
                "sockopt": {"tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 15},
            },
        }

        if security_str == "reality":
            ob["streamSettings"]["realitySettings"] = {
                "show": False,
                "fingerprint": fp_str,
                "serverName": sni_str,
                "publicKey": pbk_str,
                "shortId": sid_str,
                "spiderX": spx_str,
            }
        elif security_str == "tls":
            ob["streamSettings"]["tlsSettings"] = {"serverName": sni_str}

        if net_type == "xhttp":
            # No xPaddingBytes: Xray 26.x accepts xhttp without padding, and
            # padding adds latency to every small Telegram media packet.
            ob["streamSettings"]["xhttpSettings"] = {
                "mode": "auto",
                "path": path_str,
                "extra": {
                    "mode": "auto",
                    "xmux": {
                        # strictly maxConnections-only: Xray 26.x hard-errors on
                        # maxConcurrency + maxConnections combined at startup.
                        "maxConnections": 4,
                    },
                },
            }

        ob["mux"] = {"enabled": False}
        return ob

    if stype == "vmess":
        if not host or port <= 0:
            agent.log(f"[vmess sanitize] Skipping server with invalid host/port: {host}:{port}")
            return None
        uuid_val = srv.get("uuid", "")
        if not _is_valid_uuid(uuid_val):
            agent.log(f"[vmess sanitize] Skipping server with invalid uuid: {uuid_val}")
            return None
        net_type = _sanitize_network(srv.get("network", "tcp"))
        security_str = (srv.get("security") or "none").strip().lower()
        if security_str not in ("none", "tls"):
            agent.log(f"[vmess sanitize] Unsupported security '{security_str}', using 'none'")
            security_str = "none"
        ob: dict = {
            "protocol": "vmess",
            "settings": {
                "vnext": [{
                    "address": host,
                    "port": port,
                    "users": [{"id": uuid_val, "security": "auto"}],
                }]
            },
            "streamSettings": {
                "network": net_type,
                "security": security_str,
                "sockopt": {"tcpKeepAliveInterval": 15},
            },
        }
        if security_str == "tls":
            tls_settings: dict = {}
            sni_val = srv.get("sni", "")
            if sni_val:
                tls_settings["serverName"] = sni_val
            ob["streamSettings"]["tlsSettings"] = tls_settings
        return ob

    if stype == "trojan":
        if not host or port <= 0:
            agent.log(f"[trojan sanitize] Skipping server with invalid host/port: {host}:{port}")
            return None
        ob: dict = {
            "protocol": "trojan",
            "settings": {
                "servers": [{"address": host, "port": port, "password": srv.get("password", "")}],
            },
            "streamSettings": {
                "network": "tcp",
                "security": "tls",
                "tlsSettings": {"serverName": srv.get("sni", "") or host},
                "sockopt": {"tcpKeepAliveInterval": 15},
            },
        }
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
                "sockopt": {"tcpKeepAliveInterval": 15},
            },
        }

    return None


def _xray_cfg_with_outbound(ob: dict) -> dict:
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
                # destOverride http/tls for routing decisions only; keep the
                # block minimal - routeOnly is rejected by some Xray builds.
                "sniffing": {"enabled": True, "destOverride": ["http", "tls"]},
                "tag": "socks-in",
                "sockopt": {"tcpKeepAliveInterval": 15},
            },
            {
                "port": 6358, "listen": "0.0.0.0", "protocol": "http", "tag": "http-in",
                "sockopt": {"tcpKeepAliveInterval": 15},
            },
        ],
        "outbounds": [ob, {"protocol": "freedom", "tag": "direct", "streamSettings": {"sockopt": {"tcpKeepAliveInterval": 15}}}],
        "routing": {
            "domainStrategy": "IPIfNonMatch",
            "rules": [
                # Telegram MTProto / QUIC IP ranges: matched FIRST so these
                # flows bypass sniffing fallbacks entirely.
                {"type": "field", "ip": ["91.108.0.0/16", "149.154.160.0/20", "185.76.151.0/24"], "outboundTag": ob.get("tag", "proxy")},
                {"type": "field", "port": 53, "network": "udp", "outboundTag": ob.get("tag", "proxy")},
                {"type": "field", "inboundTag": ["socks-in", "http-in"], "outboundTag": ob.get("tag", "proxy")},
            ],
        },
    }
    _purge_route_only(cfg)
    return cfg


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
                "tcp_fast_open": True,
            },
            {
                "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358,
                "tcp_fast_open": True,
            },
        ],
        "outbounds": [ob, {"type": "direct", "tag": "direct", "tcp_fast_open": True, "tcp_keep_alive": "5m", "tcp_keep_alive_interval": "15s"}],
        "route": {
            "final": ob.get("tag", "proxy"),
            "auto_detect_interface": True,
            "rules": [
                # Telegram MTProto / QUIC: route by IP BEFORE the sniff action,
                # so proxy-bound MTProto flows are never fingerprinted.
                {"action": "route", "inbound": ["socks-in", "http-in"], "ip_cidr": ["91.108.0.0/16", "149.154.160.0/20", "185.76.151.0/24"], "outbound": ob.get("tag", "proxy")},
                {"action": "sniff", "inbound": ["socks-in", "http-in"]},
            ],
        },
        "experimental": {"cache_file": {"enabled": True}},
    }


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
                "tcp_fast_open": True,
            },
            {
                "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358,
                "tcp_fast_open": True,
            },
        ],
        "outbounds": [],
    }
    for i, srv in enumerate(servers):
        ob = _singbox_outbound(srv)
        if ob:
            ob["tag"] = srv.get("tag", f"server-{i}")
            cfg["outbounds"].append(ob)
    tag = "direct"
    if servers:
        idx = active_idx if 0 <= active_idx < len(servers) else 0
        tag = servers[idx].get("tag", f"server-{idx}")
    cfg["route"] = {
        "final": tag,
        "auto_detect_interface": True,
        # sing-box >= 1.13: inbound sniff options were removed entirely;
        # sniffing is expressed as a route rule action instead.
        "rules": [
            # Telegram MTProto / QUIC: route by IP BEFORE the sniff action,
            # so proxy-bound MTProto flows are never fingerprinted.
            {"action": "route", "inbound": ["socks-in", "http-in"], "ip_cidr": ["91.108.0.0/16", "149.154.160.0/20", "185.76.151.0/24"], "outbound": tag},
            {"action": "sniff", "inbound": ["socks-in", "http-in"]},
        ],
    }
    cfg["experimental"] = {"cache_file": {"enabled": True}}
    cfg["outbounds"].append({"type": "direct", "tag": "direct", "tcp_fast_open": True, "tcp_keep_alive": "5m", "tcp_keep_alive_interval": "15s"})
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
        # TCP_NODELAY by default. tcp_fast_open reduces connection-setup
        # latency for the frequent small connections Telegram opens.)
        "tcp_fast_open": True,
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
        agent.log("[singbox] shadowsocks outbound unsupported by sing-box image, falling back to xray")
        return None

    if proto not in ("vless", "vmess", "trojan"):
        return None

    net_type = srv.get("network", "tcp")
    if net_type == "http":
        net_type = "tcp"
    if net_type == "xhttp":
        agent.log(f"[singbox] {host} uses xhttp transport - sing-box cannot handle, xray fallback required")
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
        srv["spx"] = urllib.parse.unquote(params.get("spx", ""))
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


def parse_url_to_outbound(url_str: str, engine: str = "singbox") -> Tuple[str, dict]:
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
            agent.log(f"[singbox] No sing-box outbound for {scheme}, falling back to xray")
            return parse_url_to_outbound(url_str, engine="xray")
        ob["tag"] = tag
        return "singbox", ob

    # Xray branch: share the unified server-descriptor pipeline so every
    # protocol (vless/vmess/trojan/ss) produces a proper xray outbound.
    if scheme in ("vmess", "trojan", "ss"):
        srv = _url_to_srv(scheme, user_info, host, port, params, tag)
        ob = _xray_outbound(srv)
        if ob is not None:
            ob["tag"] = tag
            return "xray", ob
        agent.log(f"[xray] No xray outbound for {scheme}")
        return "xray", {"protocol": "freedom", "tag": "direct"}

    if scheme == "vless":
        srv = _url_to_srv(scheme, user_info, host, port, params, tag)
        ob = _xray_outbound(srv)
        if ob is not None:
            ob["tag"] = tag
            return "xray", ob
        agent.log("[xray] No xray outbound for vless server")
        return "xray", {"protocol": "freedom", "tag": "direct"}

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
