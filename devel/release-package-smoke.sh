#!/bin/sh
# Install only in disposable, network-isolated containers. Never on the host.
set -eu
packages=$(cd "${1:?usage: release-package-smoke.sh packages [amd64|arm64] [previous-packages]}" && pwd)
architecture=${2:-amd64}
previous=$(cd "${3:-$packages}" && pwd)
case "$architecture" in amd64|arm64) ;; *) echo 'unsupported architecture' >&2; exit 1 ;; esac

# Verify all supplied package bytes and strictly constrain strings passed to shell.
identity() {
    python3 - "$1" "$architecture" <<'PY'
import hashlib, json, pathlib, re, sys
root, arch = pathlib.Path(sys.argv[1]), sys.argv[2]
records = json.loads((root / 'package-manifest.json').read_text())['packages']
selected = [r for r in records if r['architecture'] == arch]
assert len(selected) == 3
versions = {r['version'] for r in selected}
assert len(versions) == 1
version = versions.pop()
assert re.fullmatch(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-rc\.([1-9][0-9]*)', version)
name = 'jobman-dashboard-' + version[1:].replace('.', '-')
assert {r['file'].rsplit('.', 1)[-1] for r in selected} == {'deb', 'rpm', 'apk'}
for record in selected:
    expected = f'jobman-dashboard_{version}_linux_{arch}.' + record['file'].rsplit('.', 1)[-1]
    assert record['file'] == expected and record['packageName'] == name
    path = root / expected
    assert not path.is_symlink()
    with path.open('rb') as source:
        assert hashlib.file_digest(source, 'sha256').hexdigest() == record['sha256']
print(version)
PY
}
version=$(identity "$packages")
previous_version=$(identity "$previous")
script=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)/release-package-smoke-container.sh
for format in deb rpm apk; do
    case "$format" in
        deb) image=ubuntu:24.04@sha256:561618e2c15bf2397621dd04f96926663a3b5616c189cf7e38db7e82f5c538ea ;;
        rpm) image=fedora:42@sha256:99e203b80b1c3d8f7e161ec10a68fd02b081ef83a3963553e513c82846b97814 ;;
        apk) image=alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40 ;;
    esac
    docker run --rm --network none --platform "linux/$architecture" --pids-limit 128 --memory 512m \
        --mount "type=bind,src=$packages,dst=/packages,readonly" \
        --mount "type=bind,src=$previous,dst=/previous,readonly" \
        --mount "type=bind,src=$script,dst=/smoke.sh,readonly" \
        "$image" sh /smoke.sh "$format" "$architecture" "$version" "$previous_version"
done
echo "Candidate package lifecycle verified for linux/$architecture."
