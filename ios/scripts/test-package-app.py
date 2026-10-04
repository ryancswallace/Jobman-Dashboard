#!/usr/bin/env python3
import argparse
import io
import importlib.util
from pathlib import Path
import tempfile
import hashlib
import json
import subprocess
import tarfile
import plistlib
from unittest.mock import patch
import unittest

spec = importlib.util.spec_from_file_location("packaging", Path(__file__).with_name("package-app.py"))
p = importlib.util.module_from_spec(spec); spec.loader.exec_module(p)

class PackagingTests(unittest.TestCase):
    def args(self, output, **changes):
        values = dict(mode="unsigned", output=str(output), bundle_id="org.jobman.dashboard", version="0.1.0", build="2", team=None, identity=None, profile=None, archive=None)
        values.update(changes); return argparse.Namespace(**values)
    def test_unsigned_never_signs_or_updates_provisioning(self):
        with tempfile.TemporaryDirectory() as root:
            _, _, command, export = p.plan(self.args(Path(root).resolve() / "new"))
            self.assertIn("CODE_SIGNING_ALLOWED=NO", command)
            self.assertIn("MARKETING_VERSION=0.1.0", command)
            self.assertIn("CURRENT_PROJECT_VERSION=2", command)
            self.assertNotIn("-allowProvisioningUpdates", command)
            self.assertNotIn("-exportArchive", command)
            self.assertIsNone(export)
    def test_development_requires_explicit_existing_signing_inputs(self):
        with tempfile.TemporaryDirectory() as root:
            output = Path(root).resolve() / "new"
            with self.assertRaises(ValueError): p.plan(self.args(output, mode="development"))
            _, _, command, _ = p.plan(self.args(output, mode="development", team="ABCDEFGHIJ", identity="a"*40, profile="10000000-0000-4000-8000-000000000001"))
            self.assertIn("APNS_ENVIRONMENT=development", command)
            self.assertIn("CODE_SIGN_STYLE=Manual", command)
            self.assertNotIn("-allowProvisioningUpdates", command)
    def test_existing_output_and_mixed_signing_inputs_are_rejected(self):
        with tempfile.TemporaryDirectory() as root:
            with self.assertRaises(ValueError): p.plan(self.args(Path(root).resolve()))
            with self.assertRaises(ValueError): p.plan(self.args(Path(root).resolve() / "new", team="ABCDEFGHIJ"))
    def test_version_build_validation_and_actual_archive_facts(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root).resolve()
            for key, invalid in (("version", ["1", "1.2", "01.2.3", "1.2.3-rc1", "1.2.10000"]), ("build", ["0", "01", "1.100", "10000", "1.2.3.4", "1beta"])):
                for value in invalid:
                    with self.assertRaises(ValueError): p.plan(self.args(root / "new", **{key:value}))
            app = root / "archive/Products/Applications/JobmanDashboard.app"
            app.mkdir(parents=True)
            (app / "JobmanDashboard").write_bytes(b"test executable")
            info = {"CFBundleIdentifier":"org.jobman.dashboard", "CFBundleExecutable":"JobmanDashboard", "UIDeviceFamily":[1], "CFBundleShortVersionString":"1.2.3", "CFBundleVersion":"45.6.7"}
            (app / "Info.plist").write_bytes(plistlib.dumps(info))
            facts = p.archive_facts(root / "archive")
            self.assertEqual((facts["version"], facts["build"]), ("1.2.3", "45.6.7"))
            changed = dict(facts, build="45.6.8")
            with patch.object(p.subprocess, "run") as run, self.assertRaises(ValueError):
                p.verify_archive(root / "archive", changed)
            run.assert_not_called()
    def test_unsigned_environment_excludes_ambient_build_and_signing_overrides(self):
        ambient = {key: "unsafe" for key in ("XCODE_XCCONFIG_FILE", "SDKROOT", "SWIFTFLAGS", "TOOLCHAINS", "CODE_SIGN_IDENTITY", "DYLD_INSERT_LIBRARIES", "GITHUB_TOKEN")}
        ambient.update(PATH="/usr/bin", HOME="/approved/home", DEVELOPER_DIR="/Applications/Xcode.app/Contents/Developer")
        env = p.unsigned_environment(ambient)
        self.assertNotIn("unsafe", env.values())
        self.assertEqual(env["DEVELOPER_DIR"], ambient["DEVELOPER_DIR"])
        self.assertEqual(env["HOME"], ambient["HOME"])

    def test_unsigned_source_uses_committed_bytes_and_refuses_dirty_tree(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve(); repo = root / "repo"; repo.mkdir()
            def git(*args):
                return subprocess.run(["git", "-C", str(repo), *args], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout
            git("init", "-q")
            (repo / ".gitignore").write_text("Local.xcconfig\n")
            (repo / "public").write_bytes(b"reviewed source")
            (repo / "alias").symlink_to("public")
            git("add", ".")
            git("-c", "user.name=Packaging Test", "-c", "user.email=package@example.invalid", "commit", "-qm", "fixture")
            (repo / "Local.xcconfig").write_text("private untracked settings")
            provenance = p.unsigned_source(root / "source", repo)
            self.assertEqual(provenance["revision"], git("rev-parse", "HEAD").decode().strip())
            self.assertEqual(provenance["sourceArchiveSHA256"], hashlib.sha256(git("archive", "--format=tar", "HEAD")).hexdigest())
            self.assertFalse((root / "source/Local.xcconfig").exists())
            self.assertFalse((root / "source/alias").is_symlink())
            self.assertEqual((root / "source/alias").read_bytes(), b"reviewed source")
            (repo / "public").write_bytes(b"uncommitted")
            with self.assertRaisesRegex(ValueError, "clean committed"):
                p.unsigned_source(root / "dirty", repo)
            git("checkout", "--", "public")
            (repo / "unexpected").write_bytes(b"untracked")
            with self.assertRaisesRegex(ValueError, "clean committed"):
                p.unsigned_source(root / "untracked", repo)

    def test_unsigned_archive_inventory_binds_resources_and_tar(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve(); archive = root / "JobmanDashboard.xcarchive"; archive.mkdir()
            (archive / "binary").write_bytes(b"executable")
            (archive / "resource").write_bytes(b"compiled asset")
            (archive / "alias").symlink_to("resource")
            facts = p.unsigned_archive(archive, root)
            inventory = root / "archive-files.json"
            self.assertEqual(facts["archiveInventorySHA256"], hashlib.sha256(inventory.read_bytes()).hexdigest())
            self.assertEqual(facts["archiveTarSHA256"], hashlib.sha256((root / facts["archiveTar"]).read_bytes()).hexdigest())
            self.assertFalse(facts["installable"])
            rows = json.loads(inventory.read_bytes()); self.assertEqual(len(rows), 3)
            with tarfile.open(root / facts["archiveTar"]) as stream:
                for row in rows:
                    member = stream.getmember(archive.name + "/" + row["path"])
                    if "link" in row: self.assertEqual(member.linkname, row["link"])
                    else: self.assertEqual(hashlib.sha256(stream.extractfile(member).read()).hexdigest(), row["sha256"])
            with self.assertRaises(FileExistsError): p.unsigned_archive(archive, root)

    def test_unsigned_main_receipt_binds_source_tools_and_produced_tar(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp).resolve() / "output"
            source_paths = []
            def source(destination):
                (destination / "ios").mkdir(parents=True)
                source_paths.append(destination)
                return {"revision": "a" * 40, "sourceArchiveSHA256": "b" * 64, "source": "clean-committed-git-archive"}
            def build(command, **kwargs):
                self.assertEqual(kwargs["cwd"], source_paths[0] / "ios")
                self.assertNotIn("XCODE_XCCONFIG_FILE", kwargs["env"])
                self.assertIn(str(source_paths[0] / "ios/JobmanDashboard.xcodeproj"), command)
                intent = json.loads((output / "build-intent.json").read_bytes())
                self.assertFalse(intent["completed"])
                self.assertEqual(intent["revision"], "a" * 40)
                archive = Path(command[command.index("-archivePath") + 1])
                app = archive / "Products/Applications/JobmanDashboard.app"; app.mkdir(parents=True)
                (app / "JobmanDashboard").write_bytes(b"synthetic executable")
                (app / "Assets.car").write_bytes(b"synthetic icon")
                (app / "Info.plist").write_bytes(plistlib.dumps({"CFBundleIdentifier": "org.jobman.dashboard", "CFBundleExecutable": "JobmanDashboard", "UIDeviceFamily": [1], "CFBundleShortVersionString": "0.1.0", "CFBundleVersion": "2"}))
                return argparse.Namespace(wait=lambda timeout: 0)
            argv = ["package-app.py", "unsigned", "--version", "0.1.0", "--build", "2", "--output", str(output)]
            with patch.object(p.sys, "argv", argv), patch.object(p, "unsigned_source", side_effect=source), patch.object(p, "capture", return_value="recorded-tool-version"), patch.object(p.subprocess, "Popen", side_effect=build), patch.object(p.sys, "stdout", io.StringIO()):
                p.main()
            receipt = json.loads((output / "build-receipt.json").read_bytes())
            self.assertTrue(receipt["completed"])
            self.assertEqual(receipt["sourceArchiveSHA256"], "b" * 64)
            self.assertEqual(set(receipt["toolchains"]), {"xcode", "swift", "iphoneOSSDK"})
            self.assertEqual(receipt["archiveTarSHA256"], p.file_digest(output / receipt["archiveTar"]))
            self.assertEqual(receipt["buildLogSHA256"], p.file_digest(output / "xcodebuild.log"))
            self.assertFalse(source_paths[0].exists())

    def test_unsigned_archive_rejects_external_alias_and_oversize(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve(); archive = root / "archive"; archive.mkdir()
            (root / "private").write_bytes(b"not included")
            (archive / "alias").symlink_to("../private")
            with self.assertRaisesRegex(ValueError, "alias escapes"):
                p.unsigned_archive(archive, root)
            (archive / "alias").unlink()
            with (archive / "large").open("wb") as stream: stream.truncate((1 << 30) + 1)
            with self.assertRaisesRegex(ValueError, "exceeds its bound"):
                p.unsigned_archive(archive, root)
            self.assertFalse((root / "archive-files.json").exists())

    def test_export_cannot_bypass_completed_signed_archive(self):
        with tempfile.TemporaryDirectory() as root:
            with self.assertRaises(ValueError): p.plan(self.args(Path(root).resolve() / "new", mode="export-development", team="ABCDEFGHIJ", identity="a"*40, profile="10000000-0000-4000-8000-000000000001"))

if __name__ == "__main__": unittest.main()
