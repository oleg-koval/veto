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


def run(binary: str, args: list[str], rows: int, columns: int, mouse: bool) -> None:
    with tempfile.TemporaryDirectory(prefix="veto-tui-pty-") as home:
        pid, master = pty.fork()
        if pid == 0:
            env = os.environ.copy()
            env.update({"HOME": home, "NO_COLOR": "1"})
            os.execve(binary, [binary, "tui", *args], env)

        try:
            termios.tcsetwinsize(master, (rows, columns))
            output = bytearray()
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
                waited, status = os.waitpid(pid, os.WNOHANG)
                if waited == pid:
                    break
                time.sleep(0.05)
            if status is None:
                os.kill(pid, signal.SIGTERM)
                _, status = os.waitpid(pid, 0)
            if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
                raise SystemExit(f"TUI exited unsuccessfully: status={status} output={bytes(output)!r}")
        finally:
            os.close(master)


def main() -> int:
    if len(sys.argv) != 2:
        print(f"usage: {sys.argv[0]} VETO_BINARY", file=sys.stderr)
        return 2
    run(sys.argv[1], ["--reduce-motion", "--no-color", "--no-mouse"], 12, 40, False)
    run(sys.argv[1], ["--reduce-motion", "--no-color"], 24, 80, True)
    print("TUI PTY smoke passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
