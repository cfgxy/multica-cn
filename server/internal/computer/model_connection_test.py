import json
import os
import pwd
import re
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import uuid

PROMPT = "Reply with exactly MULTICA_CONNECTION_OK. Do not use tools or inspect files."


def main(home=None, executable=None):
    start = time.monotonic()
    proc = None
    work = None
    def interrupted(signum, frame):
        raise ValueError("model_test_timeout")
    for sig in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
        signal.signal(sig, interrupted)
    result = {"status": "failed", "error_code": "model_connection_failed", "duration_ms": 0}
    try:
        request = json.load(sys.stdin)
        home = home or pwd.getpwuid(os.getuid()).pw_dir
        kind = request["kind"]
        model = request["model_selector"]
        if not isinstance(model, str) or not model:
            raise ValueError("model_connection_failed")
        env = {"HOME": home, "USER": pwd.getpwuid(os.getuid()).pw_name,
               "LANG": "C.UTF-8", "XDG_CONFIG_HOME": home + "/.config",
               "PATH": home + "/.local/bin:" + home + "/.kimi-code/bin:" + home + "/.grok/bin:/usr/local/bin:/usr/bin:/bin",
               "NO_COLOR": "1"}
        # A managed daemon EnvironmentFile can override native CLI files. Detect
        # it rather than reporting success with a different authentication source.
        model_env = os.path.join(home, ".config/multica-provision/model.env")
        if os.path.exists(model_env):
            with open(model_env) as f:
                providers = {"claude": "ANTHROPIC", "codex": "OPENAI|CODEX", "grok": "XAI|GROK|OPENAI", "kimi": "KIMI|MOONSHOT|OPENAI", "opencode": "OPENCODE|OPENAI|ANTHROPIC", "omp": "OMP|OPENAI|ANTHROPIC|OPENROUTER", "pi": "PI|OPENAI|ANTHROPIC|OPENROUTER", "openclaw": "OPENCLAW|OPENAI|ANTHROPIC"}.get(kind, "")
                if any(re.match(r"\s*(" + providers + r")_\w+\s*=", line) for line in f):
                    raise ValueError("model_environment_conflict")
        binary = executable or shutil.which(kind, path=env["PATH"])
        if not binary:
            raise ValueError("cli_missing")
        acp = kind in ("kimi", "grok")
        args = {
            "claude": ["-p", "--output-format", "json", "--tools", "", "--strict-mcp-config",
                       "--mcp-config", '{"mcpServers":{}}', "--no-session-persistence",
                       "--settings", '{"disableAllHooks":true}', PROMPT],
            "codex": ["exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only",
                      "--ephemeral", "-c", "features.shell_tool=false", "-c", 'web_search="disabled"', PROMPT],
            "pi": ["--print", "--mode", "json", "--no-session", "--no-tools",
                   "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-context-files", PROMPT],
            "omp": ["--print", "--mode", "json", "--no-session", "--no-tools", PROMPT],
            "opencode": ["run", "--format", "json", PROMPT],
            "kimi": ["acp"],
            "grok": ["--no-auto-update", "agent", "stdio"],
            "openclaw": ["agent", "--local", "--json", "--session-id", str(uuid.uuid4()), "--message", PROMPT],
        }.get(kind)
        if args is None:
            raise ValueError("model_test_unsupported")
        if kind in ("claude", "codex", "pi", "omp", "opencode"):
            # The prompt is the final positional argument in these adapters.
            args[-1:-1] = ["--model", model]
        env["OPENCODE_PERMISSION"] = '{"*":"deny"}'
        env["OPENCODE_DISABLE_AUTOUPDATE"] = "true"
        env["OPENCODE_DISABLE_DEFAULT_PLUGINS"] = "true"
        work = tempfile.mkdtemp(prefix=".multica-model-test-", dir=home)
        if kind == "openclaw":
            # This CLI has no --model flag. Pin a temporary agent to the saved
            # selector and use the same saved provider settings, without hooks.
            with open(os.path.join(home, ".openclaw/openclaw.json")) as f:
                saved = json.load(f)
            pinned = {"models": saved["models"], "agents": {"defaults": {"model": {"primary": model}},
                      "list": [{"id": "multica-config-test", "model": model}]},
                      "tools": {"deny": ["*"]}}
            config_path = os.path.join(work, "openclaw.json")
            fd = os.open(config_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as f:
                json.dump(pinned, f)
            env["OPENCLAW_CONFIG_PATH"] = config_path
            args.extend(["--agent", "multica-config-test"])
        if work:
            proc = subprocess.Popen([binary] + args, cwd=work, env=env, stdin=subprocess.PIPE,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            def send(data):
                proc.stdin.write((json.dumps(data) + "\n").encode())
                proc.stdin.flush()
            if acp:
                send({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
                    "protocolVersion": 1, "clientInfo": {"name": "multica-config-test", "version": "1"},
                    "clientCapabilities": {}}})
            else:
                proc.stdin.close()
            output = bytearray()
            stderr = bytearray()
            streams = {proc.stdout.fileno(): output, proc.stderr.fileno(): stderr}
            text_reply = False
            terminal = False
            failed = False
            session_id = None
            reply = ""
            stdout_bytes = 0
            stderr_bytes = 0
            while streams:
                if time.monotonic() - start > 60:
                    raise ValueError("model_test_timeout")
                readable, _, _ = select.select(list(streams), [], [], 0.2)
                for fd in readable:
                    chunk = os.read(fd, 8192)
                    if not chunk:
                        del streams[fd]
                        continue
                    streams[fd].extend(chunk)
                    if fd == proc.stdout.fileno():
                        stdout_bytes += len(chunk)
                        if stdout_bytes > 262144:
                            raise ValueError("model_test_output_limit")
                    else:
                        stderr_bytes += len(chunk)
                        if stderr_bytes > 65536:
                            raise ValueError("model_test_output_limit")
                    if fd != proc.stdout.fileno() or kind == "openclaw":
                        continue
                    while b"\n" in output:
                        line, _, rest = output.partition(b"\n")
                        output[:] = rest
                        try:
                            event = json.loads(line)
                        except (ValueError, UnicodeError):
                            continue
                        if not isinstance(event, dict):
                            continue
                        if acp:
                            method = event.get("method", "")
                            if "id" in event and method:
                                if method == "session/request_permission":
                                    send({"jsonrpc": "2.0", "id": event["id"], "result": {"outcome": {"outcome": "cancelled"}}})
                                else:
                                    send({"jsonrpc": "2.0", "id": event["id"], "error": {"code": -32601, "message": "Not supported"}})
                            elif event.get("id") == 1:
                                if event.get("error"):
                                    raise ValueError("model_cli_protocol_failed")
                                send({"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": {"cwd": work, "mcpServers": []}})
                            elif event.get("id") == 2:
                                session_id = event.get("result", {}).get("sessionId")
                                if not session_id:
                                    raise ValueError("model_cli_protocol_failed")
                                send({"jsonrpc": "2.0", "id": 4, "method": "session/set_model", "params": {
                                    "sessionId": session_id, "modelId": model}})
                            elif event.get("id") == 4:
                                if event.get("error"):
                                    raise ValueError("model_cli_protocol_failed")
                                send({"jsonrpc": "2.0", "id": 3, "method": "session/prompt", "params": {
                                    "sessionId": session_id, "prompt": [{"type": "text", "text": PROMPT}]}})
                            elif event.get("id") == 3:
                                failed = bool(event.get("error"))
                                terminal = event.get("result", {}).get("stopReason") == "end_turn"
                            elif method == "session/update":
                                update = event.get("params", {}).get("update", {})
                                if update.get("sessionUpdate") == "agent_message_chunk":
                                    reply += update.get("content", {}).get("text", "")
                                    text_reply = "MULTICA_CONNECTION_OK" in reply
                        else:
                            typ = event.get("type")
                            if kind == "claude" and typ == "result":
                                terminal = not event.get("is_error", True)
                                text_reply = "MULTICA_CONNECTION_OK" in str(event.get("result", ""))
                            elif kind == "codex":
                                if typ == "item.completed" and event.get("item", {}).get("type") == "agent_message":
                                    text_reply = text_reply or "MULTICA_CONNECTION_OK" in event["item"].get("text", "")
                                terminal = terminal or typ == "turn.completed"
                                failed = failed or typ in ("error", "turn.failed")
                            elif kind in ("pi", "omp") and typ == "message_end":
                                message = event.get("message", {})
                                if message.get("role") == "assistant":
                                    text_reply = text_reply or any("MULTICA_CONNECTION_OK" in x.get("text", "") for x in message.get("content", []) if isinstance(x, dict))
                                    terminal = message.get("stopReason") in ("stop", "end_turn")
                                    failed = failed or message.get("stopReason") == "error"
                            elif kind == "opencode":
                                if typ == "text":
                                    text_reply = text_reply or "MULTICA_CONNECTION_OK" in event.get("part", {}).get("text", "")
                                terminal = terminal or typ == "step_finish"
                                failed = failed or typ == "error"
                            elif kind == "openclaw":
                                # OpenClaw can emit a single JSON result; handle it below.
                                output[:] = line + b"\n" + output
                                break
                if acp and (terminal or failed):
                    break
                if proc.poll() is not None and not readable:
                    break
            if not acp:
                proc.wait(timeout=3)
                failed = failed or proc.returncode != 0
            if kind == "openclaw":
                try:
                    event = json.loads(output)
                    payloads = event.get("payloads", event.get("result", {}).get("payloads", []))
                    text_reply = any("MULTICA_CONNECTION_OK" in p.get("text", "") for p in payloads)
                    terminal = text_reply
                except (ValueError, TypeError):
                    pass
            if text_reply and terminal and not failed:
                result.update(status="succeeded", error_code="")
            else:
                # Return classification only, never provider or CLI output.
                combined = bytes(stderr).lower()
                if b"401" in combined or b"unauthorized" in combined or b"invalid api key" in combined:
                    result["error_code"] = "model_auth_failed"
                elif b"429" in combined or b"rate limit" in combined:
                    result["error_code"] = "model_rate_limited"
    except Exception as exc:
        allowed = {"model_environment_conflict", "cli_missing", "model_test_unsupported",
                   "model_test_timeout", "model_test_output_limit", "model_cli_protocol_failed"}
        if isinstance(exc, ValueError) and str(exc) in allowed:
            result["error_code"] = str(exc)
    finally:
        if proc is not None:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait()
        if work is not None:
            shutil.rmtree(work, ignore_errors=True)
        result["duration_ms"] = int((time.monotonic() - start) * 1000)
        print(json.dumps(result))


if __name__ == "__main__":
    main()

