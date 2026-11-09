#!/usr/bin/env python3
"""Generate observed release dependencies and upstream notices, not legal conclusions.

Run after a locked production web build. Uses only the Python standard library
and the pinned Go toolchain; never resolves floating package versions.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
INVENTORY = "THIRD_PARTY_INVENTORY.json"
NOTICES = "THIRD_PARTY_NOTICES.md"
LOCKS = ("go.mod", "go.sum", "go.version", "web/package.json", "web/package-lock.json", "node.version", "npm.version")
NOTICE = re.compile(r"^(LICENSE|LICENCE|COPYING|COPYRIGHT|NOTICE|PATENTS|THIRD[._-]PARTY[._-](?:LICENSE|LICENCE|NOTICES?))(?:[._-].*)?$", re.IGNORECASE)
NOTICE_TEXT_SUFFIXES = {"", ".txt", ".md", ".markdown", ".rst", ".html", ".htm"}
BINARIES = ("jobman-dashboard", "jobman-log-broker")
ARCHITECTURES = ("amd64", "arm64")


def digest(data):
    return hashlib.sha256(data).hexdigest()


def read_json(path):
    return json.loads(path.read_text(encoding="utf-8"))


def notices(directory):
    """Retain complete upstream notice files, including nested notices.

    These are observations of the published package, not a claim that every
    component described by a combined upstream notice is in our executable.
    """
    result = []
    for path in sorted(directory.rglob("*")):
        relative = path.relative_to(directory)
        if any(part in {"node_modules", ".git"} for part in relative.parts):
            continue
        if path.is_file() and NOTICE.fullmatch(path.name) and path.suffix.lower() in NOTICE_TEXT_SUFFIXES:
            if path.is_symlink():
                raise ValueError(f"notice must be a regular file: {path}")
            data = path.read_bytes()
            result.append({"path": relative.as_posix(), "sha256": digest(data), "text": data.decode("utf-8")})
    if not result:
        raise ValueError(f"no upstream notice files in {directory}")
    return result


def json_stream(text):
    decoder = json.JSONDecoder()
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        yield value
        text = text.lstrip()[end:]


def go_environment(root):
    env = dict(os.environ, GOTOOLCHAIN="go" + (root / "go.version").read_text().strip(), GOWORK="off", CGO_ENABLED="0", GOFLAGS="-mod=readonly")
    env.pop("GOROOT", None)
    return env


def capture(args, root, env):
    return subprocess.check_output(args, cwd=root, env=env, text=True)


def go_packages(root):
    env = go_environment(root)
    settings = json.loads(capture(["go", "env", "-json", "GOROOT", "GOVERSION"], root, env))
    if settings["GOVERSION"] != env["GOTOOLCHAIN"]:
        raise ValueError("Go toolchain does not match go.version")
    standard = Path(settings["GOROOT"])
    standard_notices = []
    for name in ("LICENSE", "PATENTS"):
        data = (standard / name).read_bytes()
        standard_notices.append({"path": name, "sha256": digest(data), "text": data.decode("utf-8")})
    packages = {"go": {"ecosystem": "go-toolchain", "name": "Go standard library", "version": settings["GOVERSION"],
                       "source": {"url": "https://go.dev/"}, "observations": [], "notices": standard_notices}}
    for binary in BINARIES:
        for architecture in ARCHITECTURES:
            observation = {"binary": binary, "os": "linux", "architecture": architecture}
            packages["go"]["observations"].append(observation)
            data = capture(["go", "list", "-deps", "-json", "./cmd/" + binary], root, env | {"GOOS": "linux", "GOARCH": architecture})
            for package in json_stream(data):
                module = package.get("Module")
                if not module or module.get("Main"):
                    continue
                if module.get("Replace") or not module.get("Version") or not module.get("Sum") or not module.get("Dir"):
                    raise ValueError("dependency must have an immutable unreplaced module source: " + module["Path"])
                key = module["Path"] + "@" + module["Version"]
                if key not in packages:
                    packages[key] = {"ecosystem": "go", "name": module["Path"], "version": module["Version"],
                        "source": {"moduleSum": module["Sum"], "url": "https://pkg.go.dev/" + key},
                        "observations": [], "notices": notices(Path(module["Dir"]))}
                if observation not in packages[key]["observations"]:
                    packages[key]["observations"].append(observation)
    return list(packages.values())


def npm_packages(root):
    web = root / "web"
    lock = read_json(web / "package-lock.json")
    graph = read_json(web / "dist/third-party-web.json")
    if graph.get("schemaVersion") != 1 or graph.get("lockSHA256") != digest((web / "package-lock.json").read_bytes()):
        raise ValueError("web bundle inventory is stale; rebuild locked web assets")
    packages = {}
    for observation in graph["observations"]:
        if observation.get("kind") == "bundled-module":
            module_path = Path(observation["module"])
            if module_path.is_absolute() or ".." in module_path.parts:
                raise ValueError("invalid bundled module path")
            if observation.get("sourceSHA256") != digest((web / module_path).read_bytes()):
                raise ValueError("web module source changed; rebuild locked web assets")
        relative = Path(observation.get("packagePath", observation["module"]))
        if relative.is_absolute() or ".." in relative.parts or relative.parts[0] != "node_modules":
            raise ValueError("invalid package provenance path")
        if "packagePath" not in observation:
            relative = relative.parent
            while relative.as_posix() not in lock["packages"] and relative != Path("."):
                relative = relative.parent
        key = relative.as_posix()
        entry = lock["packages"].get(key, {})
        directory = web / relative
        package = read_json(directory / "package.json")
        if not entry.get("integrity") or not entry.get("resolved") or entry.get("version") != package.get("version") or package.get("name") != key.rsplit("node_modules/", 1)[-1]:
            raise ValueError("installed package disagrees with locked provenance: " + key)
        if key not in packages:
            packages[key] = {"ecosystem": "npm", "name": package["name"], "version": package["version"],
                "source": {"lockPath": key, "integrity": entry["integrity"], "url": entry["resolved"],
                           "packageJSONSHA256": digest((directory / "package.json").read_bytes())},
                "declaredLicense": package.get("license"), "observations": [], "notices": notices(directory)}
        packages[key]["observations"].append(observation)
    if not packages:
        raise ValueError("empty browser dependency graph")
    return list(packages.values())


def inventory(root):
    packages = go_packages(root) + npm_packages(root)
    return {"schemaVersion": 1, "generator": "scripts/generate-notices.py",
            "scope": "Observed Go dependency union for both Linux services and rendered browser modules/helpers. Upstream declarations and notice text are evidence, not legal classification or deployment acceptance.",
            "sourceLocks": {name: digest((root / name).read_bytes()) for name in LOCKS},
            "packages": sorted(packages, key=lambda p: (p["ecosystem"], p["name"], p["version"], json.dumps(p["source"], sort_keys=True)))}


def render_notices(data):
    output = ["<!-- Generated by make gen-notices; DO NOT EDIT. -->\n", "# Third-Party Notices\n",
              data["scope"] + "\n", "See `THIRD_PARTY_INVENTORY.json` for source-lock hashes, package provenance, observed binary targets/browser modules, and original notice bytes as UTF-8 text. Complete upstream notice files are retained; a combined upstream notice may describe additional components.\n"]
    for package in data["packages"]:
        output.append(f"## {package['ecosystem']}: {package['name']} {package['version']}\n")
        output.append("Source: <" + package["source"]["url"] + ">\n")
        for notice in package["notices"]:
            output.append("### " + notice["path"] + "\n")
            output.append("SHA-256: `" + notice["sha256"] + "`\n")
            output.append("\n".join(("    " + line.expandtabs(4)).rstrip() for line in notice["text"].splitlines()) + "\n")
    return "\n".join(output)


def generate(root, check):
    data = inventory(root)
    outputs = {INVENTORY: json.dumps(data, sort_keys=True, indent=2, ensure_ascii=False) + "\n", NOTICES: render_notices(data)}
    for name, value in outputs.items():
        path = root / name
        if check:
            if not path.is_file() or path.read_bytes() != value.encode("utf-8"):
                raise ValueError(name + " is stale; run make gen-notices")
        else:
            path.write_text(value, encoding="utf-8")
    print(f"{'Verified' if check else 'Generated'} notices for {len(data['packages'])} observed dependency packages.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    try:
        generate(ROOT, args.check)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"Third-party inventory failed: {error}\n")


if __name__ == "__main__":
    main()
