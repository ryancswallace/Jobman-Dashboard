"""Offline identity and source-isolation tests for native build profiles."""
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from native_profile import ROOT, read_profile, stage


class NativeProfileTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="jobman-native-test-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name).resolve()
        self.profile = self.directory / "profile.json"
        self.value = dict(schemaVersion=1, displayName='Example "Jobs" \\ App',
                          bundleIdentifier="org.example.jobs", authScheme="org.example.jobs.auth",
                          contentScheme="org.example.jobs.content", authLabel="Sign in with Example")
        self.write()

    def write(self):
        self.profile.write_text(json.dumps(self.value))

    def test_staged_project_is_self_contained_and_unsigned(self):
        output = stage(self.profile, self.directory / "app")
        subprocess.run([sys.executable, str(output / "scripts/generate-project.py")], check=True)
        contract = output / "Sources/DashboardCore/DashboardAPI.generated.swift"
        self.assertFalse(contract.is_symlink())
        self.assertEqual(contract.read_bytes(), (ROOT.parent / "contracts/swift/DashboardAPI.generated.swift").read_bytes())
        self.assertTrue((output / "scripts/test-core.sh").stat().st_mode & 0o100)
        project = (output / "JobmanDashboard.xcodeproj/project.pbxproj").read_text()
        for item in (self.value["bundleIdentifier"], self.value["authScheme"], self.value["contentScheme"], json.dumps(self.value["displayName"])):
            self.assertIn(item, project)
        self.assertIn('org.example.jobs.uitests', project)
        self.assertFalse((output / "Config/Signing.local.xcconfig").exists())
        with self.assertRaises(ValueError):
            stage(self.profile, output)

    def test_rejects_ambiguous_identity_and_executable_text(self):
        for key, value in (("schemaVersion", True), ("authScheme", "https"), ("contentScheme", self.value["authScheme"]),
                           ("bundleIdentifier", "bad;name"), ("authLabel", "$(SECRET)"), ("displayName", "line\nbreak"), ("secret", "forbidden")):
            with self.subTest(key=key):
                bad = dict(self.value); bad[key] = value
                self.profile.write_text(json.dumps(bad))
                with self.assertRaises(ValueError): read_profile(self.profile)
        self.profile.write_text('{"schemaVersion":1,"schemaVersion":1}')
        with self.assertRaises(ValueError): read_profile(self.profile)

    def test_rejects_checkout_outputs_and_missing_assets_without_output(self):
        with self.assertRaises(ValueError): stage(self.profile, ROOT / "variant")
        with self.assertRaises(ValueError): stage(self.profile, ROOT.parent / "variant")
        self.value["logoLightFile"]="missing.png"; self.write()
        output = self.directory / "app"
        with self.assertRaises(ValueError): stage(self.profile, output)
        self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
