#!/usr/bin/env python3
"""Upload verified, published RC packages to a configured Cloudsmith repository."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import struct
import tempfile
import time

spec = importlib.util.spec_from_file_location("publish_release", Path(__file__).with_name("publish-release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


def alpine_identity(filename):
    if not filename.endswith(".apk"):
        return None
    match = re.fullmatch(r"jobman-dashboard_(v[0-9]+\.[0-9]+\.[0-9]+-rc\.[0-9]+)_linux_(amd64|arm64)\.apk", filename)
    release.need(match, "Invalid canonical Alpine filename")
    version, arch = match.groups()
    name = "jobman-dashboard-" + version[1:].replace(".", "-")
    package_version = version[1:].replace("-rc.", "_rc.") + "-r1"
    return name, package_version, {"amd64": "x86_64", "arm64": "aarch64"}[arch]


def registry_query(filename):
    identity = alpine_identity(filename)
    if identity:
        name, version, _ = identity
        return f"format:alpine AND name:^{name}$ AND version:^{version}$"
    # Cloudsmith search anchors are not Python regular expressions.
    return "filename:^" + filename + "$"


def matches_package(record, filename):
    identity = alpine_identity(filename)
    if not identity:
        return record.get("filename") == filename
    name, version, arch = identity
    # Cloudsmith normalizes APK filenames; both architectures share the same
    # filename. Require the complete package identity before comparing bytes.
    if record.get("filename") not in (filename, f"{name}-{version}.apk"):
        return False
    release.need(record.get("format") == "alpine" and record.get("name") == name and record.get("version") == version,
                 "Registry Alpine package identity conflicts with the upload")
    architectures = record.get("architectures")
    release.need(isinstance(architectures, list) and len(architectures) == 1 and isinstance(architectures[0], dict),
                 "Registry Alpine architecture is unavailable or ambiguous")
    return architectures[0].get("name") == arch


def package_state(response, filename, expected):
    records = response.get("data", [])
    matching = [item for item in records if matches_package(item, filename)]
    release.need(len(matching) <= 1, "Duplicate registry package identity requires manual investigation")
    if not matching:
        return "missing", None
    record = matching[0]
    release.need(record.get("is_sync_failed") is not True, "Registry synchronization failed")
    if record.get("is_sync_completed") is not True:
        return "pending", record
    checksum = record.get("checksum_sha256", "")
    release.need(re.fullmatch(r"[0-9a-f]{64}", checksum), "Registry package checksum is unavailable")
    if checksum != expected:
        # The source marker identifies the intended upload; it never proves
        # payload integrity. Signed RPMs are downloaded and compared below.
        release.need(filename.endswith(".rpm") and "source-sha256-" + expected in record.get("tags", {}).get("info", []), "Registry package conflicts with the original upload")
    return "present", record


def rpm_content(path):
    """Hash the entire main header+payload, excluding only RPM's signature area.

    Signing may replace the signature header but must leave the lead, all main
    header metadata (including scripts/capabilities), and payload bytes intact.
    Header layout: RPM lead 96 bytes; signature header 16-byte prefix, nindex
    entries of 16 bytes and dataSize bytes; pad signature section to 8 bytes.
    """
    release.need(path.is_file() and not path.is_symlink() and path.stat().st_size <= 1 << 30, "Invalid or oversized RPM")
    with path.open("rb") as stream:
        lead = stream.read(96)
        release.need(len(lead) == 96 and lead[:4] == bytes.fromhex("edabeedb"), "Invalid RPM lead")
        signature = stream.read(16)
        release.need(len(signature) == 16 and signature[:8] == bytes.fromhex("8eade80100000000"), "Invalid RPM signature header")
        count, size = struct.unpack(">II", signature[8:])
        span = 16 + count * 16 + size
        release.need(count <= 4096 and size <= 16 << 20, "RPM signature header exceeds limits")
        offset = 96 + ((span + 7) // 8) * 8
        release.need(offset + 16 < path.stat().st_size, "Truncated RPM body")
        stream.seek(offset)
        header = stream.read(16)
        release.need(header[:8] == bytes.fromhex("8eade80100000000"), "Invalid RPM main header")
        digest = release.hashlib.sha256(header)
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            digest.update(chunk)
        return lead, digest.hexdigest()


def verify_stored_package(record, source, destination, version):
    if record["checksum_sha256"] == release.digest(source):
        return
    release.need(source.suffix == ".rpm", "Only RPM signature changes are permitted")
    with tempfile.TemporaryDirectory() as temp:
        output = Path(temp)
        name = "jobman-dashboard-" + version[1:].replace(".", "-")
        release.run(["cloudsmith", "download", destination, name, "--format", "rpm", "--filename", source.name, "--outfile", str(output / source.name)], timeout=300)
        release.need({p.name for p in output.iterdir()} == {source.name}, "Unexpected registry download inventory")
        stored = output / source.name
        release.need(release.digest(stored) == record["checksum_sha256"], "Downloaded RPM differs from registry digest")
        release.need(rpm_content(stored) == rpm_content(source), "Registry RPM changed content outside its signature header")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY", ""))
    parser.add_argument("--destination", default=os.environ.get("CLOUDSMITH_REPOSITORY", "jobman/stable"))
    args = parser.parse_args()
    release.need(re.fullmatch(r"[a-z0-9][a-z0-9_-]*/[a-z0-9][a-z0-9_-]*", args.destination), "Invalid Cloudsmith destination")
    release.need(release.RC.fullmatch(args.version) and release.REPOSITORY.fullmatch(args.repository), "Expected repository and RC version")
    release.need(os.environ.get("CLOUDSMITH_API_KEY"), "Configure the main environment CLOUDSMITH_API_KEY before dispatch")
    selected = release.api(f"repos/{args.repository}/releases/tags/{args.version}")
    release.need(selected["draft"] is False and selected["prerelease"] is True and selected["tag_name"] == args.version, "Only a published engineering RC can be distributed")
    reference = release.api(f"repos/{args.repository}/git/ref/tags/{args.version}")
    release.need(reference["object"]["type"] == "commit", "Expected the immutable lightweight RC tag")
    args.revision = reference["object"]["sha"]
    release.selection(args.version, args.revision, args.repository)
    with tempfile.TemporaryDirectory() as temp:
        directory = Path(temp)
        release.run(["gh", "release", "download", args.version, "--repo", args.repository, "--dir", temp], timeout=900)
        release.attest(directory / "SHA256SUMS", args)
        checksums = release.read_checksums(directory)
        for arch in release.ARCHES:
            for extension, kind, distro in (("deb", "deb", "any-distro"), ("rpm", "rpm", "any-distro"), ("apk", "alpine", "alpine")):
                name = f"jobman-dashboard_{args.version}_linux_{arch}.{extension}"
                release.need(name in checksums, "Published RC package set is incomplete")
                package = directory / name
                release.attest(package, args)
                command = ["cloudsmith", "list", "packages", args.destination, "--output-format", "json", "--query", registry_query(name)]
                state, record = package_state(json.loads(release.capture(command)), name, checksums[name])
                if state == "missing":
                    release.run(["cloudsmith", "push", kind, args.destination + "/" + distro + "/any-version", str(package), "--tags", "rc,source-sha256-" + checksums[name]], timeout=600)
                # Do not re-upload pending records. Synchronization may add an
                # RPM signature; only that signature area may differ.
                for attempt in range(12):
                    state, record = package_state(json.loads(release.capture(command)), name, checksums[name])
                    if state == "present":
                        verify_stored_package(record, package, args.destination, args.version)
                        print("Verified registry package: " + name)
                        break
                    time.sleep(5)
                else:
                    raise ValueError("Registry package did not become verifiable within one minute")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        raise SystemExit("Cloudsmith publication stopped: " + str(error)) from error
