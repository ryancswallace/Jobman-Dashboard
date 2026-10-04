#!/usr/bin/env python3
"""Local iPhone archive/export only. Never enrolls accounts or uploads builds."""
import argparse
import contextlib
import importlib.util
import hashlib
import json
import os
from pathlib import Path
import plistlib
import re
import signal
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent
TEAM = re.compile(r"[A-Z0-9]{10}\Z")
IDENTITY = re.compile(r"[A-Fa-f0-9]{40}\Z")
UUID = re.compile(r"[A-Fa-f0-9]{8}(?:-[A-Fa-f0-9]{4}){3}-[A-Fa-f0-9]{12}\Z")
BUNDLE = re.compile(r"[A-Za-z0-9][A-Za-z0-9-]*(?:\.[A-Za-z0-9][A-Za-z0-9-]*)+\Z")

VERSION = re.compile(r"(?:0|[1-9][0-9]{0,3})(?:\.(?:0|[1-9][0-9]{0,3})){2}\Z")
BUILD = re.compile(r"[1-9][0-9]{0,3}(?:\.(?:0|[1-9][0-9]?)){0,2}\Z")

def need(value, message):
    if not value:
        raise ValueError(message)

def plan(args, project_root=None):
    project_root = ROOT if project_root is None else project_root
    output = Path(args.output)
    need(output.is_absolute() and output.resolve() == output and not output.exists(), "Output must be a new absolute canonical directory")
    need(BUNDLE.fullmatch(args.bundle_id), "Invalid bundle identifier")
    need(VERSION.fullmatch(args.version), "Version must have three canonical numeric components (0..9999)")
    need(BUILD.fullmatch(args.build), "Build must have one to three canonical numeric components (1..9999, then 0..99)")
    signed = args.mode != "unsigned"
    if signed:
        need(TEAM.fullmatch(args.team or "") and IDENTITY.fullmatch(args.identity or "") and UUID.fullmatch(args.profile or ""), "Development requires explicit team, installed certificate SHA-1 and profile UUID")
    else:
        need(not any((args.team, args.identity, args.profile, args.archive)), "Unsigned mode cannot accept signing inputs or an archive")
    base = ["/usr/bin/xcodebuild", "-project", str(project_root / "JobmanDashboard.xcodeproj"), "-scheme", "JobmanDashboard", "-configuration", "Release"]
    settings = ["PRODUCT_BUNDLE_IDENTIFIER=" + args.bundle_id, "MARKETING_VERSION=" + args.version, "CURRENT_PROJECT_VERSION=" + args.build, "APNS_ENVIRONMENT=development"]
    if signed:
        settings += ["CODE_SIGN_STYLE=Manual", "DEVELOPMENT_TEAM=" + args.team, "CODE_SIGN_IDENTITY=" + args.identity, "PROVISIONING_PROFILE_SPECIFIER=" + args.profile]
    else:
        settings += ["CODE_SIGNING_ALLOWED=NO", "CODE_SIGNING_REQUIRED=NO"]
    if args.mode == "export-development":
        need(args.archive, "Export requires an archive created by this tool")
        archive = Path(args.archive)
        need(archive.is_absolute() and archive.resolve() == archive and archive.is_dir(), "Archive must be an existing canonical directory")
        record = json.loads((archive.parent / "build-receipt.json").read_bytes())
        need(record.get("completed") is True and record.get("mode") == "development", "Export requires a completed development archive")
        for key in ("team", "identity", "profile", "bundle_id", "version", "build"):
            need(record.get(key) == getattr(args, key), "Signing selection differs from the archive receipt")
        need(record["archive"] == str(archive), "Archive path differs from its receipt")
        verify_archive(archive, record)
        command = ["/usr/bin/xcodebuild", "-exportArchive", "-archivePath", str(archive), "-exportPath", str(output / "export"), "-exportOptionsPlist", str(output / "ExportOptions.plist")]
        export = {"method": "debugging", "destination": "export", "signingStyle": "manual", "teamID": args.team, "signingCertificate": args.identity, "provisioningProfiles": {args.bundle_id: args.profile}, "manageAppVersionAndBuildNumber": False}
    else:
        need(not args.archive, "Archive input is only allowed for export")
        archive = output / "JobmanDashboard.xcarchive"
        command = base + ["-destination", "generic/platform=iOS", "-derivedDataPath", str(output / "DerivedData"), "-archivePath", str(archive), "archive"] + settings
        export = None
    return output, archive, command, export

