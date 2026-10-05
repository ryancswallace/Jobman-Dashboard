#!/usr/bin/env python3
"""Verify local GoReleaser snapshots, optionally write SPDX inventories.

Only consumes named local snapshot artifacts. No extraction or publication.
"""
import argparse
import hashlib
from html.parser import HTMLParser
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import tarfile


class AssetLinks(HTMLParser):
    def __init__(self):
        super().__init__()
        self.links = []

    def handle_starttag(self, tag, attrs):
        if tag in {'script', 'link'}:
            self.links.extend(value for key, value in attrs if key in {'src', 'href'} and value)


def check(root):
    checksum = root / 'checksums.txt'
    seen = set()
    archives = []
    for line in checksum.read_text().splitlines():
        match = re.fullmatch(r'([0-9a-f]{64})\s+\*?([^/\\]+)', line)
        if not match:
            raise ValueError('invalid checksum entry')
        digest, name = match.groups()
        path = root / name
        if name in seen or name in {'.', '..'} or path.is_symlink() or not path.is_file():
            raise ValueError('duplicate, missing or nonregular artifact')
        seen.add(name)
        with path.open('rb') as f:
            actual = hashlib.file_digest(f, 'sha256').hexdigest()
        if actual != digest:
            raise ValueError(f'checksum mismatch: {name}')
        if name.endswith('.tar.gz'):
            archives.append(path)
            with tarfile.open(path, 'r:gz') as tar:
                members = tar.getmembers()
                names = set()
                for member in members:
                    p = PurePosixPath(member.name)
                    if p.is_absolute() or '..' in p.parts or not (member.isfile() or member.isdir()) or member.name in names:
                        raise ValueError('unsafe or duplicate archive entry')
                    names.add(member.name)
                    if member.name in {'bin/jobman-dashboard', 'bin/jobman-log-broker'} and (not member.isfile() or not member.mode & 0o111):
                        raise ValueError('service binary must be a regular executable')
                required = {'bin/jobman-dashboard', 'bin/jobman-log-broker', 'web/index.html', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'RELEASE.md', 'docs/LINUX_INSTALLATION.md'}
                if not required <= names:
                    raise ValueError(f'incomplete snapshot: {name}: {sorted(required - names)}')
                index = tar.extractfile('web/index.html')
                parser = AssetLinks()
                parser.feed(index.read().decode('utf-8'))
                for link in parser.links:
                    path = PurePosixPath(link.lstrip('/'))
                    if ':' in link or '..' in path.parts or 'web/' + str(path) not in names:
                        raise ValueError(f'missing or external web asset: {link}')
    for arch in ('amd64', 'arm64'):
        if not any(p.name.endswith(f'_linux_{arch}.tar.gz') for p in archives):
            raise ValueError(f'missing {arch} archive')
    for suffix in ('.deb', '.rpm', '.apk'):
        if sum(name.endswith(suffix) for name in seen) != 2:
            raise ValueError(f'expected two {suffix} packages')
    return [root / name for name in sorted(seen) if name.endswith(('.tar.gz', '.deb', '.rpm', '.apk'))]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sbom', action='store_true')
    parser.add_argument('directory', type=Path)
    args = parser.parse_args()
    artifacts = check(args.directory)
    if args.sbom:
        for artifact in artifacts:
            destination = artifact.with_name(artifact.name + '.spdx.json')
            # Exclusive output prevents accidentally overwriting prior evidence.
            with destination.open('x') as output:
                subprocess.run([os.environ.get('SYFT', 'syft'), 'scan', str(artifact), '-o', 'spdx-json'], stdout=output, check=True, timeout=300)
    print(f'Verified {len(artifacts)} engineering snapshot artifacts; nothing published.')


if __name__ == '__main__':
    main()
