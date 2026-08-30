"""Bounded, shell-free Veto subprocess adapter for Hermes Agent."""

from __future__ import annotations

import json
import math
import os
import re
import signal
import subprocess
import tempfile
import threading
import uuid
from dataclasses import dataclass
from typing import Any

PLUGIN_API_VERSION = 1
MAX_OBJECTIVE_BYTES = 32 * 1024
MAX_RESPONSE_BYTES = 1024 * 1024
_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")


def _encode(value: dict[str, Any]) -> str:
    return json.dumps(value, separators=(",", ":"), ensure_ascii=False)


def _error(code: str, message: str, **extra: Any) -> str:
    return _encode({"ok": False, "error": code, "message": message, **extra})


def _objective(args: dict[str, Any]) -> str:
    value = args.get("objective")
    if not isinstance(value, str) or not value.strip():
        raise ValueError("objective must be a non-empty string")
    value = value.strip()
    if len(value.encode("utf-8")) > MAX_OBJECTIVE_BYTES:
        raise ValueError("objective is too large")
    return value


def _number(args: dict[str, Any], key: str, minimum: float, maximum: float) -> float | None:
    value = args.get(key)
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{key} must be a number")
    value = float(value)
    if not math.isfinite(value) or value < minimum or value > maximum:
        raise ValueError(f"{key} must be between {minimum:g} and {maximum:g}")
    return value


def _choice(args: dict[str, Any], key: str, choices: set[str]) -> str | None:
    value = args.get(key)
    if value is None:
        return None
    if not isinstance(value, str) or value not in choices:
        raise ValueError(f"{key} must be one of {', '.join(sorted(choices))}")
    return value


@dataclass
class CommandResult:
    execution_id: str
    returncode: int
    stdout: str
    timed_out: bool = False
    cancelled: bool = False
    oversized: bool = False


class ProcessRunner:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._active: dict[str, subprocess.Popen[bytes]] = {}

    @staticmethod
    def binary() -> str:
        return os.environ.get("VETO_BINARY", "veto")

    def run(
        self,
        arguments: list[str],
        *,
        timeout: float,
        execution_id: str | None = None,
    ) -> CommandResult:
        identifier = execution_id or uuid.uuid4().hex
        if not _ID_RE.fullmatch(identifier):
            raise ValueError("execution_id must contain only letters, digits, dot, underscore, or hyphen")
        timeout = max(1.0, min(float(timeout), 600.0))
        env = os.environ.copy()
        env["VETO_ROUTING_ORIGIN"] = "hermes-plugin"
        popen_kwargs: dict[str, Any] = {}
        if os.name == "posix":
            popen_kwargs["start_new_session"] = True
        elif hasattr(subprocess, "CREATE_NEW_PROCESS_GROUP"):
            popen_kwargs["creationflags"] = subprocess.CREATE_NEW_PROCESS_GROUP
        with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
            process = subprocess.Popen(
                [self.binary(), *arguments],
                stdin=subprocess.DEVNULL,
                stdout=stdout,
                stderr=stderr,
                env=env,
                shell=False,
                **popen_kwargs,
            )
            with self._lock:
                if identifier in self._active:
                    self._terminate(process)
                    raise ValueError("execution_id is already active")
                self._active[identifier] = process
            timed_out = False
            try:
                process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                timed_out = True
                self._terminate(process)
            finally:
                with self._lock:
                    self._active.pop(identifier, None)
            stdout.seek(0)
            output = stdout.read(MAX_RESPONSE_BYTES + 1)
            oversized = len(output) > MAX_RESPONSE_BYTES
            return CommandResult(
                execution_id=identifier,
                returncode=process.returncode if process.returncode is not None else -1,
                stdout=output[:MAX_RESPONSE_BYTES].decode("utf-8", errors="replace"),
                timed_out=timed_out,
                cancelled=(process.returncode is not None and process.returncode < 0 and not timed_out),
                oversized=oversized,
            )

    def cancel(self, execution_id: str) -> bool:
        if not isinstance(execution_id, str) or not _ID_RE.fullmatch(execution_id):
            raise ValueError("execution_id is invalid")
        with self._lock:
            process = self._active.get(execution_id)
        if process is None:
            return False
        self._terminate(process)
        return True

    @staticmethod
    def _terminate(process: subprocess.Popen[bytes]) -> None:
        if process.poll() is not None:
            return
        try:
            if os.name == "posix":
                os.killpg(process.pid, signal.SIGTERM)
            else:
                process.terminate()
            process.wait(timeout=2)
        except (OSError, subprocess.TimeoutExpired):
            try:
                if os.name == "posix":
                    os.killpg(process.pid, signal.SIGKILL)
                else:
                    process.kill()
                process.wait(timeout=2)
            except (OSError, subprocess.TimeoutExpired):
                pass