def archive_facts(archive):
    app = archive / "Products/Applications/JobmanDashboard.app"
    info = plistlib.loads((app / "Info.plist").read_bytes())
    binary = app / "JobmanDashboard"
    need(binary.is_file() and not binary.is_symlink(), "Archive executable missing")
    need(info.get("CFBundleExecutable") == "JobmanDashboard" and info.get("UIDeviceFamily") == [1], "Archive is not the expected iPhone application")
    return {"bundle_id": info["CFBundleIdentifier"], "version": info["CFBundleShortVersionString"], "build": info["CFBundleVersion"], "executableSHA256": hashlib.sha256(binary.read_bytes()).hexdigest(), "infoSHA256": hashlib.sha256((app / "Info.plist").read_bytes()).hexdigest()}

def verify_archive(archive, record):
    facts = archive_facts(archive)
    need(all(record.get(key) == value for key, value in facts.items()), "Archive contents changed after build")
    app = archive / "Products/Applications/JobmanDashboard.app"
    subprocess.run(["/usr/bin/codesign", "--verify", "--deep", "--strict", str(app)], check=True, timeout=30, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    signed = subprocess.run(["/usr/bin/codesign", "-d", "--entitlements", ":-", str(app)], check=True, timeout=30, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    entitlements = plistlib.loads(signed.stdout)
    need(entitlements.get("application-identifier") == record["team"] + "." + record["bundle_id"]
         and entitlements.get("com.apple.developer.team-identifier") == record["team"]
         and entitlements.get("aps-environment") == "development"
         and entitlements.get("get-task-allow") is True, "Archive signing entitlements do not match development selection")

def capture(command, cwd, env=None):
    result = subprocess.run(command, cwd=cwd, env=env, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
    need(len(result.stdout) <= 8192, "Provenance output exceeds its bound")
    return result.stdout.decode().strip()


def unsigned_environment(ambient):
    # Preserve the selected installed Xcode and its ordinary user cache access;
    # exclude ambient compiler/config/preload/signing overrides from the archive.
    env = {key: ambient[key] for key in ("PATH", "HOME", "TMPDIR", "DEVELOPER_DIR") if key in ambient}
    env.update(LANG="en_US.UTF-8", LC_ALL="en_US.UTF-8")
    return env


def unsigned_source(destination, repository=None):
    repository = ROOT.parent if repository is None else repository
    env = unsigned_environment(os.environ)
    need(not capture(["git", "status", "--porcelain", "--untracked-files=normal"], repository, env), "Unsigned candidates require a clean committed checkout")
    revision = capture(["git", "rev-parse", "HEAD"], repository, env)
    need(re.fullmatch(r"[0-9a-f]{40}", revision), "Source revision is not a full Git commit")
    raw = subprocess.check_output(["git", "archive", "--format=tar", revision], cwd=repository, env=env, timeout=30)
    spec = importlib.util.spec_from_file_location("candidate_source", ROOT.parent / "scripts/build-release.py")
    release = importlib.util.module_from_spec(spec); spec.loader.exec_module(release)
    destination.mkdir()
    release.extract_source(raw, destination)
    return {"revision": revision, "sourceArchiveSHA256": hashlib.sha256(raw).hexdigest(), "source": "clean-committed-git-archive"}


def file_digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""): value.update(chunk)
    return value.hexdigest()


def unsigned_archive(archive, output):
    """Bind every retained resource, symbol file and alias, not only the executable."""
    files, entries, total = [], [], 0
    for path in archive.rglob("*"):
        entries.append(path)
        need(len(entries) <= 20000, "Archive inventory exceeds its bound")
    for path in sorted(entries):
        name = path.relative_to(archive).as_posix()
        if path.is_symlink():
            target = os.readlink(path)
            need(not os.path.isabs(target) and path.resolve().is_relative_to(archive.resolve()) and path.exists(), "Archive alias escapes its root")
            files.append({"path": name, "link": target})
        elif path.is_file():
            size = path.stat().st_size; total += size
            need(size <= 1 << 30 and total <= 4 << 30, "Archive content exceeds its bound")
            files.append({"path": name, "bytes": size, "sha256": file_digest(path)})
        else:
            need(path.is_dir(), "Archive contains a special file")
    need(files, "Archive inventory is empty")
    inventory = output / "archive-files.json"
    with inventory.open("x") as stream: stream.write(json.dumps(files, sort_keys=True, indent=2) + "\n")
    target = output / "JobmanDashboard-unsigned.xcarchive.tar.gz"
    with tarfile.open(target, "x:gz", dereference=False) as stream:
        stream.add(archive, arcname=archive.name, recursive=False)
        for path in sorted(entries): stream.add(path, arcname=archive.name + "/" + path.relative_to(archive).as_posix(), recursive=False)
    return {"archiveInventorySHA256": hashlib.sha256(inventory.read_bytes()).hexdigest(),
            "archiveFileCount": len(files), "archiveTarSHA256": file_digest(target),
            "archiveTar": target.name, "installable": False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["unsigned", "development", "export-development"])
    parser.add_argument("--output", required=True)
    parser.add_argument("--bundle-id", default="org.jobman.dashboard")
    parser.add_argument("--version", required=True, help="Three numeric marketing-version components")
    parser.add_argument("--build", required=True, help="Explicit numeric build identifier")
    parser.add_argument("--team")
    parser.add_argument("--identity", help="Existing Apple Development certificate SHA-1")
    parser.add_argument("--profile", help="Existing iPhone development provisioning profile UUID")
    parser.add_argument("--archive")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    output, archive, command, export = plan(args)
    if args.dry_run:
        print(json.dumps({"command": command, "exportOptions": export}, indent=2)); return
    with contextlib.ExitStack() as stack:
        project_root, provenance, env = ROOT, {}, None
        if args.mode == "unsigned":
            source = Path(stack.enter_context(tempfile.TemporaryDirectory(prefix="jobman-native-source-"))) / "source"
            provenance = unsigned_source(source)
            project_root = source / "ios"
            env = unsigned_environment(os.environ)
            provenance["toolchains"] = {
                "xcode": capture(["/usr/bin/xcodebuild", "-version"], project_root, env),
                "swift": capture(["/usr/bin/xcrun", "swift", "--version"], project_root, env),
                "iphoneOSSDK": capture(["/usr/bin/xcrun", "--sdk", "iphoneos", "--show-sdk-version"], project_root, env),
            }
            output, archive, command, export = plan(args, project_root)
        execute(args, output, archive, command, export, project_root, provenance, env)


def execute(args, output, archive, command, export, project_root, provenance, env=None):
    output.mkdir(mode=0o700)
    record = {**provenance, "mode": args.mode, "bundle_id": args.bundle_id, "version": args.version, "build": args.build, "team": args.team, "identity": args.identity, "profile": args.profile, "archive": str(archive), "command": command, "completed": False}
    (output / "build-intent.json").write_text(json.dumps(record, indent=2) + "\n")
    if export:
        (output / "ExportOptions.plist").write_bytes(plistlib.dumps(export))
    with (output / "xcodebuild.log").open("xb") as log:
        process = subprocess.Popen(command, cwd=project_root, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        try:
            code = process.wait(timeout=1200)
        except BaseException:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=10)
            raise
        need(code == 0, "xcodebuild failed; inspect retained xcodebuild.log")
    if not export:
        facts = archive_facts(archive)
        need(all(facts[key] == getattr(args, key) for key in ("bundle_id", "version", "build")), "Built identifier/version/build differ from requested values")
        record.update(facts)
        if args.mode == "development":
            verify_archive(archive, record)
    else:
        need(len(list((output / "export").glob("*.ipa"))) == 1, "Expected one exported development IPA")
    if args.mode == "unsigned":
        record.update(unsigned_archive(archive, output))
    record["buildLogSHA256"] = file_digest(output / "xcodebuild.log")
    record["completed"] = True
    (output / "build-receipt.json").write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps({"completed": True, "mode": args.mode, "output": str(output), "signed": args.mode != "unsigned", "exportedIPA": export is not None}))

if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError, KeyError) as error:
        print("Local packaging failed: " + str(error), file=sys.stderr)
        sys.exit(1)
