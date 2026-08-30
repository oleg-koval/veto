from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import time
import unittest
from unittest.mock import patch


PLUGIN_DIR = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location(
    "veto_plugin",
    PLUGIN_DIR / "__init__.py",
    submodule_search_locations=[str(PLUGIN_DIR)],
)
assert SPEC and SPEC.loader
PLUGIN = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = PLUGIN
SPEC.loader.exec_module(PLUGIN)
RUNTIME = PLUGIN.VetoRuntime
COMMAND_RESULT = PLUGIN.runtime.CommandResult


class FakeRunner:
    def __init__(self, results=None):
        self.results = list(results or [])
        self.calls = []
        self.cancelled = set()

    def run(self, arguments, *, timeout, execution_id=None):
        self.calls.append((list(arguments), timeout, execution_id))
        if self.results:
            result = self.results.pop(0)
            if isinstance(result, Exception):
                raise result
            return result
        return COMMAND_RESULT(execution_id or "generated", 0, "{}")

    def cancel(self, execution_id):
        if execution_id == "bad id":
            raise ValueError("execution_id is invalid")
        return execution_id in self.cancelled


class FakeContext:
    def __init__(self):
        self.tools = {}
        self.commands = {}
        self.middleware = {}

    def register_tool(self, **kwargs):
        self.tools[kwargs["name"]] = kwargs

    def register_command(self, **kwargs):
        self.commands[kwargs["name"]] = kwargs

    def register_middleware(self, kind, callback):
        self.middleware[kind] = callback


class RuntimeContractTests(unittest.TestCase):
    def test_registration_contract(self):
        ctx = FakeContext()
        PLUGIN.register(ctx)
        self.assertEqual(
            set(ctx.tools),
            {"veto_status", "veto_route", "veto_run", "veto_models", "veto_cost", "veto_cancel"},
        )
        self.assertEqual(set(ctx.commands), {"veto", "models", "route", "cost", "veto-off"})
        self.assertEqual(set(ctx.middleware), {"turn_route"})
        for item in ctx.tools.values():
            self.assertEqual(item["toolset"], "veto")
            self.assertFalse(item.get("override", False))

    def test_route_passes_objective_as_one_argument_without_a_shell(self):
        objective = 'review; touch /tmp/never-created && echo "$TOKEN"'
        runner = FakeRunner([COMMAND_RESULT("route", 0, '{"model":"sol","saved_usd":0.01}')])
        value = json.loads(RUNTIME(runner).route({"objective": objective, "risk": "low"}))
        self.assertEqual(value["model"], "sol")
        argv, timeout, _ = runner.calls[0]
        self.assertEqual(argv[-2:], ["--task", objective])
        self.assertIn("30s", argv)
        self.assertEqual(timeout, 95)

    def test_status_handles_missing_and_incompatible_veto(self):
        missing = RUNTIME(FakeRunner([FileNotFoundError()]))
        self.assertEqual(json.loads(missing.status({}))["error"], "veto_not_found")
        incompatible = RUNTIME(FakeRunner([COMMAND_RESULT("status", 0, '{"api_version":2,"version":"9.0.0"}')]))
        value = json.loads(incompatible.status({}))
        self.assertEqual(value["error"], "incompatible_veto")
        self.assertEqual(value["required_api_version"], 1)
        old_cli = RUNTIME(FakeRunner([COMMAND_RESULT("status", 2, "")]))
        self.assertEqual(json.loads(old_cli.status({}))["error"], "incompatible_veto")

    def test_json_failure_modes_do_not_raise(self):
        cases = [
            (COMMAND_RESULT("x", 0, "not-json"), "invalid_response"),
            (COMMAND_RESULT("x", 1, ""), "veto_failed"),
            (COMMAND_RESULT("x", -1, "", timed_out=True), "timeout"),
            (COMMAND_RESULT("x", 0, "{}", oversized=True), "response_too_large"),
        ]
        for result, expected in cases:
            with self.subTest(expected=expected):
                value = json.loads(RUNTIME(FakeRunner([result])).models({}))
                self.assertEqual(value["error"], expected)

    def test_run_and_cancel_use_explicit_execution_id(self):
        runner = FakeRunner([COMMAND_RESULT("job-1", 0, "done")])
        runner.cancelled.add("job-1")
        runtime = RUNTIME(runner)
        result = json.loads(runtime.run({"objective": "summarize", "execution_id": "job-1"}))
        self.assertEqual(result, {"ok": True, "execution_id": "job-1", "output": "done"})
        self.assertTrue(json.loads(runtime.cancel({"execution_id": "job-1"}))["cancelled"])
        self.assertEqual(json.loads(runtime.cancel({"execution_id": "missing"}))["error"], "not_active")

    def test_commands_toggle_visible_state_and_validate_usage(self):
        runner = FakeRunner([
            COMMAND_RESULT("status", 0, '{"api_version":1,"version":"0.4.1"}'),
            COMMAND_RESULT("models", 0, '{"models":[{"name":"sol","provider":"openai","runtime":"direct"}]}'),
            COMMAND_RESULT("route", 0, '{"model":"sol","provider":"openai","runtime":"direct","confidence":0.99}'),
        ])
        runtime = RUNTIME(runner)
        self.assertIn("disabled", runtime.command_off(""))
        status = json.loads(runtime.status({}))
        self.assertFalse(status["automatic_routing"])
        self.assertFalse(status["automatic_routing_preference"])
        self.assertIn("enabled", runtime.command_veto("on"))
        self.assertEqual(runtime.command_models("extra"), "Usage: /models")
        self.assertIn("sol · openai · direct", runtime.command_models(""))
        self.assertIn("99% confidence", runtime.command_route("plan"))
        self.assertIn("Veto error", runtime.command_route(""))

    def test_turn_route_rewrites_only_external_user_turns_and_records_trace(self):
        runner = FakeRunner([
            COMMAND_RESULT(
                "route",
                0,
                '{"model":"sol","api_model":"gpt-5.4","provider":"openai","runtime":"openai-api"}',
            )
        ])
        runtime = RUNTIME(runner)
        selected = runtime.turn_route(
            {"model": "old", "provider": "anthropic", "runtime": {"provider": "anthropic"}},
            user_message="plan a release",
            session_id="session-1",
            is_user_turn=True,
            internal=False,
            tool_continuation=False,
        )
        self.assertEqual(selected["route"]["model"], "gpt-5.4")
        self.assertEqual(selected["route"]["provider"], "openai")
        self.assertEqual(selected["source"], "veto")
        self.assertEqual(json.loads(runtime.status({}, session_id="session-1"))["last_route"]["model"], "gpt-5.4")
        self.assertIsNone(runtime.turn_route({}, user_message="internal", session_id="session-1", internal=True))

    def test_turn_route_disable_and_pin_are_session_scoped(self):
        runtime = RUNTIME(FakeRunner())
        self.assertIn(
            "disabled",
            runtime.command_off("", session_id="physical-1", session_key="chat-1"),
        )
        self.assertIsNone(
            runtime.turn_route(
                {}, user_message="x", session_id="physical-2", session_key="chat-1"
            )
        )
        self.assertIn(
            "pin",
            runtime.command_veto(
                "pin openrouter", session_id="physical-1", session_key="chat-2"
            ),
        )
        self.assertIn(
            "cleared",
            runtime.command_veto(
                "pin off", session_id="physical-2", session_key="chat-2"
            ),
        )

    def test_status_preserves_physical_id_but_keys_state_by_session_key(self):
        runtime = RUNTIME(FakeRunner())
        runtime.command_off("", session_id="physical-1", session_key="chat-1")
        value = json.loads(
            runtime.status({}, session_id="physical-rotated", session_key="chat-1")
        )
        self.assertEqual(value["session_id"], "physical-rotated")
        self.assertEqual(value["session_key"], "chat-1")
        self.assertTrue(value["session_preference"]["disabled"])

    def test_registered_handlers_contain_unexpected_failures(self):
        ctx = FakeContext()
        PLUGIN.register(ctx)
        tool_value = json.loads(ctx.tools["veto_status"]["handler"](None, future_context=True))
        self.assertEqual(tool_value["error"], "invalid_arguments")
        self.assertIn("invalid arguments", ctx.commands["veto"]["handler"](None))

    def test_non_finite_numbers_are_rejected(self):
        runtime = RUNTIME(FakeRunner())
        value = json.loads(runtime.route({"objective": "plan", "max_cost_usd": float("nan")}))
        self.assertEqual(value["error"], "invalid_arguments")


