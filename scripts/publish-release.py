#!/usr/bin/env python3
"""Fail-closed, RC-only GitHub publication. Never replaces tags or assets."""
import argparse
import hashlib
import importlib.util
import json
import os
import platform
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
RC = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-rc\.([1-9][0-9]*)\Z")
SHA = re.compile(r"[0-9a-f]{40}\Z")
DIGEST = re.compile(r"sha256:[0-9a-f]{64}\Z")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\Z")
GATES = ("ci.yml", "repository.yml", "codeql.yml", "fuzz.yml", "scorecard.yml")
ARCHES = ("amd64", "arm64")


def need(value, message):
    if not value:
        raise ValueError(message)


def run(args, **kwargs):
    return subprocess.run(args, check=True, timeout=kwargs.pop("timeout", 300), **kwargs)


def capture(args):
    return run(args, stdout=subprocess.PIPE, text=True).stdout.strip()


def api(path, *args):
    return json.loads(capture(["gh", "api", path, *args]))


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1 << 20), b""):
            value.update(chunk)
    return value.hexdigest()


def selection(version, revision, repository):
    need(RC.fullmatch(version), "Only canonical vX.Y.Z-rc.N candidates may be published")
    need(SHA.fullmatch(revision), "Expected a full source revision")
    need(REPOSITORY.fullmatch(repository), "Invalid repository")


def latest_gate(runs, revision):
    candidates = [r for r in runs if r.get("head_sha") == revision and r.get("head_branch") == "main" and r.get("event") == "push"]
    need(candidates, "Required exact-commit push workflow is missing")
    latest = max(candidates, key=lambda r: (r["id"], r.get("run_attempt", 1)))
    need(latest.get("status") == "completed" and latest.get("conclusion") == "success", "Latest exact-commit workflow did not succeed")


def preflight(args):
    selection(args.version, args.revision, args.repository)
    need(os.environ.get("GITHUB_REF") == "refs/heads/main", "Publication must run from main")
    need(os.environ.get("GITHUB_SHA") == args.revision, "Workflow revision differs from selected source")
    need(api(f"repos/{args.repository}/commits/main")["sha"] == args.revision, "Main advanced; select a newly tested RC")
    for gate in GATES:
        response = api(f"repos/{args.repository}/actions/workflows/{gate}/runs?head_sha={args.revision}&event=push&per_page=100")
        latest_gate(response["workflow_runs"], args.revision)
    # A list endpoint distinguishes an absent tag from authentication/network errors.
    refs = api(f"repos/{args.repository}/git/matching-refs/tags/{args.version}")
    need(not any(r["ref"] == "refs/tags/" + args.version for r in refs), "Tag already exists; recovery requires a new RC, never overwrite")
    releases = api(f"repos/{args.repository}/releases?per_page=100")
    need(not any(r["tag_name"] == args.version for r in releases), "Release already exists; use a new RC")


