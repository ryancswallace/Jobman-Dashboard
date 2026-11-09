import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("generate_notices", Path(__file__).with_name("generate-notices.py"))
notices = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notices)


class NoticeTests(unittest.TestCase):
    def test_root_file_copy_inputs_are_in_the_docker_context_allowlist(self):
        root = Path(__file__).resolve().parents[1]
        rules = (root / ".dockerignore").read_text().splitlines()
        self.assertIn("**", rules)
        for line in (root / "Dockerfile").read_text().splitlines():
            parts = line.split()
            if not parts or parts[0] != "COPY" or parts[1].startswith("--"):
                continue
            for source in parts[1:-1]:
                if "/" not in source and "*" not in source:
                    self.assertTrue((root / source).is_file(), source)
                    self.assertIn("!" + source, rules, source)

    def test_preserves_full_nested_and_supplemental_notice_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "nested").mkdir()
            (root / "LICENSE").write_bytes(b"license\r\ncomplete terms\r\n")
            (root / "nested/NOTICE.txt").write_text("nested attribution\n")
            (root / "THIRD-PARTY-LICENSE").write_text("upstream runtime attribution\n")
            (root / "LICENSE-3RD-PARTY.md").write_text("additional attribution\n")
            (root / "LICENSE-MIT").write_text("MIT attribution\n")
            (root / "notice_response.go").write_text("package pgproto3\n")
            (root / "copyright-date.sh").write_text("#!/bin/sh\n")
            found = {item["path"]: item for item in notices.notices(root)}
            self.assertEqual(set(found), {"LICENSE", "nested/NOTICE.txt", "THIRD-PARTY-LICENSE", "LICENSE-3RD-PARTY.md", "LICENSE-MIT"})
            self.assertEqual(found["LICENSE"]["text"], "license\r\ncomplete terms\r\n")
            self.assertEqual(found["LICENSE"]["sha256"], notices.digest((root / "LICENSE").read_bytes()))

    def test_broker_only_dependency_is_in_the_release_union(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "go.version").write_text("1.26.10\n")
            for directory in ("toolchain", "shared", "broker"):
                (root / directory).mkdir()
                (root / directory / "LICENSE").write_text(directory + " license\n")
            (root / "toolchain/PATENTS").write_text("toolchain patents\n")
            def capture(args, _root, env):
                if args[1] == "env":
                    return json.dumps({"GOROOT": str(root / "toolchain"), "GOVERSION": "go1.26.10"})
                names = ["shared"] + (["broker"] if args[-1].endswith("jobman-log-broker") else [])
                return "\n".join(json.dumps({"Module": {"Path": name, "Version": "v1.0.0", "Sum": "h1:locked", "Dir": str(root / name)}}) for name in names)
            with patch.object(notices, "capture", side_effect=capture):
                found = {item["name"]: item for item in notices.go_packages(root)}
            self.assertEqual(len(found["shared"]["observations"]), 4)
            self.assertEqual(found["broker"]["observations"], [
                {"binary": "jobman-log-broker", "os": "linux", "architecture": arch} for arch in ("amd64", "arm64")])

    def npm_fixture(self, root):
        package = root / "web/node_modules/example"
        package.mkdir(parents=True)
        (package / "package.json").write_text(json.dumps({"name": "example", "version": "1.0.0", "license": "MIT"}))
        (package / "index.js").write_text("export const value = 1;\n")
        (package / "LICENSE").write_text("complete upstream notice\n")
        lock = root / "web/package-lock.json"
        lock.write_text(json.dumps({"packages": {"node_modules/example": {"version": "1.0.0", "integrity": "sha512-locked", "resolved": "https://registry.npmjs.org/example/-/example-1.0.0.tgz"}}}))
        (root / "web/dist").mkdir()
        graph = {"schemaVersion": 1, "lockSHA256": notices.digest(lock.read_bytes()), "observations": [{"kind": "bundled-module", "module": "node_modules/example/index.js", "sourceSHA256": notices.digest((package / "index.js").read_bytes())}]}
        (root / "web/dist/third-party-web.json").write_text(json.dumps(graph))
        return package, lock

    def test_browser_inventory_binds_lock_installed_version_and_module_bytes(self):
        for mutation in ("lock", "version", "module"):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                package, lock = self.npm_fixture(root)
                found = notices.npm_packages(root)
                self.assertEqual(found[0]["notices"][0]["text"], "complete upstream notice\n")
                if mutation == "lock":
                    lock.write_text(lock.read_text() + "\n")
                elif mutation == "version":
                    (package / "package.json").write_text('{"name":"example","version":"2.0.0"}')
                else:
                    (package / "index.js").write_text("changed source")
                with self.assertRaises(ValueError):
                    notices.npm_packages(root)

    def test_check_rejects_stale_inventory_or_notices(self):
        data = {"scope": "fixture", "packages": []}
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            with patch.object(notices, "inventory", return_value=data):
                notices.generate(root, False)
                notices.generate(root, True)
                for filename in (notices.INVENTORY, notices.NOTICES):
                    original = (root / filename).read_bytes()
                    (root / filename).write_bytes(original + b"changed")
                    with self.assertRaisesRegex(ValueError, "stale"):
                        notices.generate(root, True)
                    (root / filename).write_bytes(original)
