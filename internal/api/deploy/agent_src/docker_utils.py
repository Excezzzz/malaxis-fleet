#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Docker control: run docker commands with hard timeouts, resolve compose-
prefixed container ids, fetch cleaned log tails, restart engine containers."""
import json
import re
import subprocess
import time

from agent_src import agent, config_builder


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


def _compose_available(cmd: list) -> bool:
    """True if the given compose command actually runs (`docker compose version`
    for the v2 plugin, `docker-compose version` for the v1 standalone binary)."""
    try:
        r = subprocess.run(cmd + ["version"], capture_output=True, timeout=15)
        return r.returncode == 0
    except Exception:
        return False


def _compose_cmd() -> list:
    """Resolve the docker compose command as a tokenized argv list.

    The v2 plugin wins whenever it works (a v1-only machine saved
    "docker-compose" must keep working, but a v2 machine must never run v1).
    Falls back to the saved installer choice, then to v1 standalone.
    """
    state = agent.load_state()
    saved = state.get("compose_cmd", "")
    if _compose_available(["docker", "compose"]):
        return ["docker", "compose"]
    if isinstance(saved, str) and saved.strip():
        parts = saved.strip().split()
        if _compose_available(parts):
            return parts
    if _compose_available(["docker-compose"]):
        return ["docker-compose"]
    return ["docker", "compose"]


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
    would then fail, so we look up the real container id by name filter first;
    if that misses, we fall back to `docker compose logs <service>` which
    resolves the compose service name itself. The output is cleaned of ANSI
    codes and decoded as UTF-8 with errors ignored so JSON serialization never
    truncates or fails.
    """
    target = _resolve_container_id(container)
    if target:
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
        except subprocess.TimeoutExpired:
            return f"(docker logs {container} timed out)"
        except Exception:
            pass
    try:
        r = subprocess.run(
            _compose_cmd() + ["logs", "--tail", str(tail), "--timestamps", container],
            capture_output=True, timeout=30,
        )
        out = r.stdout.decode("utf-8", errors="ignore")
        err = r.stderr.decode("utf-8", errors="ignore")
        combined = _clean_log_text((out + err)).strip()
        if combined:
            return combined
    except subprocess.TimeoutExpired:
        return f"(docker compose logs {container} timed out)"
    except Exception as e:
        return f"(failed to fetch logs for {container}: {e})"
    return f"(container '{container}' not found - is it running?)"


def _resolve_container_id(name: str) -> str:
    """Find the running container id for a service name.

    Docker-compose renames services to `<project>-<service>-<counter>` (e.g.
    `fleet-agent-xray-node-1`). We search with progressively looser name-filters
    (exact regex first, then compose-style suffixes, then substring) and always
    restrict to running containers so a stopped ghost can never shadow the real
    one. Returns the container id, or ''.
    """
    filters = [
        f"name=^/{name}$",          # exact: /xray-node
        f"name=^/{name}-\\d+$",     # compose: /fleet-agent-xray-node-1
        f"name=-{name}-\\d+$",      # compose without project prefix edge cases
        f"name=-{name}-",           # substring for compose projects
        f"name={name}",             # any container whose name contains it
    ]
    for f in filters:
        try:
            out = _docker_output(["ps", "-q", "-f", "status=running", "-f", f])
        except Exception:
            continue
        if out:
            cids = [c for c in out.splitlines() if c]
            if cids:
                return cids[0]
    # Fall back to the chosen compose command (v2 plugin or v1 standalone) for
    # `ps -q <service>`, honoring the compose_cmd saved by the installer.
    try:
        r = subprocess.run(
            _compose_cmd() + ["ps", "-q", name], capture_output=True, timeout=10
        )
        out = r.stdout.decode(errors="replace").strip()
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
        log_crash_logs(container)
    else:
        agent.log(f"Restarted {container}")


def log_crash_logs(container: str, tail: int = 15) -> None:
    """Dump the last `tail` lines of a crash-looping engine container straight
    into the agent log so failures are diagnosable without SSH."""
    logs = _docker_logs(container, tail=tail)
    agent.log(f"=== {container} CRASH LOG (last {tail} lines) ===")
    agent.log(logs if logs else f"(no log output for {container})")


# --- Singbox prerequisite helper ---

def _ensure_xray_running() -> bool:
    """Guarantee xray-node runs the sterile dummy config.

    singbox-node shares xray-node's network namespace, so ANY non-dummy xray
    config (which binds 6357/6358 for its own socks/http inbounds) collides
    with sing-box's identical inbound ports -> EADDRINUSE crash loop. We
    therefore ALWAYS force the dummy config here - never trust a merely
    "running" xray-node, its on-disk config may still be the stale full one
    (fresh install, engine switch remnant, crashed apply/rollback).
    """
    agent.log("xray-node: forcing dummy config for singbox prerequisite")
    with open(agent.XRAY_CONFIG, "w", encoding="utf-8") as f:
        json.dump(config_builder.DUMMY_XRAY_CONFIG, f, indent=2)
    # A crash-looping container keeps fighting the restart policy; stop it so
    # the next start is a clean boot from the sterile dummy config on disk.
    # Stopping xray-node also tears down singbox-node (shared netns), so stop
    # it explicitly to avoid racing the restart policy.
    _docker(["stop", "singbox-node"])
    _docker(["stop", "xray-node"])
    if _docker(["start", "xray-node"]) != 0:
        log_crash_logs("xray-node")
        _docker(["restart", "xray-node"])
    time.sleep(2)
    if _docker_output(["inspect", "-f", "{{.State.Status}}", "xray-node"]) != "running":
        agent.log("xray-node still NOT running after dummy config apply")
        log_crash_logs("xray-node")
        return False
    agent.log("xray-node started (dummy config)")
    return True
