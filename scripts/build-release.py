#!/usr/bin/env python3
"""Build secret-free Linux candidate bundles from an exact committed Git tree.

No tag, upload, installation, service change, or Git mutation is performed.
Only vX.Y.Z-rc.N candidates are currently supported: final acceptance is open.
"""
import argparse
import datetime
import gzip
import hashlib
import io
import json
import os
import posixpath
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent
VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-rc\.([1-9][0-9]*)\Z")
ARCHITECTURES = ("amd64", "arm64")
FIRST_PARTY = ("github.com/ryancswallace/jobman", "github.com/ryancswallace/jobman-diagnose")
STABLE_VERSION = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
MODULE = "github.com/ryancswallace/jobman-dashboard/internal/buildinfo"
RUNBOOKS = (
    "ACCESSIBILITY.md",
    "API.md",
    "APNS_PROVIDER.md",
    "ARCHITECTURE.md",
    "ARTIFACT_METADATA.md",
    "AUTHENTICATION.md",
    "COMPATIBILITY.md",
    "CONFIGURATION.md",
    "CONTAINERS.md",
    "DEPENDENCY_FAILURE_ACCEPTANCE.md",
    "DESIGN.md",
    "DEVELOPMENT.md",
    "DEVICE_REVOCATION.md",
    "DIAGNOSIS_STORAGE.md",
    "EVENT_INGESTION.md",
    "EVENT_RECOVERY.md",
    "EVENT_SOURCE.md",
    "FINAL_CANDIDATE.md",
    "GRAPH_CLIENT_ACCEPTANCE.md",
    "IMPLEMENTATION_PROMPT.md",
    "IMPLEMENTATION_STATUS.md",
    "INBOX.md",
    "INSTALLATION.md",
    "LAB_AUTH_ROTATION.md",
    "LAB_EXECUTION.md",
    "LAB_MIXED_LOAD.md",
    "LAB_MULTISOURCE_NOTIFICATIONS.md",
    "LAB_NOTIFICATIONS.md",
    "LAB_REPORT_REFRESH.md",
    "LAB_RESTORE.md",
    "LAB_RUN_CATALOG.md",
    "LAB_SCALE.md",
    "LAB_SPLIT.md",
    "LAB_WEB_SESSION.md",
    "LINUX_INSTALLATION.md",
    "LOG_BROKER.md",
    "NOTIFICATION_PIPELINE.md",
    "NOTIFICATION_RETENTION.md",
    "NOTIFICATION_RULES.md",
    "OPERATIONS.md",
    "OPERATOR_STATUS.md",
    "PROCESS_MODES.md",
    "PROCESS_OBSERVABILITY.md",
    "PURPOSE_KEYS.md",
    "README.md",
    "RELEASE_GAP_AUDIT.md",
    "RELEASE_HANDOFF.md",
    "REPOSITORY_SCAFFOLDING.md",
    "REQUIREMENTS.md",
    "RUN_SELECTION.md",
    "SECURITY_MODEL.md",
    "TARGETS.md",
    "TESTING.md",
    "TROUBLESHOOTING.md",
    "UPGRADING.md",
    "WATCHDOG_DENIAL_CONTRACT.md",
)


def command(args, cwd, env=None):
    # Build diagnostics contain only public source and dependencies. Credentials
    # are never inputs; there is no deployment/config file argument.
    subprocess.run(args, cwd=cwd, env=env, check=True)


def capture(args, cwd, env=None):
    return subprocess.check_output(args, cwd=cwd, env=env, text=True).strip()


def extract_source(archive, destination):
    """Materialize files and internal file aliases without following host links."""
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:") as tar:
        members = tar.getmembers()
        by_name = {}
        for member in members:
            path = PurePosixPath(member.name)
            if path.is_absolute() or ".." in path.parts or not path.parts or not (member.isdir() or member.isfile() or member.issym()) or str(path) in by_name:
                raise ValueError("source archive contains a nonregular or unsafe entry")
            by_name[str(path)] = member
        for member in members:
            path = PurePosixPath(member.name)
            content = member
            if member.issym():
                # The native project aliases its generated Swift contract. Read
                # that exact regular archive entry, never a filesystem target.
                link = PurePosixPath(member.linkname)
                resolved = PurePosixPath(posixpath.normpath(str(path.parent / link)))
                content = by_name.get(str(resolved))
                if link.is_absolute() or resolved.is_absolute() or ".." in resolved.parts or content is None or not content.isfile():
                    raise ValueError("source alias must name a regular file inside the same archive")
            target = destination.joinpath(*path.parts)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with tar.extractfile(content) as source, target.open("xb") as output:
                    shutil.copyfileobj(source, output)
                target.chmod(0o755 if content.mode & 0o111 else 0o644)


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            value.update(chunk)
    return value.hexdigest()


