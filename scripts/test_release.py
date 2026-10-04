import hashlib
import importlib.util
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("build_release", Path(__file__).with_name("build-release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseTests(unittest.TestCase):
    def test_curated_docs_include_current_behavior_and_release_gates(self):
        self.assertTrue({"RUN_SELECTION.md", "RELEASE_GAP_AUDIT.md", "FINAL_CANDIDATE.md"}.issubset(release.RUNBOOKS))
        self.assertEqual(len(release.RUNBOOKS), len(set(release.RUNBOOKS)))
        for name in release.RUNBOOKS:
            self.assertEqual(Path(name).name, name)
            self.assertTrue((release.ROOT / "docs" / name).is_file(), name)

    def test_only_actual_compiled_dependencies_have_source_checksum_claims(self):
        module = {"Path": "example.org/dependency", "Version": "v1.2.3", "Sum": "h1:" + "a" * 43 + "="}
        self.assertEqual(release.binary_dependencies({"Deps": [module]}), [module])
        for invalid in [{}, {"Deps": [module | {"Sum": ""}]}, {"Deps": [module | {"Replace": {"Path": "/private/local"}}]}, {"Deps": [module | {"Version": "(devel)"}]}]:
            with self.assertRaises(ValueError):
                release.binary_dependencies(invalid)

    def test_first_party_stable_gate_does_not_reject_transitive_pseudo_versions(self):
        modules = [{"Path": name, "Version": "v1.2.3", "Sum": "h1:" + "a" * 43 + "="} for name in release.FIRST_PARTY]
        modules.append({"Path": "example.org/transitive", "Version": "v0.0.0-20260101000000-abcdef012345", "Sum": "h1:" + "b" * 43 + "="})
        result = release.upstream_release_pins({"dashboard": modules})
        self.assertTrue(result["stableVersions"])
        self.assertFalse(result["controlCompatibilityVerified"])
        self.assertFalse(result["finalReleaseEligible"])
        for version in ("v1.2.4-0.20260101000000-abcdef012345", "v1.2.3-rc.1", "v1.2.3+incompatible", "v01.2.3"):
            changed = [dict(m) for m in modules]; changed[0]["Version"] = version
            self.assertFalse(release.upstream_release_pins({"dashboard": changed})["stableVersions"])
        with self.assertRaises(ValueError): release.upstream_release_pins({"broker": [modules[0]]})
        with self.assertRaises(ValueError):
            release.upstream_release_pins({"dashboard": modules, "broker": [modules[0] | {"Version": "v1.2.4"}]})

    def test_ambient_build_overrides_and_secrets_are_not_inherited(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "go.version").write_text("1.26.6\n")
            ambient = {key: "unsafe-ambient" for key in (
                "GOENV", "GOFLAGS", "GOEXPERIMENT", "GOAMD64", "GOARM64", "GOWORK", "GOROOT",
                "NODE_OPTIONS", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "NPM_CONFIG_USERCONFIG",
                "npm_config_registry", "HTTPS_PROXY", "GITHUB_TOKEN", "NODE_ENV", "GOVCS",
            )}
            ambient.update(PATH="/approved/bin", HOME="/approved/home", GOCACHE="/approved/cache")
            env = release.build_environment(root, root, 123, ambient)
            self.assertNotIn("unsafe-ambient", env.values())
            for key in ("NODE_OPTIONS", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "GITHUB_TOKEN", "HTTPS_PROXY", "GOROOT"):
                self.assertNotIn(key, env)
            self.assertEqual((env["GOENV"], env["GOAMD64"], env["GOARM64"], env["GOEXPERIMENT"]), ("off", "v1", "v8.0", ""))
            self.assertEqual(env["GOVCS"], "*:off")
            self.assertEqual(Path(env["npm_config_userconfig"]).read_text(), "")
            self.assertEqual(Path(env["npm_config_globalconfig"]).read_text(), "")

    def test_source_rejects_links_and_traversal(self):
        for name, kind in [("../outside", tarfile.REGTYPE), ("/absolute", tarfile.REGTYPE), ("link", tarfile.SYMTYPE), ("hard", tarfile.LNKTYPE)]:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temp:
                data = io.BytesIO()
                with tarfile.open(fileobj=data, mode="w") as archive:
                    member = tarfile.TarInfo(name)
                    member.type = kind
                    member.linkname = "outside"
                    archive.addfile(member)
                with self.assertRaises(ValueError):
                    release.extract_source(data.getvalue(), Path(temp))

    def test_internal_source_alias_uses_only_regular_archive_bytes(self):
        for target in ["../../../contracts/swift/api.swift", "/private/secret", "../../../../outside", "../../../contracts/swift/alias.swift"]:
            with self.subTest(target=target), tempfile.TemporaryDirectory() as temp:
                data = io.BytesIO()
                with tarfile.open(fileobj=data, mode="w") as archive:
                    entry = tarfile.TarInfo("contracts/swift/api.swift")
                    entry.size = 14
                    entry.mode = 0o644
                    archive.addfile(entry, io.BytesIO(b"public fixture"))
                    alias = tarfile.TarInfo("contracts/swift/alias.swift")
                    alias.type, alias.linkname = tarfile.SYMTYPE, "api.swift"
                    archive.addfile(alias)
                    native = tarfile.TarInfo("ios/Sources/DashboardCore/api.swift")
                    native.type, native.linkname = tarfile.SYMTYPE, target
                    archive.addfile(native)
                if target == "../../../contracts/swift/api.swift":
                    release.extract_source(data.getvalue(), Path(temp))
                    output = Path(temp) / native.name
                    self.assertFalse(output.is_symlink())
                    self.assertEqual(output.read_bytes(), b"public fixture")
                else:
                    with self.assertRaises(ValueError):
                        release.extract_source(data.getvalue(), Path(temp))

    def test_bundle_normalizes_metadata_and_checksums_exact_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / "candidate"
            (root / "bin").mkdir(parents=True)
            (root / "web").mkdir()
            (root / "bin/app").write_bytes(b"exact public executable fixture")
            (root / "web/index.html").write_bytes(b"<html>fixture</html>")
            release.write_checksums(root)
            checks = (root / "SHA256SUMS").read_text().splitlines()
            for line in checks:
                checksum, name = line.split("  ", 1)
                self.assertEqual(hashlib.sha256((root / name).read_bytes()).hexdigest(), checksum)
            first, second = Path(temp) / "first.tar.gz", Path(temp) / "second.tar.gz"
            release.write_archive(root, first, 1234567890)
            (root / "bin/app").chmod(0o777)
            (root / "web/index.html").chmod(0o600)
            release.write_archive(root, second, 1234567890)
            self.assertEqual(first.read_bytes(), second.read_bytes())
            with tarfile.open(first) as archive:
                self.assertEqual(archive.getnames(), ["candidate/SHA256SUMS", "candidate/bin/app", "candidate/web/index.html"])
                for entry in archive:
                    self.assertEqual((entry.uid, entry.gid, entry.uname, entry.gname, entry.mtime), (0, 0, "", "", 1234567890))
                    self.assertEqual(entry.mode, 0o755 if "/bin/" in entry.name else 0o644)
            with self.assertRaises(FileExistsError):
                release.write_archive(root, first, 1234567890)

    def test_symlink_content_never_enters_bundle(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "secret-link").symlink_to("/private/credential")
            with self.assertRaises(ValueError):
                release.write_checksums(root)

    def test_only_explicit_candidates_and_supported_architectures(self):
        for version in ["v0.1.0", "v0.01.0-rc.1", "v0.1.0-rc.0", "v0.1.0-rc.1;echo bad"]:
            with self.assertRaises(ValueError):
                release.build(version, ["arm64"], Path("/tmp/unused-release-output"))
        with self.assertRaises(ValueError):
            release.build("v0.1.0-rc.1", ["arm64", "arm64"], Path("/tmp/unused-release-output"))


if __name__ == "__main__":
    unittest.main()