class VetoRuntime:
    def __init__(self, runner: ProcessRunner | None = None) -> None:
        self.runner = runner or ProcessRunner()
        self._state_lock = threading.Lock()
        self._automatic_routing_preference = True

    def _invoke_json(self, arguments: list[str], timeout: float) -> dict[str, Any]:
        try:
            result = self.runner.run(arguments, timeout=timeout)
        except FileNotFoundError:
            return {"ok": False, "error": "veto_not_found", "message": "Veto is not installed or is not on PATH. Install Veto, then restart Hermes."}
        except (OSError, ValueError):
            return {"ok": False, "error": "veto_start_failed", "message": "Veto could not be started safely."}
        if result.timed_out:
            return {"ok": False, "error": "timeout", "message": "Veto did not finish before the timeout."}
        if result.oversized:
            return {"ok": False, "error": "response_too_large", "message": "Veto returned more data than the plugin limit."}
        if result.returncode != 0:
            return {"ok": False, "error": "veto_failed", "message": "Veto returned a non-zero exit status.", "exit_code": result.returncode}
        try:
            value = json.loads(result.stdout)
        except (json.JSONDecodeError, UnicodeError):
            return {"ok": False, "error": "invalid_response", "message": "Veto returned malformed JSON."}
        if not isinstance(value, dict):
            return {"ok": False, "error": "invalid_response", "message": "Veto returned an unexpected JSON value."}
        return value

    def status(self, _args: dict[str, Any], **_kwargs: Any) -> str:
        value = self._invoke_json(["hermes", "api", "--json"], 5)
        with self._state_lock:
            preference = self._automatic_routing_preference
        routing_state = {
            "automatic_routing": False,
            "automatic_routing_preference": preference,
        }
        if value.get("error") in {"veto_failed", "invalid_response"}:
            return _error(
                "incompatible_veto",
                f"This plugin requires Veto Hermes API {PLUGIN_API_VERSION}; update Veto and reinstall the plugin.",
                required_api_version=PLUGIN_API_VERSION,
                **routing_state,
            )
        if not value.get("ok", True):
            value.update(routing_state)
            return _encode(value)
        api_version = value.get("api_version")
        if api_version != PLUGIN_API_VERSION:
            return _error(
                "incompatible_veto",
                f"This plugin requires Veto Hermes API {PLUGIN_API_VERSION}; update Veto and reinstall the plugin.",
                api_version=api_version,
                required_api_version=PLUGIN_API_VERSION,
                **routing_state,
            )
        veto_version = value.get("version")
        if not isinstance(veto_version, str) or len(veto_version) > 64:
            veto_version = "unknown"
        return _encode({
            "ok": True,
            "available": True,
            "api_version": PLUGIN_API_VERSION,
            "version": veto_version,
            **routing_state,
        })

    @staticmethod
    def _routing_arguments(args: dict[str, Any], timeout: float) -> list[str]:
        objective = _objective(args)
        command = ["route", "--json", "--no-resume", "--timeout", f"{timeout:g}s"]
        kind = _choice(args, "kind", {"extract", "summarize", "code-change", "debug", "plan", "review", "refactor"})
        risk = _choice(args, "risk", {"low", "medium", "high"})
        max_cost = _number(args, "max_cost_usd", 0, 1000000)
        if kind:
            command.extend(["--kind", kind])
        if risk:
            command.extend(["--risk", risk])
        if max_cost is not None:
            command.extend(["--max-cost", str(max_cost)])
        command.extend(["--task", objective])
        return command

    def route(self, args: dict[str, Any], **_kwargs: Any) -> str:
        try:
            timeout = _number(args, "timeout_seconds", 1, 300) or 30
            total_timeout = min(timeout * 3 + 5, 600)
            return _encode(self._invoke_json(self._routing_arguments(args, timeout), total_timeout))
        except ValueError as exc:
            return _error("invalid_arguments", str(exc))

    def models(self, args: dict[str, Any], **_kwargs: Any) -> str:
        if set(args) - {"offline"} or ("offline" in args and not isinstance(args["offline"], bool)):
            return _error("invalid_arguments", "offline must be a boolean")
        command = ["models", "--json"]
        if args.get("offline") is True:
            command.append("--offline")
        return _encode(self._invoke_json(command, 15))

    def cost(self, args: dict[str, Any], **_kwargs: Any) -> str:
        try:
            timeout = _number(args, "timeout_seconds", 1, 300) or 30
            total_timeout = min(timeout * 3 + 5, 600)
            route = self._invoke_json(self._routing_arguments(args, timeout), total_timeout)
        except ValueError as exc:
            return _error("invalid_arguments", str(exc))
        if route.get("error"):
            return _encode(route)
        return _encode({
            "ok": True,
            "estimate": True,
            "model": route.get("model"),
            "provider": route.get("provider"),
            "api_model": route.get("api_model"),
            "runtime": route.get("runtime"),
            "estimated_savings_usd": route.get("saved_usd"),
            "message": "This is Veto's routing savings estimate, not provider billing.",
        })

    def run(self, args: dict[str, Any], **_kwargs: Any) -> str:
        try:
            objective = _objective(args)
            timeout = _number(args, "timeout_seconds", 1, 600) or 120
            max_cost = _number(args, "max_cost_usd", 0, 1000000)
            kind = _choice(args, "kind", {"extract", "summarize", "code-change", "debug", "plan", "review", "refactor"})
            risk = _choice(args, "risk", {"low", "medium", "high"})
            execution_id = args.get("execution_id")
            command = ["run", "--quiet", "--no-feedback", "--timeout", f"{timeout}s"]
            if kind:
                command.extend(["--kind", kind])
            if risk:
                command.extend(["--risk", risk])
            if max_cost is not None:
                command.extend(["--max-cost", str(max_cost)])
            command.extend(["--task", objective])
            result = self.runner.run(command, timeout=timeout + 5, execution_id=execution_id)
        except FileNotFoundError:
            return _error("veto_not_found", "Veto is not installed or is not on PATH. Install Veto, then restart Hermes.")
        except ValueError as exc:
            return _error("invalid_arguments", str(exc))
        except OSError:
            return _error("veto_start_failed", "Veto could not be started safely.")
        if result.timed_out:
            return _error("timeout", "Veto did not finish before the timeout.", execution_id=result.execution_id)
        if result.oversized:
            return _error("response_too_large", "Veto returned more data than the plugin limit.", execution_id=result.execution_id)
        if result.returncode != 0:
            return _error("veto_failed", "Veto returned a non-zero exit status.", execution_id=result.execution_id, exit_code=result.returncode)
        return _encode({"ok": True, "execution_id": result.execution_id, "output": result.stdout})

    def cancel(self, args: dict[str, Any], **_kwargs: Any) -> str:
        execution_id = args.get("execution_id")
        try:
            cancelled = self.runner.cancel(execution_id)
        except ValueError as exc:
            return _error("invalid_arguments", str(exc))
        if not cancelled:
            return _error("not_active", "No active Veto execution has that ID.", execution_id=execution_id)
        return _encode({"ok": True, "cancelled": True, "execution_id": execution_id})

    def _set_automatic(self, enabled: bool) -> str:
        with self._state_lock:
            self._automatic_routing_preference = enabled
        state = "enabled" if enabled else "disabled"
        return (
            f"Veto automatic-routing preference is {state} for this Hermes process. "
            "This plugin version is explicit-only; its tools remain available."
        )

    def command_veto(self, raw_args: str) -> str:
        action = raw_args.strip().lower()
        if action == "off":
            return self._set_automatic(False)
        if action == "on":
            return self._set_automatic(True)
        if action not in {"", "status"}:
            return "Usage: /veto [status|on|off]"
        value, error = self._command_value(self.status({}))
        if error:
            return error
        preference = "on" if value.get("automatic_routing_preference") else "off"
        return (
            f"Veto {self._display(value.get('version', 'unknown'))} is ready "
            f"(plugin API {value.get('api_version', 'unknown')}). "
            "Automatic routing is not active in this explicit-only plugin; "
            f"the next-middleware preference is {preference}."
        )

    def command_models(self, raw_args: str) -> str:
        if raw_args.strip():
            return "Usage: /models"
        value, error = self._command_value(self.models({}))
        if error:
            return error
        models = value.get("models")
        if not isinstance(models, list):
            return "Veto returned an invalid model list."
        lines = [f"Models available through Veto ({len(models)}):"]
        for model in models[:20]:
            if not isinstance(model, dict):
                continue
            name = self._display(model.get("name", "unknown"))
            provider = self._display(model.get("provider", "unknown"))
            runtime = self._display(model.get("runtime", "unknown"))
            lines.append(f"- {name} · {provider} · {runtime}")
        if len(models) > 20:
            lines.append(f"… and {len(models) - 20} more. Run `veto models` for the full list.")
        return "\n".join(lines)

    def command_route(self, raw_args: str) -> str:
        value, error = self._command_value(self.route({"objective": raw_args}))
        if error:
            return error
        model = self._display(value.get("model", "unknown"))
        provider = self._display(value.get("provider", "unknown"))
        runtime = self._display(value.get("runtime", "unknown"))
        confidence = value.get("confidence")
        confidence_text = f" at {confidence:.0%} confidence" if isinstance(confidence, (int, float)) else ""
        return f"Veto selected {model} ({provider} via {runtime}){confidence_text}."

    def command_cost(self, raw_args: str) -> str:
        value, error = self._command_value(self.cost({"objective": raw_args}))
        if error:
            return error
        model = self._display(value.get("model", "unknown"))
        savings = value.get("estimated_savings_usd")
        if isinstance(savings, (int, float)) and math.isfinite(savings):
            estimate = f"${savings:.6f}"
        else:
            estimate = "unavailable"
        return f"Estimated routing savings with {model}: {estimate}. This is not provider billing."

    def command_off(self, raw_args: str) -> str:
        if raw_args.strip():
            return "Usage: /veto-off"
        return self._set_automatic(False)

    @staticmethod
    def _display(value: Any, limit: int = 96) -> str:
        text = str(value)
        text = "".join(character if character.isprintable() else " " for character in text)
        text = " ".join(text.split())
        return text[:limit] or "unknown"

    @classmethod
    def _command_value(cls, result: str) -> tuple[dict[str, Any], str | None]:
        try:
            value = json.loads(result)
        except (TypeError, json.JSONDecodeError):
            return {}, "Veto returned an invalid response."
        if not isinstance(value, dict):
            return {}, "Veto returned an invalid response."
        if value.get("error"):
            code = cls._display(value.get("error"), 48)
            message = cls._display(value.get("message", "The request failed."), 160)
            return {}, f"Veto error ({code}): {message}"
        return value, None

    def tools(self):
        objective = {"type": "string", "description": "Task objective"}
        routing_properties = {
            "objective": objective,
            "kind": {"type": "string", "enum": ["extract", "summarize", "code-change", "debug", "plan", "review", "refactor"]},
            "risk": {"type": "string", "enum": ["low", "medium", "high"]},
            "max_cost_usd": {"type": "number", "minimum": 0},
            "timeout_seconds": {"type": "number", "minimum": 1, "maximum": 300},
        }
        route_schema = {"type": "object", "properties": routing_properties, "required": ["objective"], "additionalProperties": False}
        run_properties = dict(routing_properties)
        run_properties["timeout_seconds"] = {"type": "number", "minimum": 1, "maximum": 600}
        run_properties["execution_id"] = {"type": "string", "description": "Caller-chosen ID used by veto_cancel"}
        tools = [
            ("veto_status", "Check Veto availability, compatibility, and integration state.", {"type": "object", "properties": {}, "additionalProperties": False}, self.status),
            ("veto_route", "Ask Veto to choose a model without executing the task.", route_schema, self.route),
            ("veto_run", "Route and execute a task through Veto.", {"type": "object", "properties": run_properties, "required": ["objective"], "additionalProperties": False}, self.run),
            ("veto_models", "List Veto models and their effective runtime capabilities.", {"type": "object", "properties": {"offline": {"type": "boolean"}}, "additionalProperties": False}, self.models),
            ("veto_cost", "Estimate routing savings without executing the task.", route_schema, self.cost),
            ("veto_cancel", "Cancel one active plugin-owned Veto execution by ID.", {"type": "object", "properties": {"execution_id": {"type": "string"}}, "required": ["execution_id"], "additionalProperties": False}, self.cancel),
        ]
        return [(name, description, schema, self._safe_tool(handler)) for name, description, schema, handler in tools]

    def commands(self):
        commands = [
            ("veto", "Show Veto status or toggle automatic routing.", "[status|on|off]", self.command_veto),
            ("models", "List models available through Veto.", "", self.command_models),
            ("route", "Choose a model for an objective.", "<objective>", self.command_route),
            ("cost", "Estimate routing savings for an objective.", "<objective>", self.command_cost),
            ("veto-off", "Disable automatic Veto routing for this Hermes process.", "", self.command_off),
        ]
        return [(name, description, hint, self._safe_command(handler)) for name, description, hint, handler in commands]

    @staticmethod
    def _safe_tool(handler):
        def wrapped(args: dict[str, Any], **kwargs: Any) -> str:
            if not isinstance(args, dict):
                return _error("invalid_arguments", "tool arguments must be an object")
            try:
                return handler(args, **kwargs)
            except Exception:
                return _error("plugin_error", "The Veto plugin could not complete this request safely.")

        return wrapped

    @staticmethod
    def _safe_command(handler):
        def wrapped(raw_args: str) -> str:
            if not isinstance(raw_args, str):
                return "The Veto command received invalid arguments."
            try:
                return handler(raw_args)
            except Exception:
                return "The Veto plugin could not complete this command safely."

        return wrapped
