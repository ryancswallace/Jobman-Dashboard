import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("package_release", Path(__file__).with_name("package-release.py"))
package = importlib.util.module_from_spec(spec)
spec.loader.exec_module(package)


def fixture(directory, version="v0.1.0-rc.8", architecture="arm64", mutate=None, extra=None):
    """Synthetic executables for packaging/lifecycle tests, never release artifacts."""
    name = f"jobman-dashboard_{version}_linux_{architecture}"
    metadata = {"formatVersion": 1, "releaseState": "candidate", "version": version,
                "architecture": architecture, "os": "linux", "revision": "a" * 40,
                "sourceDateEpoch": 1700000000}
    files = {"build.json": json.dumps(metadata).encode(), "LICENSE": b"fixture license\n",
             "bin/jobman-dashboard": f"#!/bin/sh\nprintf '%s\\n' '{version}'\n".encode(),
             "bin/jobman-log-broker": f"#!/bin/sh\nprintf '%s\\n' '{version}'\n".encode(),
             "web/index.html": b"<html>synthetic lifecycle fixture</html>\n"}
    files["SHA256SUMS"] = "".join(f"{hashlib.sha256(data).hexdigest()}  {path}\n"
                                 for path, data in sorted(files.items())).encode()
    if mutate:
        mutate(files)
    archive = directory / (name + ".tar.gz")
    with tarfile.open(archive, "w:gz") as out:
        for path, data in sorted(files.items()):
            info = tarfile.TarInfo(name + "/" + path)
            info.mode = 0o755 if path.startswith("bin/") else 0o644
            info.size, info.mtime = len(data), metadata["sourceDateEpoch"]
            out.addfile(info, io.BytesIO(data))
        if extra:
            out.addfile(extra(name))
    (directory / "SHA256SUMS").write_text(f"{package.digest(archive)}  {archive.name}\n")
    return archive


class PackageTests(unittest.TestCase):
    def test_exact_bytes_and_safe_version_owned_installation(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            archive = fixture(root)
            destination = root / "unpacked"
            metadata = package.unpack_archive(archive, destination)
            config = package.configuration(destination, metadata)
            self.assertEqual(config["name"], "jobman-dashboard-0-1-0-rc-8")
            self.assertNotIn("scripts", config)
            for item in config["contents"]:
                self.assertTrue(item["dst"].startswith("/opt/jobman-dashboard/releases/v0.1.0-rc.8/"))
                self.assertEqual(item["file_info"]["owner"], "root")
            self.assertEqual((destination / "web/index.html").read_bytes(), b"<html>synthetic lifecycle fixture</html>\n")

    def test_corrupt_missing_and_unlisted_content_rejected(self):
        def missing(files):
            files.pop("LICENSE")
        def changed(files):
            files["web/index.html"] += b"tampered"
        def unlisted(files):
            files["surprise.txt"] = b"unlisted"
        for mutate in (missing, changed, unlisted):
            with self.subTest(mutate=mutate), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                with self.assertRaises(ValueError):
                    package.unpack_archive(fixture(root, mutate=mutate), root / "out")

    def test_links_duplicates_traversal_and_wrong_root_rejected(self):
        for path, kind in (("../outside", tarfile.REGTYPE), ("/absolute", tarfile.REGTYPE),
                           ("alias", tarfile.SYMTYPE), ("alias", tarfile.LNKTYPE),
                           ("LICENSE", tarfile.REGTYPE), ("directory", tarfile.DIRTYPE)):
            with self.subTest(path=path, kind=kind), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                def extra(name):
                    entry = tarfile.TarInfo(name + "/" + path)
                    entry.type, entry.linkname, entry.mode = kind, "LICENSE", 0o644
                    return entry
                with self.assertRaises(ValueError):
                    package.unpack_archive(fixture(root, extra=extra), root / "out")

    def test_checksum_inventory_rejects_duplicate_unsafe_and_invalid_records(self):
        checksum = "a" * 64
        for content in ("", f"{checksum}  ../secret\n", f"{checksum}  /secret\n",
                        f"{checksum}  a\n{checksum}  a\n", f"{checksum}  a//b\n", "bad  a\n"):
            with self.subTest(content=content), self.assertRaises(ValueError):
                package.checksums(content)

    def test_existing_output_and_symlink_destinations_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            archive = fixture(root)
            destination = root / "destination"
            destination.symlink_to(root, target_is_directory=True)
            with self.assertRaises(ValueError):
                package.unpack_archive(archive, destination)
            with self.assertRaises(ValueError):
                package.unpack_archive(archive, root)
            with self.assertRaises(ValueError):
                package.package(root, root, Path("/unused"))

    def test_version_identity_is_validated_before_package_names(self):
        for version in ("v0.1.0", "v0.1.0-rc.0", "v0.01.0-rc.1", "../escape"):
            with self.subTest(version=version), self.assertRaises(ValueError):
                package.package_name(version)

    def test_provenance_identity_cannot_be_rewritten_with_matching_hashes(self):
        for changed in ({"version": "v0.1.0-rc.7"}, {"revision": 42}, {"architecture": "amd64"},
                        {"releaseState": "stable"}, {"sourceDateEpoch": True}):
            def mutate(files):
                metadata = json.loads(files["build.json"])
                metadata.update(changed)
                files["build.json"] = json.dumps(metadata).encode()
                files["SHA256SUMS"] = "".join(f"{hashlib.sha256(data).hexdigest()}  {path}\n"
                    for path, data in sorted(files.items()) if path != "SHA256SUMS").encode()
            with self.subTest(changed=changed), tempfile.TemporaryDirectory() as temp:
                root = Path(temp)
                with self.assertRaises(ValueError):
                    package.unpack_archive(fixture(root, mutate=mutate), root / "out")

    def test_packager_requires_pinned_original_module(self):
        embedded = "nfpm:\n\tmod\tgithub.com/goreleaser/nfpm/v2\tv2.47.0\th1:fixture\n"
        with patch.object(package.subprocess, "check_output", return_value=embedded):
            package.verify_nfpm(Path("nfpm"))
        for invalid in (embedded.replace("v2.47.0", "v2.46.0"), embedded + "\n\t=>\t/local/replacement\n"):
            with patch.object(package.subprocess, "check_output", return_value=invalid), self.assertRaises(ValueError):
                package.verify_nfpm(Path("nfpm"))

    def test_outer_checksums_are_verified_before_packaging(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source = root / "source"
            source.mkdir()
            archive = fixture(source)
            archive.write_bytes(archive.read_bytes() + b"changed archive")
            with patch.object(package, "verify_nfpm"), self.assertRaises(ValueError):
                package.package(source, root / "out", Path("unused"))

    def test_archive_entry_count_is_bounded(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            archive = fixture(root)
            with patch.object(package, "MAX_FILES", 2), self.assertRaises(ValueError):
                package.unpack_archive(archive, root / "out")


if __name__ == "__main__":
    unittest.main()
