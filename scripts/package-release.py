#!/usr/bin/env python3
"""Package verified canonical candidate archives without rebuilding their contents.

Packages are version-named so staging an upgrade preserves the active release.
They never own current, private configuration, service units, users or data.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile

NFPM_VERSION = "v2.47.0"
VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-rc\.([1-9][0-9]*)\Z")
ARCHIVE = re.compile(r"jobman-dashboard_(v[0-9.]+-rc\.[0-9]+)_linux_(amd64|arm64)\.tar\.gz\Z")
MAX_BYTES = 1024 * 1024 * 1024
MAX_FILES = 10000
MAX_METADATA_BYTES = 4 * 1024 * 1024


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def checksums(text):
    result = {}
    for line in text.splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9_.+/-]+)", line)
        if not match:
            raise ValueError("invalid checksum record")
        checksum, name = match.groups()
        path = PurePosixPath(name)
        if path.is_absolute() or ".." in path.parts or str(path) != name or name in result:
            raise ValueError("unsafe or duplicate checksum path")
        result[name] = checksum
    if not result:
        raise ValueError("empty checksum inventory")
    return result


def unpack_archive(archive, destination):
    """Verify a canonical archive and unpack exact regular bytes into an empty dir.

    Callers must separately verify its publisher-provided outer checksum.
    Returns build.json. Never executes archive content or follows archive links.
    """
    archive, destination = Path(archive), Path(destination)
    match = ARCHIVE.fullmatch(archive.name)
    if not match or not VERSION.fullmatch(match[1]) or archive.is_symlink() or not archive.is_file():
        raise ValueError("expected a regular canonical candidate archive")
    version, architecture = match.groups()
    if destination.is_symlink() or (destination.exists() and any(destination.iterdir())):
        raise ValueError("unpack destination must be an empty real directory")
    destination.mkdir(parents=True, exist_ok=True)
    root = archive.name.removesuffix(".tar.gz")
    observed, total = {}, 0
    with tarfile.open(archive, "r:gz") as tar:
        for member in tar:
            parts = PurePosixPath(member.name).parts
            total += member.size
            if (not member.isfile() or member.pax_headers or len(parts) < 2 or parts[0] != root
                    or member.name != PurePosixPath(member.name).as_posix() or ".." in parts
                    or total > MAX_BYTES or member.size < 0 or len(observed) >= MAX_FILES):
                raise ValueError("unsafe, nonregular or oversized archive entry")
            relative = PurePosixPath(*parts[1:]).as_posix()
            if not re.fullmatch(r"[A-Za-z0-9_.+/-]+", relative) or relative in observed:
                raise ValueError("unsafe or duplicate archive member")
            if relative in ("build.json", "SHA256SUMS") and member.size > MAX_METADATA_BYTES:
                raise ValueError("archive metadata exceeds size limit")
            expected_mode = 0o755 if parts[1] == "bin" else 0o644
            if member.mode != expected_mode or member.uid != 0 or member.gid != 0:
                raise ValueError("archive ownership or permissions are not canonical")
            target = destination / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            with tar.extractfile(member) as source, target.open("xb") as out:
                shutil.copyfileobj(source, out)
            target.chmod(expected_mode)
            observed[relative] = digest(target)
    inventory = checksums((destination / "SHA256SUMS").read_text())
    if inventory != {key: value for key, value in observed.items() if key != "SHA256SUMS"}:
        raise ValueError("archive member checksums or inventory disagree")
    metadata = json.loads((destination / "build.json").read_text())
    if (metadata.get("version") != version or metadata.get("architecture") != architecture
            or metadata.get("os") != "linux" or metadata.get("releaseState") != "candidate"
            or metadata.get("formatVersion") != 1
            or not isinstance(metadata.get("revision"), str)
            or not re.fullmatch(r"[0-9a-f]{40}", metadata["revision"])
            or type(metadata.get("sourceDateEpoch")) is not int or metadata["sourceDateEpoch"] <= 0):
        raise ValueError("archive provenance disagrees with canonical candidate identity")
    required = {"bin/jobman-dashboard", "bin/jobman-log-broker", "web/index.html", "LICENSE"}
    if not required.issubset(observed):
        raise ValueError("archive lacks required runtime or license files")
    return metadata


def package_name(version):
    if not VERSION.fullmatch(version):
        raise ValueError("expected candidate version")
    return "jobman-dashboard-" + version[1:].replace(".", "-")


def configuration(bundle, metadata):
    stamp = datetime.datetime.fromtimestamp(metadata["sourceDateEpoch"], datetime.timezone.utc).isoformat()
    prefix = "/opt/jobman-dashboard/releases/" + metadata["version"]
    contents = []
    for path in sorted(bundle.rglob("*")):
        if path.is_file():
            relative = path.relative_to(bundle).as_posix()
            contents.append({"src": str(path), "dst": prefix + "/" + relative,
                             "file_info": {"owner": "root", "group": "root", "mtime": stamp,
                                           "mode": 0o755 if relative.startswith("bin/") else 0o644}})
    return {"name": package_name(metadata["version"]), "arch": metadata["architecture"],
            "platform": "linux", "version": metadata["version"], "release": "1", "mtime": stamp,
            "maintainer": "Ryan Wallace <ryancswallace@gmail.com>", "license": "MIT",
            "homepage": "https://github.com/ryancswallace/Jobman-Dashboard",
            "description": "Jobman Dashboard candidate; explicit operator activation required.",
            "disable_globbing": True, "contents": contents}


def verify_nfpm(nfpm):
    # go install embeds the immutable module version even when --version says dev.
    result = subprocess.check_output(["go", "version", "-m", str(nfpm)], text=True, timeout=30)
    if not re.search(r"\n\s*mod\s+github\.com/goreleaser/nfpm/v2\s+" + re.escape(NFPM_VERSION) + r"\s+h1:", result) or "\n\t=>" in result:
        raise ValueError("nFPM must be installed from github.com/goreleaser/nfpm/v2/cmd/nfpm@" + NFPM_VERSION)


def package(input_directory, output_directory, nfpm):
    if not output_directory.is_absolute() or output_directory.exists() or output_directory.is_symlink():
        raise ValueError("output directory must be a new absolute path")
    if input_directory.is_symlink() or not input_directory.is_dir():
        raise ValueError("input must be a real canonical archive directory")
    inventory_path = input_directory / "SHA256SUMS"
    if inventory_path.is_symlink() or not inventory_path.is_file() or inventory_path.stat().st_size > MAX_METADATA_BYTES:
        raise ValueError("checksum inventory must be a regular file")
    inventory = checksums(inventory_path.read_text())
    archives = sorted(input_directory.glob("*.tar.gz"))
    if not archives or set(inventory) != {path.name for path in archives}:
        raise ValueError("outer checksum inventory must cover exactly the candidate archives")
    verify_nfpm(nfpm)
    output_directory.mkdir(mode=0o755)
    manifest = {"formatVersion": 1, "nfpmVersion": NFPM_VERSION, "packages": []}
    identity = None
    with tempfile.TemporaryDirectory(prefix="jobman-dashboard-packages-") as temp:
        staging = Path(temp)
        for archive in archives:
            if archive.is_symlink() or digest(archive) != inventory[archive.name]:
                raise ValueError("outer archive checksum mismatch")
            bundle = staging / archive.name.removesuffix(".tar.gz")
            metadata = unpack_archive(archive, bundle)
            candidate_identity = (metadata["version"], metadata["revision"], metadata["sourceDateEpoch"])
            if identity is not None and candidate_identity != identity:
                raise ValueError("candidate archives must share version, revision and source timestamp")
            identity = candidate_identity
            config = staging / "nfpm.json"
            config.write_text(json.dumps(configuration(bundle, metadata), indent=2) + "\n")
            env = {key: os.environ[key] for key in ("PATH", "HOME") if key in os.environ}
            env.update(SOURCE_DATE_EPOCH=str(metadata["sourceDateEpoch"]), TZ="UTC", LC_ALL="C")
            for kind in ("deb", "rpm", "apk"):
                target = output_directory / (archive.name.removesuffix(".tar.gz") + "." + kind)
                subprocess.run([str(nfpm), "package", "--config", str(config), "--packager", kind,
                                "--target", str(target)], check=True, env=env, timeout=300)
                manifest["packages"].append({"file": target.name, "sha256": digest(target),
                    "archive": archive.name, "archiveSHA256": inventory[archive.name],
                    "version": metadata["version"], "revision": metadata["revision"],
                    "architecture": metadata["architecture"], "packageName": package_name(metadata["version"]),
                    "installPrefix": "/opt/jobman-dashboard/releases/" + metadata["version"]})
    (output_directory / "package-manifest.json").write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n")
    (output_directory / "SHA256SUMS").write_text("".join(
        f"{digest(path)}  {path.name}\n" for path in sorted(output_directory.iterdir())))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input-directory", type=Path, required=True)
    parser.add_argument("--output-directory", type=Path, required=True)
    parser.add_argument("--nfpm", type=Path, default=Path(__file__).resolve().parent.parent / "bin/nfpm")
    args = parser.parse_args()
    try:
        package(args.input_directory.resolve(), args.output_directory, args.nfpm.resolve())
    except (ValueError, OSError, tarfile.TarError, subprocess.SubprocessError) as error:
        parser.exit(1, f"Candidate packaging failed: {error}\nRetry into a new output directory.\n")


if __name__ == "__main__":
    main()
