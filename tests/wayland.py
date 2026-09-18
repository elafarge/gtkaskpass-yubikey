"""Smoke-test the installed helper on a real, isolated headless Wayland server."""
import os
import pathlib
import selectors
import signal
import subprocess
import tempfile
import time


with tempfile.TemporaryDirectory(prefix="gtkaskpass-wayland-") as runtime:
    env = dict(os.environ, XDG_RUNTIME_DIR=runtime, WAYLAND_DISPLAY="askpass-test",
               GDK_BACKEND="wayland", GSK_RENDERER="cairo", GTK_A11Y="none",
               GTKASKPASS_CACHE="off", GTKASKPASS_TRACE="metadata")
    env.pop("DISPLAY", None)
    with tempfile.TemporaryFile() as weston_log:
        weston = subprocess.Popen(
            ["weston", "--backend=headless", "--renderer=pixman", "--shell=kiosk",
             "--no-config", "--socket=askpass-test"], env=env,
            stdout=weston_log, stderr=weston_log)
        try:
            deadline = time.monotonic() + 10
            while not pathlib.Path(runtime, "askpass-test").exists():
                if weston.poll() is not None or time.monotonic() > deadline:
                    weston_log.seek(0)
                    raise AssertionError(weston_log.read().decode())
                time.sleep(0.02)
            for hint, expected in [("none", 0), ("", 1)]:
                env["SSH_ASKPASS_PROMPT"] = hint
                process = subprocess.Popen(
                    [os.environ["ASKPASS_BIN"], "Wayland smoke test"], env=env,
                    stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                try:
                    assert process.stderr is not None
                    with selectors.DefaultSelector() as selector:
                        selector.register(process.stderr, selectors.EVENT_READ)
                        trace = b""
                        deadline = time.monotonic() + 10
                        while b"window-shown" not in trace:
                            if time.monotonic() > deadline or process.poll() is not None:
                                raise AssertionError(trace.decode())
                            if selector.select(timeout=0.1):
                                trace += os.read(process.stderr.fileno(), 4096)
                    process.send_signal(signal.SIGTERM)
                    out, err = process.communicate(timeout=5)
                    assert process.returncode == expected, (process.returncode, trace, err)
                    assert out == b"", out
                finally:
                    if process.poll() is None:
                        process.kill()
                        process.wait()
        finally:
            weston.terminate()
            weston.wait(timeout=5)
print("Wayland notification and input cancellation passed")