def package_module():
    spec = importlib.util.spec_from_file_location("package_release", ROOT / "scripts/package-release.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def context(args):
    selection(args.version, args.revision, args.repository)
    need(not args.output.exists(), "Container context must be new")
    read_checksums(args.input)
    args.output.mkdir()
    package = package_module()
    for arch in ARCHES:
        with tempfile.TemporaryDirectory() as temp:
            unpacked = Path(temp) / "bundle"
            metadata = package.unpack_archive(args.input / f"jobman-dashboard_{args.version}_linux_{arch}.tar.gz", unpacked)
            need(metadata["version"] == args.version and metadata["revision"] == args.revision and metadata["architecture"] == arch, "Container bundle identity mismatch")
            target = args.output / arch
            target.mkdir()
            for folder in ("bin", "web"):
                shutil.copytree(unpacked / folder, target / folder)
            for name in ("LICENSE", "THIRD_PARTY_NOTICES.md", "build.json"):
                shutil.copyfile(unpacked / name, target / name)
    shutil.copyfile(ROOT / "deploy/Dockerfile.release", args.output / "Dockerfile")


def same_payload(expected, actual):
    inventories = []
    for root in (expected, actual):
        paths = list(root.rglob("*"))
        need(root.is_dir() and not root.is_symlink() and all(not p.is_symlink() and (p.is_dir() or p.is_file()) for p in paths), "Container payload contains a nonregular entry")
        inventories.append({p.relative_to(root) for p in paths if p.is_file()})
    need(inventories[0] == inventories[1], "Container payload inventory differs")
    for path in inventories[0]:
        need(digest(expected / path) == digest(actual / path) and ((expected / path).stat().st_mode & 0o777) == ((actual / path).stat().st_mode & 0o777), "Container rebuilt or changed canonical bytes/modes")


def platform_manifests(index):
    """Select exactly one immutable runnable child for each supported platform."""
    need(isinstance(index, dict) and isinstance(index.get("manifests"), list), "Image must be a multi-platform manifest index")
    result = {}
    for manifest in index["manifests"]:
        need(isinstance(manifest, dict) and isinstance(manifest.get("platform"), dict), "Image descriptor lacks platform metadata")
        platform = manifest["platform"]
        os_name, arch = platform.get("os"), platform.get("architecture")
        # BuildKit attaches provenance as non-runnable unknown/unknown entries.
        if (os_name, arch) == ("unknown", "unknown"):
            continue
        need(os_name == "linux" and arch in ARCHES and arch not in result, "Image must contain exactly one amd64 and one arm64 Linux manifest")
        digest_value = manifest.get("digest")
        need(isinstance(digest_value, str) and DIGEST.fullmatch(digest_value), "Platform manifest digest must be SHA-256")
        need(manifest.get("mediaType") in ("application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"), "Platform descriptor must identify an image manifest")
        result[arch] = digest_value
    need(set(result) == set(ARCHES) and len(set(result.values())) == len(ARCHES), "Image must contain distinct amd64 and arm64 Linux manifests")
    return result


def verify_container(args):
    need(DIGEST.fullmatch(args.image_digest), "Invalid immutable image digest")
    index_image = args.image + "@" + args.image_digest
    index = json.loads(capture(["docker", "buildx", "imagetools", "inspect", index_image, "--raw"]))
    manifests = platform_manifests(index)
    for arch in ARCHES:
        # Docker's classic store cannot retain two platform images under one
        # index digest. Pull and execute each immutable child independently;
        # the release receipt and attestation still identify the whole index.
        image = args.image + "@" + manifests[arch]
        run(["docker", "pull", "--platform", "linux/" + arch, image], timeout=600)
        container = capture(["docker", "create", "--platform", "linux/" + arch, image])
        try:
            config = json.loads(capture(["docker", "inspect", container]))[0]["Config"]
            need(config["User"] == "10001:10001", "Runtime must be non-root")
            need(config["Labels"]["org.opencontainers.image.revision"] == args.revision and config["Labels"]["org.opencontainers.image.version"] == args.version, "Container labels differ from release")
            runtime_arch = args.runtime_architecture or {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64"}.get(platform.machine())
            if runtime_arch in ("all", arch):
                for binary in ("jobman-dashboard", "jobman-log-broker"):
                    info = json.loads(capture(["docker", "run", "--rm", "--platform", "linux/" + arch, "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--entrypoint", "/usr/local/bin/" + binary, image, "version"]))
                    need(info["version"] == args.version and info["revision"] == args.revision and info["architecture"] == arch and info["os"] == "linux", "Container executable identity mismatch")
                unconfigured = subprocess.run(["docker", "run", "--rm", "--platform", "linux/" + arch, "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", image], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True, timeout=30)
                need(unconfigured.returncode == 1 and "config" in unconfigured.stderr.lower(), "Unconfigured runtime did not fail closed on configuration")
            with tempfile.TemporaryDirectory() as temp:
                output = Path(temp)
                for folder, remote in (("bin", "/usr/local/bin"), ("web", "/usr/share/jobman-dashboard/web")):
                    run(["docker", "cp", container + ":" + remote, str(output / folder)])
                    expected = args.input / arch / folder
                    actual = output / folder
                    same_payload(expected, actual)
        finally:
            run(["docker", "rm", container])
    (args.output / "container.json").write_text(json.dumps({"image": args.image, "digest": args.image_digest, "platforms": ["linux/amd64", "linux/arm64"], "version": args.version, "revision": args.revision, "canonicalPayloadVerified": True}, sort_keys=True, indent=2) + "\n")


def assemble(args):
    selection(args.version, args.revision, args.repository)
    need(not args.output.exists(), "Asset directory must be new")
    read_checksums(args.input)
    read_checksums(args.packages)
    args.output.mkdir()
    for arch in ARCHES:
        for suffix, directory in (("tar.gz", args.input), ("deb", args.packages), ("rpm", args.packages), ("apk", args.packages)):
            name = f"jobman-dashboard_{args.version}_linux_{arch}.{suffix}"
            need((directory / name).is_file() and not (directory / name).is_symlink(), "Missing regular release asset: " + name)
            shutil.copyfile(directory / name, args.output / name)
    manifest = json.loads((args.packages / "package-manifest.json").read_text())
    records = manifest["packages"]
    need(len(records) == 6 and {record["file"] for record in records} == {f"jobman-dashboard_{args.version}_linux_{arch}.{suffix}" for arch in ARCHES for suffix in ("deb", "rpm", "apk")}, "Package manifest inventory differs")
    for record in records:
        arch = record["architecture"]
        archive = f"jobman-dashboard_{args.version}_linux_{arch}.tar.gz"
        need(arch in ARCHES and record["version"] == args.version and record["revision"] == args.revision and record["archive"] == archive and record["archiveSHA256"] == digest(args.input / archive) and record["sha256"] == digest(args.packages / record["file"]), "Package provenance differs from canonical source bytes")
    shutil.copyfile(args.packages / "package-manifest.json", args.output / "package-manifest.json")
    native = json.loads((args.native / "build-receipt.json").read_text())
    need(native.get("completed") is True and native.get("mode") == "unsigned" and native.get("installable") is False and native.get("revision") == args.revision, "Native archive is not the unsigned exact-source candidate")
    need(native["version"] == args.version[1:].split("-rc.")[0], "Native marketing version differs from RC")
    for name, key in (("JobmanDashboard-unsigned.xcarchive.tar.gz", "archiveTarSHA256"), ("archive-files.json", "archiveInventorySHA256")):
        need(digest(args.native / name) == native[key], "Native receipt hash mismatch")
        shutil.copyfile(args.native / name, args.output / name)
    # Retain useful provenance without build commands, runner paths or raw logs.
    keys = ("revision", "sourceArchiveSHA256", "source", "toolchains", "mode", "bundle_id", "version", "build", "completed", "installable", "archiveTarSHA256", "archiveInventorySHA256", "executableSHA256", "infoSHA256")
    (args.output / "native-provenance.json").write_text(json.dumps({k: native[k] for k in keys}, sort_keys=True, indent=2) + "\n")


def checksums(args):
    files = sorted(args.input.iterdir())
    need(files and all(p.is_file() and not p.is_symlink() for p in files), "Assets must be regular files")
    need(not (args.input / "SHA256SUMS").exists(), "Checksum evidence is immutable")
    (args.input / "SHA256SUMS").write_text("".join(f"{digest(p)}  {p.name}\n" for p in files))


def read_checksums(directory):
    result = {}
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9][A-Za-z0-9_.-]*)", line)
        need(match and match[2] not in result and match[2] != "SHA256SUMS", "Invalid or duplicate asset checksum")
        result[match[2]] = match[1]
    need(result, "Empty asset manifest")
    need({p.name for p in directory.iterdir()} == set(result) | {"SHA256SUMS"}, "Missing or extra release assets")
    for name, expected in result.items():
        path = directory / name
        need(path.is_file() and not path.is_symlink() and digest(path) == expected, "Release asset digest mismatch: " + name)
    return result


