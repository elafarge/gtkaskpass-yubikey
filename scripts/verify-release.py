"""Validate the complete draft release using GitHub's server-side SHA-256 digests."""
import hashlib
import json
import pathlib
import re
import sys

if not __debug__:
    raise RuntimeError("Release verification requires Python assertions enabled")


def verify(tag, revision, release, directory):
    if not re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", tag):
        raise ValueError("invalid release tag")
    assert release["draft"] is True and release["tag_name"] == tag
    assets = {asset["name"]: asset for asset in release["assets"]}
    expected = set()
    for system in ("x86_64-linux", "aarch64-linux"):
        base = f"gtkaskpass-yubikey-{tag[1:]}-{system}"
        names = [base + suffix for suffix in (".nar.zst", ".json", ".sha256")]
        expected.update(names)
        for name in names:
            assert assets[name]["state"] == "uploaded" and assets[name]["size"] > 0
        checksums = {}
        checksum_file = directory / names[2]
        assert assets[names[2]]["digest"] == "sha256:" + hashlib.sha256(checksum_file.read_bytes()).hexdigest()
        for line in checksum_file.read_text().splitlines():
            digest, name = line.split("  ", 1)
            assert re.fullmatch(r"[0-9a-f]{64}", digest)
            assert name not in checksums
            checksums[name] = digest
        assert set(checksums) == set(names[:2])
        for name, digest in checksums.items():
            assert assets[name]["digest"] == "sha256:" + digest
        metadata_file = directory / names[1]
        assert hashlib.sha256(metadata_file.read_bytes()).hexdigest() == checksums[names[1]]
        metadata = json.loads(metadata_file.read_text())
        assert metadata["format"] == 1 and metadata["version"] == tag[1:]
        assert metadata["system"] == system and metadata["revision"] == revision
        assert re.fullmatch(r"/nix/store/[a-z0-9]{32}-gtkaskpass-yubikey-" + re.escape(tag[1:]), metadata["storePath"])
    assert set(assets) == expected, "unexpected or missing release assets"


if __name__ == "__main__":
    verify(sys.argv[1], sys.argv[2], json.loads(pathlib.Path(sys.argv[3]).read_text()), pathlib.Path(sys.argv[4]))
    print("Both native release bundles and their uploaded digests verified")
