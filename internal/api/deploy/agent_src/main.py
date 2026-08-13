#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Entry point: command execution, non-blocking action worker, poll/health
loops and the OTA update_client_files flow. node_agent.py re-exports this
module's public API so the legacy `import node_agent` CLI contract works."""
import json
import os
import py_compile
import queue
import shutil
import signal
import ssl
import subprocess
import sys
import threading
import time
import traceback
import urllib.request
import zipfile
from typing import Any, Optional, Union

from agent_src import agent, docker_utils, engine, network

# Re-export the public CLI API (used by fleet-cli.sh / fleet-cli.ps1 through
# the node_agent.py bootstrap: `import node_agent; node_agent.report(...)`).
from agent_src.engine import (  # noqa: F401
    apply_configs,
    benchmark_servers,
    fetch_subscription_now,
    get_benchmark,
    print_benchmark,
    print_server_list,
    restore_active_vpn,
    rollback_engine,
    save_benchmark,
    select_mode,
    select_server,
    test_proxy,
    update_subscription,
)
from agent_src.network import poll, report  # noqa: F401
from agent_src.config_builder import parse_url_to_outbound  # noqa: F401


def health_loop() -> None:
    fail_count = 0
    while True:
        time.sleep(agent.HEALTH_INTERVAL)
        try:
            ok, status = engine.test_proxy()
            if ok:
                if fail_count > 0:
                    agent.log(f"Container healthy again after {fail_count} failures")
                fail_count = 0
            else:
                fail_count += 1
                if fail_count < agent.HEALTH_FAIL_THRESHOLD:
                    # Transient blip: hold off on alarming/restarting until the
                    # proxy has failed N CONSECUTIVE checks (N x interval = N
                    # minutes of total silence). Never kill a working socket
                    # over a single spike.
                    agent.log(f"Health check warning ({fail_count}/{agent.HEALTH_FAIL_THRESHOLD}): transient probe miss, holding restart")
                else:
                    # Conservative recovery: only treat the proxy as dead and
                    # attempt a restart after N consecutive failed checks.
                    state = agent.load_state()
                    container = "singbox-node" if state.get("active_engine", "singbox") == "singbox" else "xray-node"
                    agent.log(f"Health check failed ({fail_count}): {status}")
                    agent.log(f"Proxy considered dead after {fail_count} consecutive failures, restarting {container}")
                    network.report(status="Proxy dead", message=f"Health check failed {fail_count} times consecutively, restarted {container}")
                    docker_utils.docker_restart(container)
                    docker_utils.log_crash_logs(container)
                    fail_count = 0
        except Exception as e:
            agent.log(f"Health check error: {e}")


# --- Command Execution ---

_last_command: Optional[str] = None