def attest(path, args):
    run(["gh", "attestation", "verify", str(path), "--repo", args.repository, "--signer-workflow", args.repository + "/.github/workflows/release.yml", "--source-ref", "refs/heads/main", "--source-digest", args.revision, "--deny-self-hosted-runners"], timeout=120)


def expected_assets(version):
    payloads = {f"jobman-dashboard_{version}_linux_{arch}.{suffix}" for arch in ARCHES for suffix in ("tar.gz", "deb", "rpm", "apk")}
    payloads.add("JobmanDashboard-unsigned.xcarchive.tar.gz")
    return payloads | {name + ".spdx.json" for name in payloads} | {"archive-files.json", "native-provenance.json", "package-manifest.json", "container.json", "container-amd64.spdx.json", "container-arm64.spdx.json"}


def stage(args):
    preflight(args)
    need(set(read_checksums(args.input)) == expected_assets(args.version), "Release payload/SBOM set is incomplete or unexpected")
    # Creating the ref reserves the version. A partial failure intentionally
    # remains immutable and requires a new RC rather than destructive repair.
    api(f"repos/{args.repository}/git/refs", "--method", "POST", "-f", "ref=refs/tags/" + args.version, "-f", "sha=" + args.revision)
    notes = ("Engineering release candidate; final release eligibility remains false. "
             "Corporate AD FS/direct AD, real APNs, signing/internal distribution, "
             "company-managed-device and pilot acceptance remain open. "
             "The native archive is unsigned and cannot be installed. "
             "Containers and packages contain the canonical Linux candidate bytes. "
             "See RELEASE.md and docs/FINAL_CANDIDATE.md for installation and gates.")
    run(["gh", "release", "create", args.version, "--repo", args.repository, "--verify-tag", "--draft", "--prerelease", "--latest=false", "--title", "Jobman Dashboard " + args.version + " (engineering RC)", "--notes", notes])
    run(["gh", "release", "upload", args.version, "--repo", args.repository, *[str(p) for p in sorted(args.input.iterdir())]], timeout=900)


