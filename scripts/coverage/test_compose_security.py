"""Exercise operational scripts against isolated files and real cryptographic tools."""

import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class ComposeSecurityContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        for tool in ("openssl", "jq"):
            if shutil.which(tool) is None:
                raise RuntimeError(f"{tool} is required for operational security contracts")

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.scripts = self.root / "scripts" / "compose"
        self.scripts.mkdir(parents=True)
        for name in ("rotate-auth-key.sh", "init-docker-proxy-certs.sh"):
            shutil.copyfile(ROOT / "scripts" / "compose" / name, self.scripts / name)
        self.environment = dict(os.environ)
        for name in ("COMPOSE_FILE", "DOCKER_PROXY_CERT_DAYS", "DOCKER_PROXY_ROTATE_CERTS"):
            self.environment.pop(name, None)
        self.issuers = self.root / "certs" / "issuers"
        self.issuer = self.issuers / "server"
        self.trusted = self.issuers / "trusted.json"

    def run_script(self, name, *args, **environment):
        return subprocess.run(
            ["/bin/sh", str(self.scripts / name), *map(str, args)],
            env={**self.environment, **environment},
            text=True,
            capture_output=True,
            timeout=30,
        )

    def seed_existing(self, bundle):
        self.issuer.mkdir(parents=True)
        (self.issuer / "auth.ed").write_text("existing private key\n")
        (self.issuer / "auth.ed.pub").write_text("existing public key\n")
        self.trusted.write_text(bundle)

    def assert_existing_unchanged(self, bundle):
        self.assertEqual((self.issuer / "auth.ed").read_text(), "existing private key\n")
        self.assertEqual((self.issuer / "auth.ed.pub").read_text(), "existing public key\n")
        self.assertEqual(self.trusted.read_text(), bundle)
        self.assertEqual(list(self.issuers.glob("**/tmp.*")), [])

    def test_unknown_and_missing_issuer_do_not_create_keys(self):
        for args, error in (((), "usage:"), (("../server",), "unknown issuer:")):
            with self.subTest(args=args):
                result = self.run_script("rotate-auth-key.sh", *args)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(error, result.stderr)
                self.assertFalse(self.issuers.exists())

    def test_invalid_bundles_preserve_existing_keys(self):
        for bundle in ("not-json", "[]", "null", '"text"'):
            with self.subTest(bundle=bundle):
                if self.issuers.exists():
                    shutil.rmtree(self.issuers)
                self.seed_existing(bundle)
                result = self.run_script("rotate-auth-key.sh", "server")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("is corrupt", result.stderr)
                self.assert_existing_unchanged(bundle)

    def test_untrusted_kid_is_rejected_before_shell_interpolation(self):
        marker = self.root / "unexpected-command"
        bundle = json.dumps({f"$(touch {marker})": {"iss": "server", "pub": "server/auth.ed.pub"}})
        self.seed_existing(bundle)
        result = self.run_script("rotate-auth-key.sh", "server")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unexpected kid", result.stderr)
        self.assertFalse(marker.exists())
        self.assert_existing_unchanged(bundle)

    def test_repeated_host_rotation_preserves_old_public_key_and_other_issuers(self):
        self.issuers.mkdir(parents=True)
        unrelated = {"iss": "users-service", "pub": "users-service/auth.ed.pub"}
        self.trusted.write_text(json.dumps({"unrelated": unrelated}))
        (self.root / "compose.dev.yaml").touch()
        first = self.run_script("rotate-auth-key.sh", "server")
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertIn("-f compose.dev.yaml", first.stdout)
        first_bundle = json.loads(self.trusted.read_text())
        first_kid = next(key for key in first_bundle if key != "unrelated")
        first_private = (self.issuer / "auth.ed").read_bytes()
        first_public = (self.issuer / "auth.ed.pub").read_text()
        self.assertEqual(first_bundle[first_kid], {"iss": "server", "pub": "server/auth.ed.pub"})
        # A writable host installation selects the host path instead of a root helper.
        (self.issuer / "auth.ed").chmod(0o640)
        self.trusted.chmod(0o644)
        second = self.run_script("rotate-auth-key.sh", "server", COMPOSE_FILE="custom.yaml")
        self.assertEqual(second.returncode, 0, second.stderr)
        self.assertIn("-f custom.yaml", second.stdout)
        bundle = json.loads(self.trusted.read_text())
        self.assertEqual(bundle["unrelated"], unrelated)
        self.assertEqual(bundle[first_kid], {"iss": "server", "pub": first_public.strip()})
        current = [key for key, entry in bundle.items() if entry == {"iss": "server", "pub": "server/auth.ed.pub"}]
        self.assertEqual(len(current), 1)
        self.assertNotEqual(current[0], first_kid)
        self.assertNotEqual((self.issuer / "auth.ed").read_bytes(), first_private)
        derived = subprocess.run(
            ["openssl", "pkey", "-in", str(self.issuer / "auth.ed"), "-pubout"],
            check=True, capture_output=True, timeout=10,
        ).stdout
        self.assertEqual(derived, (self.issuer / "auth.ed.pub").read_bytes())
        self.assertEqual(stat.S_IMODE((self.issuer / "auth.ed").stat().st_mode), 0o440)
        self.assertEqual(stat.S_IMODE((self.issuer / "auth.ed.pub").stat().st_mode), 0o444)
        self.assertEqual(stat.S_IMODE(self.trusted.stat().st_mode), 0o444)
        self.assertEqual(list(self.issuers.glob("**/tmp.*")), [])
        self.assertFalse((self.issuer / ".auth.ed.bak").exists())

    def test_certificate_root_guard_does_not_touch_existing_directory(self):
        unsafe = self.root / "unrelated"
        unsafe.mkdir()
        sentinel = unsafe / "keep"
        sentinel.write_text("preserve")
        result = self.run_script("init-docker-proxy-certs.sh", unsafe)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("refusing unsafe", result.stderr)
        self.assertEqual(sentinel.read_text(), "preserve")
        self.assertEqual(list(unsafe.iterdir()), [sentinel])

    def test_certificate_duration_validation_precedes_file_creation(self):
        root = self.root / "docker-proxy-certs"
        for days in ("abc", "-1", "0", "1;touch unexpected"):
            with self.subTest(days=days):
                result = self.run_script("init-docker-proxy-certs.sh", root, DOCKER_PROXY_CERT_DAYS=days)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("DOCKER_PROXY_CERT_DAYS", result.stderr)
                self.assertFalse(root.exists())


if __name__ == "__main__":
    unittest.main()
