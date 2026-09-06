#!/usr/bin/env python3
"""Exercise the built TUI through a real pseudo-terminal."""

from __future__ import annotations

import os
import pty
import select
import signal
import sys
import tempfile
import termios
import time


def run(binary: str, args: list[str], rows: int, columns: int, mouse: bool, secret_probe: bool = False, home: str | None = None, term: str | None = None, command: list[str] | None = None) -> None:
    home_context = tempfile.TemporaryDirectory(prefix="veto-tui-pty-") if home is None else None
    selected_home = home if home is not None else home_context.name
    try:
        pid, master = pty.fork()
        if pid == 0:
            env = os.environ.copy()
            env.update({"HOME": selected_home, "NO_COLOR": "1"})
            if term is not None:
                env["TERM"] = term
            argv = [binary, "tui", *args] if command is None else [binary, *command]
            os.execve(binary, argv, env)

        try:
            termios.tcsetwinsize(master, (rows, columns))
            output = bytearray()

            def drain() -> None:
                while True:
                    ready, _, _ = select.select([master], [], [], 0)
                    if not ready:
                        return
                    try:
                        data = os.read(master, 8192)
                    except OSError:
                        return
                    if not data:
                        return
                    output.extend(data)

            deadline = time.monotonic() + 12
            while time.monotonic() < deadline:
                ready, _, _ = select.select([master], [], [], 0.2)
                if ready:
                    try:
                        output.extend(os.read(master, 8192))
                    except OSError:
                        break
                if b"VETO" in output and (b"Ready" in output or b"unavailable" in output or b"Error" in output):
                    break
            time.sleep(0.2)

            if secret_probe:
                # Open the first command's form and type through the provider,
                # mode, and masked API-key fields, then cancel before submit.
                os.write(master, b"r")
                time.sleep(0.15)
                os.write(master, b"openai\r\rsmoke-secret")
                time.sleep(0.2)
                os.write(master, b"\x1b")
            else:
                os.write(master, b"/")
                time.sleep(0.15)
                drain()
                if b"Command palette" not in output:
                    raise SystemExit(f"command palette did not render after '/': output={bytes(output)!r}")
                os.write(master, b"\x1b")
                time.sleep(0.1)
                os.write(master, b"?")
                time.sleep(0.15)
                os.write(master, b"?")
                if mouse:
                    os.write(master, b"\x1b[<35;3;2M")
            time.sleep(0.15)
            os.write(master, b"q")

            end = time.monotonic() + 8
            status = None
            while time.monotonic() < end:
                drain()
                waited, status = os.waitpid(pid, os.WNOHANG)
                if waited == pid:
                    drain()
                    break
                time.sleep(0.05)
            if status is None:
                os.kill(pid, signal.SIGTERM)
                _, status = os.waitpid(pid, 0)
            if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
                raise SystemExit(f"TUI exited unsuccessfully: status={status} output={bytes(output)!r}")
            if b"\x1b[?1049h" not in output or b"\x1b[?1049l" not in output:
                raise SystemExit(f"TUI did not enter/leave alternate screen: output={bytes(output)!r}")
            if b"VETO" not in output or not (b"COMMANDS" in output or b"COMPOSER" in output):
                raise SystemExit(f"TUI did not render its command shell: output={bytes(output)!r}")
            if secret_probe and b"smoke-secret" in output:
                raise SystemExit("TUI login form leaked the probe secret into terminal output")
        finally:
            os.close(master)
    finally:
        if home_context is not None:
            home_context.cleanup()


def run_execution(binary: str, home: str) -> None:
    pid, master = pty.fork()
    if pid == 0:
        env = os.environ.copy()
        env.update({"HOME": home, "NO_COLOR": "1"})
        os.execve(binary, [binary, "tui", "--reduce-motion", "--no-color", "--no-mouse"], env)

    output = bytearray()
    status = None
    try:
        termios.tcsetwinsize(master, (24, 100))
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    chunk = os.read(master, 8192)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                if b"VETO" in output:
                    break

        # Select Run, open its composer, enter an objective, then accept the
        # default CLI-compatible flag values through the final field.
        os.write(master, b"jjjr")
        time.sleep(0.15)
        os.write(master, b"summarize this example\r")
        for _ in range(13):
            time.sleep(0.03)
            os.write(master, b"\r")

        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    chunk = os.read(master, 8192)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                if b"SMOKE EXECUTION OK" in output:
                    break
        if b"SMOKE EXECUTION OK" not in output:
            raise SystemExit(f"TUI Run did not reach fake-provider output: output={bytes(output)!r}")
        if not (b"LIVE ROUTING" in output or b"route." in output or b"winner" in output):
            raise SystemExit(f"TUI Run did not render live routing evidence: output={bytes(output)!r}")
        os.write(master, b"q")

        end = time.monotonic() + 8
        while time.monotonic() < end:
            waited, status = os.waitpid(pid, os.WNOHANG)
            if waited == pid:
                break
            time.sleep(0.05)
        if status is None:
            os.kill(pid, signal.SIGTERM)
            _, status = os.waitpid(pid, 0)
        if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
            raise SystemExit(f"TUI Run exited unsuccessfully: status={status} output={bytes(output)!r}")
    finally:
        os.close(master)


