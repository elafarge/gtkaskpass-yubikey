"""Test-only client for the user-service IPC; credentials are synthetic."""
import base64
import json
import os
import socket
import struct
import sys


def call(**request):
    data = json.dumps(dict(version=1, **request)).encode()
    with socket.socket(socket.AF_UNIX) as conn:
        conn.settimeout(5)
        conn.connect(os.environ["XDG_RUNTIME_DIR"] + "/gtkaskpass-yubikey/cache.sock")
        conn.sendall(struct.pack("!I", len(data)) + data)
        stream = conn.makefile("rb")
        length = struct.unpack("!I", stream.read(4))[0]
        response = json.loads(stream.read(length))
        assert "error" not in response, response
        return response


key = dict(path="/home/alice/key", kind="pin")
response = call(op="begin", key=key)
if sys.argv[1] == "store":
    assert response.get("ttl") == 2_000_000_000, response
    result = call(op="commit", key=key, token=response["token"],
                  secret=base64.b64encode(b"synthetic-pin").decode())
    assert result["reason"] == "stored", result
elif sys.argv[1] == "hit":
    assert base64.b64decode(response["secret"]) == b"synthetic-pin", response
elif sys.argv[1] == "miss":
    assert "secret" not in response, response
else:
    raise ValueError(sys.argv[1])
