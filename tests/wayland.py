"""Smoke-test the installed helper on a real, isolated headless Wayland server."""
import os
import pathlib
import selectors
import signal
import subprocess
import tempfile
import time


with tempfile.TemporaryDirectory(prefix="ssh-askpass-fido-wayland-") as runtime:
    env = dict(os.environ, XDG_RUNTIME_DIR=runtime, WAYLAND_DISPLAY="askpass-test",
               GDK_BACKEND="wayland", GSK_RENDERER="cairo", GTK_A11Y="none",
               SSH_ASKPASS_FIDO_CACHE="off", SSH_ASKPASS_FIDO_TRACE="metadata")
    env.pop("DISPLAY", None)
    with tempfile.TemporaryFile() as weston_log:
        env["XDG_CONFIG_HOME"] = str(pathlib.Path(runtime, "test-config"))
        daemon = subprocess.Popen([os.environ["CACHE_BIN"], "serve", "--pin-verification", "off", "--touch-monitor=false"], env=env,
                                  stdout=subprocess.DEVNULL, stderr=weston_log)
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
            deadline = time.monotonic() + 5
            while not pathlib.Path(runtime, "ssh-askpass-fido", "cache.sock").exists():
                assert daemon.poll() is None and time.monotonic() < deadline
                time.sleep(.02)
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
            daemon.terminate()
            daemon.wait(timeout=5)
print("Wayland notification and input cancellation passed")