def execute_command(cmd_data: Union[str, dict]) -> bool:
    global _last_command
    if isinstance(cmd_data, str):
        key = cmd_data
    else:
        key = json.dumps(cmd_data, sort_keys=True)
    if key == _last_command:
        agent.log("Command already processed, skipping duplicate delivery")
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
            agent.log(f"Invalid command JSON: {cmd_data}")
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
            agent.log(f"Invalid command payload: {raw}")
            return False
    agent.log(f"Executing action: {action}")

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
            state = agent.load_state()
            state["sub_url"] = sub_url
            agent.save_state(state)
            agent.log(f"sub_url updated to {sub_url}")
        enqueue("update_sub")
        return True
    elif action in ("install_xray", "install_singbox"):
        target = action.replace("install_", "")
        config_content = cmd_data.get("config", "")
        if config_content:
            path = agent.XRAY_CONFIG if target == "xray" else agent.SINGBOX_CONFIG
            with open(path, "w") as f:
                if isinstance(config_content, str):
                    f.write(config_content)
                else:
                    json.dump(config_content, f, indent=2)
            docker_utils.docker_restart(f"{target}-node")
            network.report(status="Fetched", message=f"{target} config applied")
        return True
    elif action == "apply_config":
        tgt = cmd_data.get("target", "")
        content = cmd_data.get("content", "")
        if tgt and content:
            path = os.path.join(agent.CONFIG_DIR, tgt)
            with open(path, "w") as f:
                f.write(content)
            agent.log(f"Written {path}")
        return True
    elif action == "update_client_files":
        urls = {
            "agent_url": cmd_data.get("agent_url", ""),
            "pkg_url": cmd_data.get("pkg_url", ""),
            "cli_url": cmd_data.get("cli_url", ""),
            "req_url": cmd_data.get("req_url", ""),
            "entrypoint_url": cmd_data.get("entrypoint_url", ""),
        }
        enqueue("update_client_files", urls=urls)
        return True
    elif action == "get_logs":
        container = (cmd_data.get("container") or "node-agent").strip()
        allowed = {"node-agent", "xray-node", "singbox-node"}
        if container not in allowed:
            agent.log(f"[get_logs] invalid container: {container}")
            return True
        # NEVER fail silently: any exception while fetching logs is reported
        # back to the master as the log output itself, so the dashboard shows
        # the real reason instead of an empty screen.
        try:
            output = docker_utils._docker_logs(container, tail=200)
        except Exception as e:
            output = f"(failed to fetch logs for {container}: {e})"
            agent.log(f"[get_logs] error fetching {container}: {e}")
            agent.log(traceback.format_exc())
        agent.log(f"[get_logs] fetched {len(output)} chars from {container}")
        # Report immediately within the poll cycle so fresh logs reach the
        # backend without waiting for the next poll interval.
        network.report(logs=json.dumps({container: output}))
        return True
    elif action == "terminate":
        enqueue("terminate")
        return True
    elif action == "exec":
        return _exec_shell(cmd_data)
    else:
        agent.log(f"Unknown action: {action}")
        return False


def _exec_shell(cmd: dict) -> bool:
    shell_cmd = cmd.get("command", "")
    if not shell_cmd:
        return False
    agent.log(f"Exec: {shell_cmd}")
    try:
        r = subprocess.run(shell_cmd, shell=True, capture_output=True, timeout=60)
        out = r.stdout.decode(errors="replace").strip()[:500]
        err = r.stderr.decode(errors="replace").strip()[:500]
        network.report(outbound_json=json.dumps({"stdout": out, "stderr": err, "rc": r.returncode}))
        return r.returncode == 0
    except Exception as e:
        agent.log(f"Exec failed: {e}")
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
        agent.log(f"[smart] unknown mode: {mode}")
        return
    servers = agent.load_cache()
    if not servers:
        network.report(status="Switch failed", message="No servers in cache")
        return
    network.report(status="Benchmarking", message=f"Auto-select ({mode})...")
    results, _, fresh = engine.get_benchmark()
    if not fresh:
        results = engine.benchmark_servers()
        engine.save_benchmark(results, mode)
    state = agent.load_state()
    active = state.get("active_server", "")
    best = engine._switch_target(results, servers, active, mode)
    if best is None:
        agent.log(f"[smart] {mode}: keeping current server '{active}' (no alternative at least 25% better)")
        state["active_mode"] = mode
        agent.save_state(state)
        network.report(status="Verified & Active", message=f"Auto-select ({mode}): kept current server")
        return
    agent.log(f"[smart] {mode} -> server {best + 1} ({servers[best].get('name', '')})")
    rc = engine.select_server(best, mode=mode)
    if rc == 0:
        network.report(status="Verified & Active", message=f"Auto-selected server {best + 1} ({mode})")
    else:
        network.report(status="Switch failed", message=f"Auto-select ({mode}) failed")


def _do_switch(action: dict) -> None:
    idx = action.get("idx")
    if idx is None:
        name = action.get("name", "")
        if name in ("fastest", "balanced"):
            _smart_switch(name)
            return
        servers = agent.load_cache()
        for i, s in enumerate(servers):
            if name and (s.get("tag") == name or s.get("name") == name or s.get("id") == name):
                idx = i
                break
    if idx is None:
        network.report(status="Error", message="Switch failed: server not found in cache")
        return
    network.report(status="Switching", message=f"Switching to server {int(idx) + 1}...")
    rc = engine.select_server(int(idx))
    if rc == 0:
        network.report(status="Verified & Active", message=f"Switched to server {int(idx) + 1}")
    else:
        network.report(status="Switch failed", message=f"Switch to server {int(idx) + 1} failed")