def run_cancellation(binary: str, home: str) -> None:
    pid, master = pty.fork()
    if pid == 0:
        env = os.environ.copy()
        env.update({"HOME": home, "NO_COLOR": "1"})
        os.execve(binary, [binary, "tui", "--reduce-motion", "--no-color", "--no-mouse"], env)

    output = bytearray()
    status = None
    try:
        termios.tcsetwinsize(master, (24, 100))
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    output.extend(os.read(master, 8192))
                except OSError:
                    break
                if b"VETO" in output:
                    break

        # Select Run, submit a task that only the delayed fake model accepts,
        # then cancel while the admission request is still in flight.
        request_signal = os.path.join(home, ".veto", "fake-provider-request.received")
        try:
            os.unlink(request_signal)
        except FileNotFoundError:
            pass
        os.write(master, b"jjjr")
        time.sleep(0.15)
        os.write(master, b"debug this example\r")
        for _ in range(13):
            time.sleep(0.03)
            os.write(master, b"\r")
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline and not os.path.exists(request_signal):
            ready, _, _ = select.select([master], [], [], 0.1)
            if ready:
                try:
                    output.extend(os.read(master, 8192))
                except OSError:
                    break
        if not os.path.exists(request_signal):
            raise SystemExit(f"fake provider did not receive cancellation request: output={bytes(output)!r}")
        os.write(master, b"\x1b")

        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    output.extend(os.read(master, 8192))
                except OSError:
                    break
                if b"action cancelled" in output:
                    break
        if b"action cancelled" not in output:
            raise SystemExit(f"TUI Run cancellation was not rendered: output={bytes(output)!r}")
        os.write(master, b"q")

        end = time.monotonic() + 8
        while time.monotonic() < end:
            waited, status = os.waitpid(pid, os.WNOHANG)
            if waited == pid:
                break
            time.sleep(0.05)
        if status is None:
            os.kill(pid, signal.SIGTERM)
            _, status = os.waitpid(pid, 0)
        if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
            raise SystemExit(f"TUI cancellation exited unsuccessfully: status={status} output={bytes(output)!r}")
    finally:
        os.close(master)


def run_route(binary: str, home: str) -> None:
    pid, master = pty.fork()
    if pid == 0:
        env = os.environ.copy()
        env.update({"HOME": home, "NO_COLOR": "1"})
        os.execve(binary, [binary, "tui", "--reduce-motion", "--no-color", "--no-mouse"], env)

    output = bytearray()
    status = None
    try:
        termios.tcsetwinsize(master, (24, 100))
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    chunk = os.read(master, 8192)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                if b"VETO" in output:
                    break

        # Select Route, open the selected action, submit an objective, and
        # accept every default flag. The global r shortcut opens Run.
        os.write(master, b"jjjjj\r")
        time.sleep(0.15)
        os.write(master, b"route this example\r")
        for _ in range(12):
            time.sleep(0.03)
            os.write(master, b"\r")

        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    chunk = os.read(master, 8192)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                if b"LIVE ROUTING" in output and (b"winner" in output or b"completed" in output):
                    break
        if b"LIVE ROUTING" not in output or not (b"winner" in output or b"completed" in output):
            raise SystemExit(f"TUI Route did not render completion evidence: output={bytes(output)!r}")
        os.write(master, b"q")

        end = time.monotonic() + 8
        while time.monotonic() < end:
            waited, status = os.waitpid(pid, os.WNOHANG)
            if waited == pid:
                break
            time.sleep(0.05)
        if status is None:
            os.kill(pid, signal.SIGTERM)
            _, status = os.waitpid(pid, 0)
        if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
            raise SystemExit(f"TUI Route exited unsuccessfully: status={status} output={bytes(output)!r}")
    finally:
        os.close(master)


