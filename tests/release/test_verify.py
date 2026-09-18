"""Exercise the release gate with complete, incomplete, and tampered assets."""
import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest
from typing import Any

script = pathlib.Path(__file__).resolve().parents[2] / "scripts" / "verify-release.py"
spec = importlib.util.spec_from_file_location("verify_release", script)
assert spec is not None and spec.loader is not None
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class ReleaseVerification(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = pathlib.Path(self.temp.name)
        self.release: dict[str, Any] = dict(draft=True, tag_name="v0.1.0", assets=[])
        for system in ("x86_64-linux", "aarch64-linux"):
            base = f"gtkaskpass-yubikey-0.1.0-{system}"
            bundle = base + ".nar.zst"
            metadata = base + ".json"
            contents = {
                bundle: b"synthetic exported closure",
                metadata: json.dumps(dict(format=1, version="0.1.0", system=system,
                                          revision="commit-id",
                                          storePath="/nix/store/" + "a" * 32 + "-gtkaskpass-yubikey-0.1.0")).encode(),
            }
            contents[base + ".sha256"] = "".join(
                hashlib.sha256(data).hexdigest() + "  " + name + "\n"
                for name, data in contents.items()).encode()
            for name, data in contents.items():
                (self.directory / name).write_bytes(data)
                self.release["assets"].append(dict(name=name, size=len(data), state="uploaded",
                                                   digest="sha256:" + hashlib.sha256(data).hexdigest()))

    def verify(self):
        verifier.verify("v0.1.0", "commit-id", self.release, self.directory)

    def test_complete(self):
        self.verify()

    def test_missing_architecture(self):
        self.release["assets"] = self.release["assets"][:3]
        with self.assertRaises(KeyError):
            self.verify()

    def test_incomplete_upload(self):
        self.release["assets"][0]["state"] = "new"
        with self.assertRaises(AssertionError):
            self.verify()

    def test_corrupt_bundle(self):
        self.release["assets"][0]["digest"] = "sha256:" + "0" * 64
        with self.assertRaises(AssertionError):
            self.verify()

    def test_corrupt_metadata(self):
        metadata = next(self.directory.glob("*.json"))
        metadata.write_text("{}")
        with self.assertRaises(AssertionError):
            self.verify()

    def test_wrong_revision(self):
        with self.assertRaises(AssertionError):
            verifier.verify("v0.1.0", "other-commit", self.release, self.directory)

    def test_published_release(self):
        self.release["draft"] = False
        with self.assertRaises(AssertionError):
            self.verify()

    def test_invalid_tag(self):
        with self.assertRaises(ValueError):
            verifier.verify("../tag", "commit-id", self.release, self.directory)
