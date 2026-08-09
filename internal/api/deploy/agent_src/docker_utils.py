#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Docker control: run docker commands with hard timeouts, resolve compose-
prefixed container ids, fetch cleaned log tails, restart engine containers."""
import json
import re
import subprocess
import time

from agent_src import agent


def _docker(args: list, timeout: int = 30) -> int:
    """Run a docker command with a hard timeout. Returns exit code (or -1)."""
    try:
        r = subprocess.run(["docker"] + args, capture_output=True, timeout=timeout)
        if r.returncode != 0:
            err = r.stderr.decode(errors="replace").strip()
            if err:
                agent.log(f"docker {' '.join(args)}: {err[:300]}")
        return r.returncode
    except subprocess.TimeoutExpired:
        agent.log(f"docker {' '.join(args)}: timed out after {timeout}s")
        return -1
    except Exception as e:
        agent.log(f"docker {' '.join(args)}: {e}")
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


def docker_restart(container: str) -> None:
    if container == "singbox-node":
        _ensure_xray_running()
    rc = _docker(["restart", container])
    if rc != 0:
        logs = _docker_output(["logs", container, "--tail", "20"])
        agent.log(f"{container} logs: {logs}")
    else:
        agent.log(f"Restarted {container}")


# --- Singbox prerequisite helper ---

def _ensure_xray_running() -> bool:
    if _docker_output(["inspect", "-f", "{{.State.Running}}", "xray-node"]) == "true":
        return True
    agent.log("xray-node not running, starting with dummy config for singbox prerequisite")
    dummy = {"log": {"loglevel": "warning"}, "inbounds": [{"port": 9999, "listen": "127.0.0.1", "protocol": "socks", "settings": {"auth": "noauth"}}], "outbounds": [{"protocol": "freedom", "tag": "direct"}]}
    with open(agent.XRAY_CONFIG, "w", encoding="utf-8") as f:
        json.dump(dummy, f, indent=2)
    _docker(["stop", "singbox-node"])
    if _docker(["start", "xray-node"]) != 0:
        _docker(["restart", "xray-node"])
    time.sleep(2)
    agent.log("xray-node started (dummy config)")
    return False