def regular_files(root):
    files = []
    for path in sorted(root.rglob("*")):
        if path.is_symlink() or not (path.is_dir() or path.is_file()):
            raise ValueError("bundle contains a nonregular entry")
        if path.is_file():
            files.append(path)
    return files


def write_checksums(root):
    lines = [f"{digest(path)}  {path.relative_to(root).as_posix()}\n" for path in regular_files(root)]
    with (root / "SHA256SUMS").open("x", encoding="utf-8", newline="\n") as out:
        out.writelines(lines)


def write_archive(root, target, epoch):
    # Stable file order, uid/gid/names/modes/timestamps and gzip header make
    # packaging independent of the build user's home, umask and wall clock.
    files = regular_files(root)
    with target.open("xb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0, compresslevel=9) as zipped:
        with tarfile.open(fileobj=zipped, mode="w", format=tarfile.USTAR_FORMAT) as tar:
            for path in files:
                rel = path.relative_to(root)
                entry = tarfile.TarInfo((Path(root.name) / rel).as_posix())
                entry.size = path.stat().st_size
                entry.mtime = epoch
                entry.mode = 0o755 if rel.parts[0] == "bin" else 0o644
                with path.open("rb") as source:
                    tar.addfile(entry, source)


def build_environment(source, staging, epoch, ambient):
    # Only tool lookup and verified dependency/build caches come from the caller.
    # In particular: no GOEXPERIMENT/ISA overrides, persisted GOENV, Node preload,
    # npm lifecycle/config override, CI secret, proxy credential or LD_PRELOAD.
    env = {key: ambient[key] for key in ("PATH", "HOME", "GOCACHE", "GOMODCACHE") if key in ambient}
    for name in ("npm-user.conf", "npm-global.conf"):
        (staging / name).write_text("")
    env.update(
        GOENV="off", GOWORK="off", GOTOOLCHAIN="go" + (source / "go.version").read_text().strip(),
        GOFLAGS="", GOEXPERIMENT="", GOAMD64="v1", GOARM64="v8.0", CGO_ENABLED="0",
        GOPROXY="https://proxy.golang.org", GOSUMDB="sum.golang.org", GOVCS="*:off",
        TZ="UTC", LC_ALL="C", SOURCE_DATE_EPOCH=str(epoch),
        npm_config_userconfig=str(staging / "npm-user.conf"),
        npm_config_globalconfig=str(staging / "npm-global.conf"),
        npm_config_cache=str(staging / "npm-cache"),
        npm_config_registry="https://registry.npmjs.org/",
    )
    return env


def binary_dependencies(info):
    # The lazy module graph includes modules whose source is never downloaded
    # or compiled. Record the actual executable's embedded, verified dependencies.
    result = []
    for module in info.get("Deps", []):
        if module.get("Replace") or not module.get("Version", "").startswith("v") or not re.fullmatch(r"h1:[A-Za-z0-9+/]{43}=", module.get("Sum", "")):
            raise ValueError("compiled release dependencies require original immutable versions and checksums")
        result.append({key: module[key] for key in ("Path", "Version", "Sum")})
    if not result:
        raise ValueError("executable has no verifiable dependency metadata")
    return sorted(result, key=lambda item: item["Path"])


def upstream_release_pins(dependencies):
    """Candidate provenance only; immutable pseudo-versions are not final tags."""
    found = {}
    for modules in dependencies.values():
        for module in modules:
            if module["Path"] in FIRST_PARTY:
                previous = found.setdefault(module["Path"], module)
                if previous != module:
                    raise ValueError("first-party executable dependencies disagree")
    if set(found) != set(FIRST_PARTY):
        raise ValueError("candidate must record both first-party compiled dependencies")
    return {"dependencies": [found[name] for name in FIRST_PARTY],
            "stableVersions": all(STABLE_VERSION.fullmatch(found[name]["Version"]) is not None for name in FIRST_PARTY),
            "controlCompatibilityVerified": False,
            "finalReleaseEligible": False}


def build(version, architectures, output):
    if not VERSION.fullmatch(version) or not architectures or len(set(architectures)) != len(architectures) or any(a not in ARCHITECTURES for a in architectures):
        raise ValueError("use vX.Y.Z-rc.N and unique linux architectures amd64/arm64")
    if not output.is_absolute() or output.exists() or output.is_symlink():
        raise ValueError("output directory must be a new absolute path")
    if capture(["git", "status", "--porcelain", "--untracked-files=normal"], ROOT):
        raise ValueError("commit all task changes before building a candidate; working tree is not clean")
    revision = capture(["git", "rev-parse", "HEAD"], ROOT)
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("source revision is not a full Git commit")
    epoch = int(capture(["git", "show", "-s", "--format=%ct", revision], ROOT))
    stamp = datetime.datetime.fromtimestamp(epoch, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    archive = subprocess.check_output(["git", "archive", "--format=tar", revision], cwd=ROOT)
    with tempfile.TemporaryDirectory(prefix="jobman-dashboard-release-") as temp:
        staging = Path(temp)
        source = staging / "source"
        source.mkdir()
        extract_source(archive, source)
        env = build_environment(source, staging, epoch, os.environ)
        versions = {
            "go": capture(["go", "env", "GOVERSION"], source, env),
            "node": capture(["node", "--version"], source, env).removeprefix("v"),
            "npm": capture(["npm", "--version"], source, env),
        }
        expected = {"go": env["GOTOOLCHAIN"], "node": (source / "node.version").read_text().strip(), "npm": (source / "npm.version").read_text().strip()}
        if versions != expected:
            raise ValueError("build tool versions must exactly match go.version, node.version and npm.version")
        locked = {name: (source / name).read_bytes() for name in ("go.mod", "go.sum", "web/package-lock.json")}
        command(["go", "mod", "download"], source, env)
        command(["go", "mod", "verify"], source, env)
        modules = capture(["go", "list", "-mod=readonly", "-m", "-json", "all"], source, env)
        decoder, remainder = json.JSONDecoder(), modules
        while remainder.strip():
            module, end = decoder.raw_decode(remainder.lstrip())
            remainder = remainder.lstrip()[end:]
            if module.get("Replace"):
                raise ValueError("release builds reject replaced modules")
            if not module.get("Main") and not module.get("Version"):
                raise ValueError("release dependencies must have immutable versions")
        command(["npm", "ci", "--ignore-scripts", "--no-audit", "--fund=false"], source / "web", env)
        command(["npm", "run", "build"], source / "web", env)
        output.mkdir(mode=0o755)
        for architecture in architectures:
            name = f"jobman-dashboard_{version}_linux_{architecture}"
            bundle = staging / name
            (bundle / "bin").mkdir(parents=True)
            build_env = env | {"GOOS": "linux", "GOARCH": architecture}
            ldflags = f"-X {MODULE}.Version={version} -X {MODULE}.Revision={revision} -X {MODULE}.BuiltAt={stamp}"
            dependencies = {}
            for binary in ("jobman-dashboard", "jobman-log-broker"):
                command(["go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", str(bundle / "bin" / binary), "./cmd/" + binary], source, build_env)
                embedded = json.loads(capture(["go", "version", "-m", "-json", str(bundle / "bin" / binary)], source, env))
                if embedded.get("GoVersion") != versions["go"]:
                    raise ValueError("executable toolchain does not match the selected release toolchain")
                dependencies[binary] = binary_dependencies(embedded)
            if any((source / name).read_bytes() != value for name, value in locked.items()):
                raise ValueError("build changed committed module or npm locks")
            shutil.copytree(source / "web/dist", bundle / "web")
            (bundle / "deploy").mkdir()
            for example in sorted((source / "deploy").glob("*.example.json")):
                shutil.copyfile(example, bundle / "deploy" / example.name)
            shutil.copytree(source / "deploy/systemd", bundle / "deploy/systemd")
            shutil.copytree(source / "deploy/postgres", bundle / "deploy/postgres", ignore=shutil.ignore_patterns("__pycache__", "*.pyc", "test_*"))
            (bundle / "docs").mkdir()
            for name in RUNBOOKS:
                shutil.copyfile(source / "docs" / name, bundle / "docs" / name)
            for name in ("README.md", "LICENSE", "THIRD_PARTY_NOTICES.md", "RELEASE.md", "SECURITY.md", "SUPPORT.md", "CHANGELOG.md", "CITATION.cff", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md"):
                shutil.copyfile(source / name, bundle / name)
            metadata = {"formatVersion": 1, "releaseState": "candidate", "version": version, "revision": revision, "sourceDateEpoch": epoch, "os": "linux", "architecture": architecture, "isa": "v1" if architecture == "amd64" else "v8.0", "toolchains": versions, "goModules": dependencies, "upstreamReleasePins": upstream_release_pins(dependencies), "webLockSHA256": digest(source / "web/package-lock.json")}
            (bundle / "build.json").write_text(json.dumps(metadata, sort_keys=True, indent=2) + "\n")
            write_checksums(bundle)
            write_archive(bundle, output / (bundle.name + ".tar.gz"), epoch)
        write_checksums(output)
    print(f"Built {len(architectures)} candidate bundle(s) from {revision}; no release published or service installed.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--architecture", action="append", choices=ARCHITECTURES)
    parser.add_argument("--output-directory", type=Path, required=True)
    args = parser.parse_args()
    try:
        build(args.version, args.architecture or list(ARCHITECTURES), args.output_directory)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"Candidate build failed: {error}\nPartial output is retained for inspection; retry into a new directory.\n")


if __name__ == "__main__":
    main()