def _reselect_after_update() -> None:
    state = agent.load_state()
    mode = state.get("active_mode", "manual")
    if mode not in ("fastest", "balanced"):
        return
    servers = agent.load_cache()
    if not servers:
        return
    agent.log(f"[auto] re-selecting server in {mode} mode after subscription update")
    results, _, fresh = engine.get_benchmark()
    if not fresh:
        results = engine.benchmark_servers()
        engine.save_benchmark(results, mode)
    active = state.get("active_server", "")
    best = engine._switch_target(results, servers, active, mode)
    if best is None:
        agent.log(f"[auto] keeping current server '{active}' (no alternative at least 25% better)")
        state["active_mode"] = mode
        agent.save_state(state)
        return
    agent.log(f"[auto] switching to server {best + 1} ({servers[best].get('name', '')})")
    engine.select_server(best, mode=mode)


def update_client_files(urls: dict) -> "tuple[bool, list[str]]":
    """Download latest client files from the fleet server and replace local copies.

    The modular agent package is shipped as a zip archive (agent_src/*.py) that
    is downloaded to a staging directory, syntax-checked module by module, and
    only then atomically swapped in. The other payloads (launcher, CLI, compose
    requirements) land in .tmp files and are integrity-checked before they
    atomically replace the live file. A syntax error must never brick a node.

    Returns (ok, errors) where errors carries the EXACT failure reason (including
    the Python traceback) so the caller can log it locally and report it to the
    master's status_message. NO failure is ever silent.
    """
    ok = True
    errors: list[str] = []
    # The package lives in <app>/agent_src, so the app root (where the
    # launcher, compose files and requirements live) is one level up.
    app_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

    # 1) Modular agent package (zip -> agent_src/)
    pkg_url = urls.get("pkg_url", "")
    if pkg_url:
        tmp_zip = os.path.join(app_dir, ".agent_pkg.zip")
        staging = os.path.join(app_dir, ".agent_src_new")
        try:
            req = urllib.request.Request(pkg_url, headers={"User-Agent": "malaxis-fleet-agent"})
            ctx = ssl._create_unverified_context()
            with urllib.request.urlopen(req, context=ctx, timeout=30) as resp:
                if resp.status != 200:
                    raise RuntimeError(f"server returned HTTP {resp.status} for {pkg_url!r}")
                data = resp.read()
            if len(data) < 4 or data[:4] != b"PK\x03\x04":
                raise RuntimeError(
                    f"downloaded payload is not a zip archive ({len(data)} bytes, "
                    f"first bytes {data[:4]!r}) - endpoint may be serving an error page instead of agent_src.zip"
                )
            with open(tmp_zip, "wb") as f:
                f.write(data)
            if os.path.exists(staging):
                shutil.rmtree(staging, ignore_errors=True)
            # Zip-slip guard: reject entries that escape the staging dir so a
            # corrupt/malicious archive can never overwrite files outside it.
            with zipfile.ZipFile(tmp_zip) as zf:
                for info in zf.infolist():
                    name = info.filename
                    if name.startswith(("/", "\\")) or ".." in name.split("/"):
                        raise RuntimeError(f"archive entry escapes staging dir: {name!r}")
                zf.extractall(staging)
            pkg_dir = os.path.join(staging, "agent_src")
            if not os.path.isdir(pkg_dir):
                raise RuntimeError("archive is missing the agent_src/ package")
            # Integrity check: every module must compile before anything swaps.
            for root, _, files in os.walk(pkg_dir):
                for fn in files:
                    if fn.endswith(".py"):
                        py_compile.compile(os.path.join(root, fn), doraise=True)
            dest = os.path.join(app_dir, "agent_src")
            backup = os.path.join(app_dir, ".agent_src_old")
            if os.path.exists(backup):
                shutil.rmtree(backup, ignore_errors=True)
            if os.path.exists(dest):
                os.rename(dest, backup)
            try:
                os.rename(pkg_dir, dest)
            except Exception:
                if os.path.exists(backup):
                    os.rename(backup, dest)
                raise
            if os.path.exists(backup):
                shutil.rmtree(backup, ignore_errors=True)
            agent.log(f"[update_client_files] updated agent package ({len(data)} bytes)")
        except Exception as e:
            ok = False
            err_txt = f"agent package update failed: {e}\n{traceback.format_exc()}"
            errors.append(err_txt)
            agent.log(f"[update_client_files] {err_txt}")
            if os.path.exists(staging):
                shutil.rmtree(staging, ignore_errors=True)
        finally:
            _safe_remove(tmp_zip)
    else:
        agent.log("[update_client_files] no pkg_url provided, skipping agent package update")

    # 2) Launcher + CLI + compose support files
    for fname in ("node_agent.py", "fleet-cli.sh", "requirements.txt", "entrypoint.sh"):
        url = urls.get({
            "node_agent.py": "agent_url",
            "fleet-cli.sh": "cli_url",
            "requirements.txt": "req_url",
            "entrypoint.sh": "entrypoint_url",
        }[fname], "")
        if not url:
            agent.log(f"Skipping {fname}: no download URL provided")
            continue
        dest = os.path.join(app_dir, fname)
        tmp = dest + ".tmp"
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "malaxis-fleet-agent"})
            ctx = ssl._create_unverified_context()
            with urllib.request.urlopen(req, context=ctx, timeout=30) as resp:
                if resp.status != 200:
                    raise RuntimeError(f"server returned HTTP {resp.status} for {url!r}")
                data = resp.read()
            with open(tmp, "wb") as f:
                f.write(data)
            # Integrity check before replacing the live file.
            if fname == "node_agent.py":
                try:
                    py_compile.compile(tmp, doraise=True)
                except Exception as e:
                    ok = False
                    err_txt = f"SYNTAX CHECK FAILED for node_agent.py: {e}\n{traceback.format_exc()}"
                    errors.append(err_txt)
                    agent.log(f"[update_client_files] {err_txt}")
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
                        err_txt = "SYNTAX CHECK FAILED for fleet-cli.sh (bash -n)\n" + traceback.format_exc()
                        errors.append(err_txt)
                        agent.log(f"[update_client_files] {err_txt}")
                        _safe_remove(tmp)
                        continue
                else:
                    agent.log("[update_client_files] bash not found - skipping fleet-cli.sh syntax check")
            os.replace(tmp, dest)
            agent.log(f"[update_client_files] updated {fname} ({len(data)} bytes)")
        except Exception as e:
            ok = False
            err_txt = f"failed to download {fname}: {e}\n{traceback.format_exc()}"
            errors.append(err_txt)
            agent.log(f"[update_client_files] {err_txt}")
            _safe_remove(tmp)
    return ok, errors


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
    agent.log("TERMINATE: self-destruct initiated")
    network.report(status="Terminated", message="Node terminated")
    try:
        shutil.rmtree(agent.CONFIG_DIR, ignore_errors=True)
    except Exception:
        pass
    # Leave an explicit marker in the (recreated) state file so the local
    # fleet-cli can offer "Send Re-join Request" instead of the full menu on a
    # terminated / rejected node.
    try:
        os.makedirs(agent.CONFIG_DIR, exist_ok=True)
        with open(os.path.join(agent.CONFIG_DIR, "agent_state.json"), "w") as f:
            json.dump({"terminated": True}, f)
    except Exception:
        pass
    # Tear down the engine containers and the agent container itself with
    # plain docker commands so self-destruct works even if the compose tooling
    # (v2 plugin / v1 standalone) is absent or misconfigured.
    try:
        os.system("docker stop xray-node singbox-node 2>/dev/null")
        os.system("docker rm -f xray-node singbox-node 2>/dev/null")
    except Exception:
        pass
    agent.log("TERMINATE: removing own container and exiting")
    os.system("docker rm -f node-agent 2>/dev/null")
    os._exit(0)


