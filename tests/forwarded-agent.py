"""Test a real SSH client's forwarded agent channel using disposable identities."""
import base64
import pathlib
import socket
import struct
import subprocess
import sys
import threading
import time

import paramiko
from paramiko.common import AUTH_SUCCESSFUL, AUTH_FAILED, OPEN_SUCCEEDED, OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED


def string(data):
    return struct.pack("!I", len(data)) + data


def exact(channel, size):
    result = b""
    while len(result) < size:
        part = channel.recv(size - len(result))
        if not part:
            raise RuntimeError("unexpected EOF from forwarded agent")
        result += part
    return result


root = pathlib.Path(sys.argv[1])
public = base64.b64decode((root / "fido-test.pub").read_text().split()[1])
identity = paramiko.RSAKey.generate(2048)
identity.write_private_key_file(str(root / "transport-key"))
(root / "transport-key").chmod(0o600)
forwarded = threading.Event()
executed = threading.Event()
problems = []


class Server(paramiko.ServerInterface):
    def check_auth_publickey(self, username, key):
        return AUTH_SUCCESSFUL if key == identity else AUTH_FAILED

    def get_allowed_auths(self, username):
        return "publickey"

    def check_channel_request(self, kind, chanid):
        return OPEN_SUCCEEDED if kind == "session" else OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

    def check_channel_forward_agent_request(self, channel):
        forwarded.set()
        return True

    def check_channel_exec_request(self, channel, command):
        executed.set()
        return True


listener = socket.socket()
listener.bind(("127.0.0.1", 0))
listener.listen(1)
listener.settimeout(10)
port = listener.getsockname()[1]


def serve():
    try:
        conn, _ = listener.accept()
        with paramiko.Transport(conn) as transport:
            transport.add_server_key(paramiko.RSAKey.generate(2048))
            transport.start_server(server=Server())
            channel = transport.accept(10)
            assert channel is not None and executed.wait(10) and forwarded.is_set()
            with transport.open_forward_agent_channel() as remote:
                remote.settimeout(10)
                # SSH_AGENTC_SIGN_REQUEST through a genuine SSH forwarding channel.
                request = bytes([13]) + string(public) + string(b"forwarded-signing-test") + struct.pack("!I", 0)
                remote.sendall(string(request))
                length = struct.unpack("!I", exact(remote, 4))[0]
                assert 1 < length < 16384
                response = exact(remote, length)
                assert response[0] == 14, "forwarded signing failed"
            channel.send_exit_status(0)
            channel.shutdown_write()
            time.sleep(.1)
            channel.close()
            time.sleep(.1)
    except Exception as error:
        problems.append(error)
    finally:
        listener.close()


thread = threading.Thread(target=serve, daemon=True)
thread.start()
result = subprocess.run([
    "ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
    "-o", "ForwardAgent=yes", "-o", "StrictHostKeyChecking=no",
    "-o", "UserKnownHostsFile=/dev/null", "-i", str(root / "transport-key"),
    "-p", str(port), "test@127.0.0.1", "test"
], capture_output=True, text=True, timeout=20)
thread.join(10)
assert not thread.is_alive() and not problems, problems
assert result.returncode == 0, result.stderr
