#!/usr/bin/env python3
import argparse
import importlib.util
from pathlib import Path
import tempfile
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
    def test_export_cannot_bypass_completed_signed_archive(self):
        with tempfile.TemporaryDirectory() as root:
            with self.assertRaises(ValueError): p.plan(self.args(Path(root).resolve() / "new", mode="export-development", team="ABCDEFGHIJ", identity="a"*40, profile="10000000-0000-4000-8000-000000000001"))

if __name__ == "__main__": unittest.main()
