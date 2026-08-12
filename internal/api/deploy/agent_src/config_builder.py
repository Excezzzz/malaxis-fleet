#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Config generation: builds xray / sing-box JSON configs from parsed server
descriptors, converts a raw proxy URL into an outbound object, and ships the
default bootstrap configs."""
import base64
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
            "sniff": {"enabled": True, "override_destination": False},
        },
        {
            "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358,
            "sniff": {"enabled": True, "override_destination": True},
        },
    ],
    "outbounds": [{"type": "direct", "tag": "direct"}],
    "route": {
        "final": "direct",
        "auto_detect_interface": True,
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
                "sniffing": {"enabled": True, "destOverride": ["http", "tls"]},
                "tag": "socks-in",
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15},
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
            agent.log("[VLESS patch] Correcting network type 'http' to 'tcp' for Reality.")
            net_type = "tcp"
        security_str = srv.get("security", "none")
        flow_str = srv.get("flow", "")
        sni_str = srv.get("sni", "")
        if not sni_str:
            agent.log("[SNI patch] Empty SNI, keeping hostname")
            sni_str = srv.get("hostname", "")

        pbk_str = srv.get("pbk", "")
        fp_str = _normalize_fp(srv.get("fp", "chrome"))
        spx_str = srv.get("spx", "")
        sid_str = srv.get("sid", "")
        path_str = srv.get("path", "/")
        host_str = srv.get("hostname", "")

        agent.log(f"[VLESS outbound] uuid={srv.get('uuid','')} host={host_str} port={srv.get('port',0)} net={net_type} sec={security_str} flow={flow_str} sni={sni_str} pbk={pbk_str} fp={fp_str} sid={sid_str} spx={spx_str} path={path_str}")

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
                "sniffing": {"enabled": True, "destOverride": ["http", "tls"]},
                "tag": "socks-in",
                "sockopt": {"tcpNoDelay": True, "tcpKeepAliveInterval": 15},
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
                "sniff": {"enabled": True, "override_destination": False},
            },
            {
                "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358,
                "sniff": {"enabled": True, "override_destination": True},
            },
        ],
        "outbounds": [ob, {"type": "direct", "tag": "direct", "tcp_keep_alive": "5m", "tcp_keep_alive_interval": "15s"}],
        "route": {
            "final": ob.get("tag", "proxy"),
            "auto_detect_interface": True,
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
                "sniff": {"enabled": True, "override_destination": False},
            },
            {
                "type": "http", "tag": "http-in", "listen": "0.0.0.0", "listen_port": 6358,
                "sniff": {"enabled": True, "override_destination": True},
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
            agent.log("[SNI patch] Empty SNI, keeping hostname")
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