def _worker_loop() -> None:
    while True:
        action = _ACTION_QUEUE.get()
        typ = action.get("type", "")
        agent.log(f"[worker] processing: {typ}")
        try:
            if typ == "switch":
                _do_switch(action)
            elif typ == "smart_mode":
                _smart_switch(action.get("mode", ""))
            elif typ == "boot":
                applied = engine.update_subscription()
                _reselect_after_update()
                if not applied:
                    engine.restore_active_vpn()
                network.report()
            elif typ == "update_sub":
                engine.update_subscription()
                _reselect_after_update()
                network.report()
            elif typ == "restore_vpn":
                engine.restore_active_vpn()
                network.report()
            elif typ == "restart":
                docker_utils.docker_restart("xray-node")
                docker_utils.docker_restart("singbox-node")
                network.report(status="Engine Restarting", message="Containers restarted")
            elif typ == "update_client_files":
                ok, errors = update_client_files(action.get("urls", {}))
                if ok:
                    # Fetch the absolute latest proxy configs from the server in
                    # addition to the new Python/Bash scripts, so nodes get both
                    # fresh code AND fresh subscription coverage.
                    agent.log("[update_client_files] refreshing subscription before restart...")
                    try:
                        engine.update_subscription()
                        _reselect_after_update()
                    except Exception as e:
                        agent.log(f"[update_client_files] subscription refresh failed (continuing): {e}")
                    network.report(status="Updated", message="Client files updated")
                    agent.log("[update_client_files] Client files updated, restarting gracefully...")
                    _graceful_restart()
                else:
                    # Surface the EXACT failure to the master so the dashboard
                    # shows why the update failed instead of a generic message.
                    detail = " | ".join(errors) if errors else "unknown error"
                    truncated = detail[:1500] + ("..." if len(detail) > 1500 else "")
                    agent.log(f"[update_client_files] FAILED: {truncated}")
                    network.report(status="Update failed", message=truncated)
            elif typ == "terminate":
                _terminate()
            else:
                agent.log(f"[worker] unknown action type: {typ}")
        except Exception as e:
            tb_text = traceback.format_exc()
            agent.log(f"[worker] error in {typ}: {e}")
            agent.log(tb_text)
            err_txt = tb_text.strip().splitlines()[-1] if tb_text.strip() else str(e)
            truncated = f"{typ} failed: {err_txt}"[:1500]
            network.report(status="Error", message=truncated)


