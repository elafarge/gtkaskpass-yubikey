"""Kernel-backed, test-only HID fixture. Never installed with the application."""
import os
import pathlib
import selectors
import socket
import struct
import time

root = pathlib.Path("/run/ssh-askpass-fido-touch-fixture")
root.mkdir(mode=0o700)
descriptor = bytes.fromhex("06 d0 f1 09 01 a1 01 09 20 15 00 26 ff 00 75 08 95 40 81 02 09 21 95 40 91 02 c0")
fd = os.open("/dev/uhid", os.O_RDWR | os.O_NONBLOCK)
os.write(fd, struct.pack("<I128s64s64sHHIIII4096s", 11, b"ssh-askpass-fido touch fixture",
                        b"test", b"fixture", len(descriptor), 3, 0x1234, 0x5678, 1, 0, descriptor))
server = socket.socket(socket.AF_UNIX)
server.bind(str(root / "control"))
server.listen(1)
deadline = time.monotonic() + 10
device = None
while device is None:
    for path in pathlib.Path("/sys/class/hidraw").glob("hidraw*"):
        if "ssh-askpass-fido touch fixture" in (path / "device/uevent").read_text():
            device = pathlib.Path("/dev", path.name)
            break
    if time.monotonic() > deadline:
        raise RuntimeError("UHID device did not appear")
    time.sleep(.02)
while not device.exists():
    assert time.monotonic() < deadline
    time.sleep(.02)
device.chmod(0o666)  # Disposable VM fixture only, not a production udev rule.
(root / "device").write_text(str(device))
(root / "ready").touch()

def report(channel, command, status):
    return struct.pack(">IBHB", channel, command, 1, status).ljust(64, b"\0")

with selectors.DefaultSelector() as selector:
    selector.register(fd, selectors.EVENT_READ)
    selector.register(server, selectors.EVENT_READ)
    running = True
    while running:
        for key, _ in selector.select(timeout=5):
            if key.fileobj == fd:
                event = os.read(fd, 8192)
                kind = struct.unpack("<I", event[:4])[0]
                # Monitor must NEVER send output, get-feature, or set-feature.
                if kind in (6, 9, 13):
                    (root / "unexpected-write").write_text(str(kind))
            else:
                conn, _ = server.accept()
                with conn:
                    command = conn.recv(128).decode().strip()
                    if command == "up":
                        payload = report(42, 0xbb, 2)
                    elif command == "done":
                        payload = report(42, 0x90, 0)
                    elif command == "unrelated":
                        payload = report(99, 0x90, 0)
                    elif command == "quit":
                        running = False
                        conn.sendall(b"ok")
                        continue
                    else:
                        raise ValueError(command)
                    os.write(fd, struct.pack("<IH", 12, len(payload)) + payload)
                    conn.sendall(b"ok")
server.close()
os.close(fd)