def run_resize(binary: str, home: str | None = None) -> None:
    home_context = tempfile.TemporaryDirectory(prefix="veto-tui-resize-") if home is None else None
    selected_home = home if home is not None else home_context.name
    try:
        pid, master = pty.fork()
        if pid == 0:
            env = os.environ.copy()
            env.update({"HOME": selected_home, "NO_COLOR": "1"})
            os.execve(binary, [binary, "tui", "--reduce-motion", "--no-color", "--no-mouse"], env)

        output = bytearray()
        status = None
        try:
            termios.tcsetwinsize(master, (24, 100))
            deadline = time.monotonic() + 8
            while time.monotonic() < deadline and b"VETO" not in output:
                ready, _, _ = select.select([master], [], [], 0.2)
                if ready:
                    try:
                        output.extend(os.read(master, 8192))
                    except OSError:
                        break
            termios.tcsetwinsize(master, (12, 40))
            time.sleep(0.3)
            termios.tcsetwinsize(master, (24, 100))
            time.sleep(0.5)
            os.write(master, b"q")

            end = time.monotonic() + 8
            while time.monotonic() < end:
                waited, status = os.waitpid(pid, os.WNOHANG)
                if waited == pid:
                    break
                time.sleep(0.05)
            if status is None:
                os.kill(pid, signal.SIGTERM)
                _, status = os.waitpid(pid, 0)
            if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
                raise SystemExit(f"TUI resize exited unsuccessfully: status={status}")
        finally:
            os.close(master)
    finally:
        if home_context is not None:
            home_context.cleanup()


def run_plan(binary: str, home: str) -> None:
    pid, master = pty.fork()
    if pid == 0:
        env = os.environ.copy()
        env.update({"HOME": home, "NO_COLOR": "1"})
        os.execve(binary, [binary, "tui", "--reduce-motion", "--no-color", "--no-mouse"], env)

    output = bytearray()
    status = None
    try:
        termios.tcsetwinsize(master, (24, 100))
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    output.extend(os.read(master, 8192))
                except OSError:
                    break
                if b"VETO" in output:
                    break

        # Open Execute plan through the palette, choose the safe plan name,
        # accept default flags, and verify the step reaches the Runner.
        os.write(master, b"jjjj\r")
        time.sleep(0.2)
        os.write(master, b"smoke-plan.md\r")
        for _ in range(6):
            time.sleep(0.03)
            os.write(master, b"\r")

        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            ready, _, _ = select.select([master], [], [], 0.2)
            if ready:
                try:
                    chunk = os.read(master, 8192)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                if b"SMOKE EXECUTION OK" in output:
                    break
        if b"SMOKE EXECUTION OK" not in output or b"OUTPUT" not in output:
            raise SystemExit(f"TUI Execute plan did not reach visible Runner output: output={bytes(output)!r}")
        os.write(master, b"q")

        end = time.monotonic() + 8
        while time.monotonic() < end:
            waited, status = os.waitpid(pid, os.WNOHANG)
            if waited == pid:
                break
            time.sleep(0.05)
        if status is None:
            os.kill(pid, signal.SIGTERM)
            _, status = os.waitpid(pid, 0)
        if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
            raise SystemExit(f"TUI Execute plan exited unsuccessfully: status={status} output={bytes(output)!r}")
    finally:
        os.close(master)


def main() -> int:
    if len(sys.argv) not in (2, 4) or (len(sys.argv) == 4 and sys.argv[2] != "--execution-home"):
        print(f"usage: {sys.argv[0]} VETO_BINARY [--execution-home HOME]", file=sys.stderr)
        return 2
    run(sys.argv[1], ["--reduce-motion", "--no-color", "--no-mouse"], 12, 40, False, True)
    run(sys.argv[1], ["--reduce-motion", "--no-color"], 24, 80, True)
    run(sys.argv[1], [], 24, 80, False, command=[])
    for term in ("xterm-256color", "screen-256color", "vt100", "dumb"):
        run(sys.argv[1], ["--reduce-motion", "--no-color", "--no-mouse"], 24, 80, False, term=term)
    run(sys.argv[1], ["--screen-reader"], 24, 80, False, term="dumb")
    run_resize(sys.argv[1])
    if len(sys.argv) == 4:
        run_route(sys.argv[1], sys.argv[3])
        run_cancellation(sys.argv[1], sys.argv[3])
        run_execution(sys.argv[1], sys.argv[3])
        run_plan(sys.argv[1], sys.argv[3])
    print("TUI PTY smoke passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