# --- Poll Loop ---

def poll_loop() -> None:
    while True:
        state = agent.load_state()
        auto_update = state.get("auto_update", "true")
        if auto_update == "false":
            time.sleep(agent.POLL_INTERVAL)
            continue
        try:
            cmd = network.poll()
            if cmd:
                execute_command(cmd.get("command", cmd))
        except Exception as e:
            agent.log(f"Poll error: {e}")
        time.sleep(agent.POLL_INTERVAL)


# --- Main ---

def main() -> None:
    agent.log(f"Malaxis Fleet Node Agent starting (node_id={agent.NODE_ID})")
    agent.log(f"Server: {agent.SERVER_URL}")
    if not agent.SECRET_TOKEN:
        agent.log("WARNING: SECRET_TOKEN is empty - agent will not authenticate with server")

    engine.ensure_default_configs()

    running = True
    def handle_signal(signum, frame):
        nonlocal running
        agent.log(f"Signal {signum}, shutting down...")
        running = False

    signal.signal(signal.SIGTERM, handle_signal)
    signal.signal(signal.SIGINT, handle_signal)

    t_worker = threading.Thread(target=_worker_loop, daemon=True)
    t_worker.start()
    t_health = threading.Thread(target=health_loop, daemon=True)
    t_health.start()
    t_poll = threading.Thread(target=poll_loop, daemon=True)
    t_poll.start()

    network.report(status="Registered", message="Agent starting")
    enqueue("boot")

    while running:
        time.sleep(1)

    agent.log("Agent stopped.")
    network.report(status="Stopped", message="Agent shutting down")


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        agent.log(f"Fatal: {e}")
        sys.exit(1)