class ProcessRunnerTests(unittest.TestCase):
    @unittest.skipIf(os.name == "nt", "POSIX executable fixture")
    def test_process_runner_preserves_arguments_and_origin_marker(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "fake-veto"
            binary.write_text(
                "#!/usr/bin/env python3\n"
                "import json, os, sys\n"
                "print(json.dumps({'argv': sys.argv[1:], 'origin': os.getenv('VETO_ROUTING_ORIGIN')}))\n"
            )
            binary.chmod(0o700)
            with patch.dict(os.environ, {"VETO_BINARY": str(binary)}, clear=False):
                result = PLUGIN.runtime.ProcessRunner().run(["route", "--task", "a; echo nope"], timeout=2)
            self.assertEqual(result.returncode, 0)
            value = json.loads(result.stdout)
            self.assertEqual(value["argv"], ["route", "--task", "a; echo nope"])
            self.assertEqual(value["origin"], "hermes-plugin")

    @unittest.skipIf(os.name == "nt", "POSIX cancellation fixture")
    def test_process_runner_cancels_only_named_active_process(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "fake-veto"
            binary.write_text("#!/bin/sh\nsleep 30\n")
            binary.chmod(0o700)
            runner = PLUGIN.runtime.ProcessRunner()
            result = {}

            def invoke():
                result["value"] = runner.run(["run"], timeout=20, execution_id="job-2")

            with patch.dict(os.environ, {"VETO_BINARY": str(binary)}, clear=False):
                thread = threading.Thread(target=invoke)
                thread.start()
                deadline = time.time() + 3
                while time.time() < deadline:
                    with runner._lock:
                        if "job-2" in runner._active:
                            break
                    time.sleep(0.01)
                self.assertTrue(runner.cancel("job-2"))
                thread.join(timeout=5)
            self.assertFalse(thread.is_alive())
            self.assertNotEqual(result["value"].returncode, 0)


if __name__ == "__main__":
    unittest.main()
