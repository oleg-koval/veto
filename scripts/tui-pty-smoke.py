#!/usr/bin/env python3
"""Exercise the built TUI through a real pseudo-terminal."""

from __future__ import annotations

import os
import pty
import select
import signal
import sys
import time
import tempfile
import termios


def run(binary: str, args: list[str], rows: int, columns: int, mouse: bool, secret_probe: bool = False, home: str | None = None) -> None:
    home_context = tempfile.TemporaryDirectory(prefix="veto-tui-pty-") if home is None else None
    selected_home = home if home is not None else home_context.name
    try:
        pid, master = pty.fork()
        if pid == 0:
            env = os.environ.copy()
            env.update({"HOME": selected_home, "NO_COLOR": "1"})
            os.execve(binary, [binary, "tui", *args], env)

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
                if time.monotonic() + 0.2 >= deadline:
                    break
                if len(output) > 0:
                    break

            if secret_probe:
                # Open the first command's form and type through the provider,
                # mode, and masked API-key fields, then cancel before submit.
                os.write(master, b"r")
                time.sleep(0.15)
                os.write(master, b"openai\r\rsmoke-secret")
                time.sleep(0.2)
                os.write(master, b"\x1b")
            else:
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


def main() -> int:
    if len(sys.argv) not in (2, 4) or (len(sys.argv) == 4 and sys.argv[2] != "--execution-home"):
        print(f"usage: {sys.argv[0]} VETO_BINARY [--execution-home HOME]", file=sys.stderr)
        return 2
    run(sys.argv[1], ["--reduce-motion", "--no-color", "--no-mouse"], 12, 40, False, True)
    run(sys.argv[1], ["--reduce-motion", "--no-color"], 24, 80, True)
    if len(sys.argv) == 4:
        run_execution(sys.argv[1], sys.argv[3])
    print("TUI PTY smoke passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