def publish(args):
    selection(args.version, args.revision, args.repository)
    local = read_checksums(args.input)
    need(set(local) == expected_assets(args.version), "Release payload/SBOM set is incomplete or unexpected")
    release = api(f"repos/{args.repository}/releases/tags/{args.version}")
    need(release["draft"] is True and release["prerelease"] is True, "Only a staged draft prerelease can be published")
    need(api(f"repos/{args.repository}/git/ref/tags/{args.version}")["object"]["sha"] == args.revision, "Reserved release tag moved")
    need({a["name"] for a in release["assets"]} == set(local) | {"SHA256SUMS"} and len(release["assets"]) == len(local) + 1, "Remote asset inventory differs")
    with tempfile.TemporaryDirectory() as temp:
        directory = Path(temp)
        run(["gh", "release", "download", args.version, "--repo", args.repository, "--dir", str(directory)], timeout=900)
        need(digest(directory / "SHA256SUMS") == digest(args.input / "SHA256SUMS"), "Remote checksum manifest differs")
        need(read_checksums(directory) == local, "Remote assets changed")
        for path in sorted(directory.iterdir()):
            attest(path, args)
        container = json.loads((directory / "container.json").read_text())
        need(container["revision"] == args.revision and container["version"] == args.version and container["canonicalPayloadVerified"] is True and DIGEST.fullmatch(container["digest"]), "Invalid container receipt")
        attest("oci://" + container["image"] + "@" + container["digest"], args)
        resolved = capture(["docker", "buildx", "imagetools", "inspect", container["image"] + ":" + args.version, "--format", "{{.Manifest.Digest}}"])
        need(resolved == container["digest"], "Versioned container tag moved")
    # Never mark any candidate as latest, stable, or final-release eligible.
    run(["gh", "release", "edit", args.version, "--repo", args.repository, "--draft=false", "--prerelease", "--latest=false"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("preflight", "context", "verify-container", "assemble", "checksums", "stage", "publish"))
    parser.add_argument("--version", default=os.environ.get("RELEASE_VERSION", ""))
    parser.add_argument("--revision", default=os.environ.get("GITHUB_SHA", ""))
    parser.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY", ""))
    for option in ("input", "output", "packages", "native"):
        parser.add_argument("--" + option, type=Path)
    parser.add_argument("--image")
    parser.add_argument("--image-digest")
    parser.add_argument("--runtime-architecture", choices=("amd64", "arm64", "all"))
    args = parser.parse_args()
    try:
        globals()[args.command.replace("-", "_")](args)
    except (ValueError, KeyError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f"Release stopped: {error}\nExisting tags, drafts and images are retained. Use a new RC after partial publication.\n")


if __name__ == "__main__":
    main()
