"""Strict nonsecret native build profile; all output is a new staging tree."""
import json
from pathlib import Path
import re
import shutil
import struct
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SCHEME = re.compile(r"[a-z][a-z0-9.-]{2,79}\Z")
BUNDLE = re.compile(r"[A-Za-z0-9][A-Za-z0-9-]*(?:\.[A-Za-z0-9][A-Za-z0-9-]*)+\Z")
FIELDS = {"schemaVersion", "displayName", "bundleIdentifier", "authScheme", "contentScheme", "authLabel", "logoLightFile", "logoDarkFile", "appIconFile"}
REQUIRED = {"schemaVersion", "displayName", "bundleIdentifier", "authScheme", "contentScheme", "authLabel"}

def unique(pairs):
    result = {}
    for key,value in pairs:
        if key in result: raise ValueError("duplicate native profile field")
        result[key] = value
    return result

def scheme(value):
    return isinstance(value,str) and SCHEME.fullmatch(value) and ".." not in value and not value.endswith(".") and (value in ("jobman-dashboard", "jobman-dashboard-auth") or "." in value)

def read_profile(path):
    path = Path(path)
    if not path.is_absolute() or not path.is_file() or path.is_symlink() or path.stat().st_size > 16384: raise ValueError("native profile must be a bounded absolute regular JSON file")
    value = json.loads(path.read_text(), object_pairs_hook=unique)
    if not isinstance(value,dict) or set(value)-FIELDS or REQUIRED-set(value) or type(value["schemaVersion"]) is not int or value["schemaVersion"] != 1: raise ValueError("unsupported native profile schema")
    for name in ("displayName", "authLabel"):
        text = value[name]
        if not isinstance(text,str) or not text or len(text.encode())>120 or text.strip()!=text or any(ord(c)<32 or ord(c)==127 for c in text) or "$" in text: raise ValueError("invalid native display text")
    if not isinstance(value["bundleIdentifier"],str) or len(value["bundleIdentifier"])>200 or not BUNDLE.fullmatch(value["bundleIdentifier"]): raise ValueError("invalid native bundle identifier")
    if not scheme(value["authScheme"]) or not scheme(value["contentScheme"]) or value["authScheme"]==value["contentScheme"] or value["authScheme"]=="jobman-dashboard": raise ValueError("distinct registered native schemes required")
    for key in ("logoLightFile", "logoDarkFile", "appIconFile"):
        if key in value:
            filename = value[key]
            if not isinstance(filename,str) or not filename or len(filename)>4096: raise ValueError("invalid native image reference")
            image = Path(filename) if Path(filename).is_absolute() else path.parent / filename
            if image.is_symlink() or not image.is_file() or image.suffix.lower()!=".png" or image.stat().st_size>8*1024*1024: raise ValueError("native assets require bounded regular PNG files")
            data=image.read_bytes()
            if len(data)<33 or data[:8]!=b"\x89PNG\r\n\x1a\n" or data[12:16]!=b"IHDR": raise ValueError("native image must be PNG")
            width,height=struct.unpack(">II",data[16:24])
            if min(width,height)<1 or max(width,height)>8192 or key=="appIconFile" and (width,height)!=(1024,1024): raise ValueError("invalid native image dimensions")
            value[key]=str(image.resolve())
    return value

def project_settings(profile):
    return {"PRODUCT_BUNDLE_IDENTIFIER":profile["bundleIdentifier"], "JOBMAN_DISPLAY_NAME":profile["displayName"], "JOBMAN_AUTH_SCHEME":profile["authScheme"], "JOBMAN_CONTENT_SCHEME":profile["contentScheme"], "JOBMAN_AUTH_LABEL":profile["authLabel"]}

def stage(profile_path, output):
    profile=read_profile(profile_path)
    output=Path(output)
    if not output.is_absolute() or output.exists() or output.resolve()!=output or output.is_relative_to(ROOT.parent) or any((ancestor/".git").exists() for ancestor in output.parents): raise ValueError("staging requires a new canonical directory outside repository checkouts")
    if not output.parent.is_dir(): raise ValueError("staging parent directory must already exist")
    # Explicit source families exclude developer-local signing/environment/cache files.
    paths=[ROOT/"Package.swift"]
    for family,patterns in {"Sources":["*.swift"],"App":["*.swift","*.xcprivacy"],"Tests":["*.swift","*.json","README.md"],"UITests":["*.swift"],"scripts":["*.py","*.sh","*.swift"]}.items():
        for pattern in patterns: paths.extend((ROOT/family).rglob(pattern))
    paths.extend(ROOT/"Config"/name for name in ["Debug.xcconfig","Release.xcconfig","Info.plist","Dashboard.entitlements"])
    paths.extend((ROOT/"App/Assets.xcassets").rglob("*"))
    aliases = {
        ROOT/"Sources/DashboardCore/DashboardAPI.generated.swift": ROOT.parent/"contracts/swift/DashboardAPI.generated.swift",
        ROOT/"Tests/DashboardCoreTests/NativeGraphFixtures.swift": ROOT/"App/NativeGraphFixtures.swift",
    }
    with tempfile.TemporaryDirectory(prefix=".jobman-native-",dir=output.parent) as temporary:
        staged = Path(temporary)/"app"
        staged.mkdir(mode=0o700)
        for source in sorted(set(paths)):
            # Materialize only verified repository-owned contract/fixture aliases
            # so the staged package is self-contained.
            if source.is_symlink() and source.resolve() != aliases.get(source): raise ValueError("staging source aliases are not permitted")
            if not source.is_file(): continue
            target=staged/source.relative_to(ROOT); target.parent.mkdir(parents=True,exist_ok=True); shutil.copy2(source,target)
        for key,name in [("logoLightFile","DashboardLogoLight"),("logoDarkFile","DashboardLogoDark"),("appIconFile","AppIcon")]:
            if key not in profile: continue
            directory=staged/"App/Assets.xcassets"/(name+(".appiconset" if key=="appIconFile" else ".imageset"))
            if directory.exists(): shutil.rmtree(directory)
            directory.mkdir(parents=True)
            shutil.copyfile(profile[key],directory/"image.png")
            image={"filename":"image.png","idiom":"universal"}
            if key=="appIconFile": image.update(platform="ios",size="1024x1024")
            (directory/"Contents.json").write_text(json.dumps({"images":[image],"info":{"author":"xcode","version":1}},indent=2)+"\n")
        # This profile contains public build identity only; original paths are excluded.
        public={k:v for k,v in profile.items() if not k.endswith("File")}
        (staged/"Config/BuildProfile.json").write_text(json.dumps(public,indent=2)+"\n")
        # Reserve the destination without replacing even an empty existing directory.
        output.mkdir(mode=0o700)
        try:
            for source in staged.iterdir(): shutil.move(str(source),output/source.name)
        except BaseException:
            shutil.rmtree(output)
            raise
    return output
