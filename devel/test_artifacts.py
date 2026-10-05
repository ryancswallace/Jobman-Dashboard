import hashlib
import io
from pathlib import Path
import tarfile
import tempfile
import unittest

from artifacts import check


class ArtifactTests(unittest.TestCase):
    def fixture(self, root, missing=None, unsafe=False):
        required = {'bin/jobman-dashboard', 'bin/jobman-log-broker', 'web/index.html', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'RELEASE.md', 'docs/LINUX_INSTALLATION.md', 'web/assets/app.js'}
        for arch in ('amd64', 'arm64'):
            path = root / f'dashboard_linux_{arch}.tar.gz'
            with tarfile.open(path, 'w:gz') as tar:
                for name in sorted(required - {missing}):
                    entry = tarfile.TarInfo(name)
                    data = b'<script src="/assets/app.js"></script>' if name == 'web/index.html' else b'x'
                    entry.size = len(data)
                    entry.mode = 0o755 if name.startswith('bin/') else 0o644
                    tar.addfile(entry, io.BytesIO(data))
                if unsafe:
                    entry = tarfile.TarInfo('../escape')
                    tar.addfile(entry, io.BytesIO())
            for suffix in ('deb', 'rpm', 'apk'):
                (root / f'dashboard_linux_{arch}.{suffix}').write_bytes(b'package fixture')
        self.checksums(root)

    def checksums(self, root):
        files = sorted(p for p in root.iterdir() if p.name != 'checksums.txt')
        (root / 'checksums.txt').write_text(''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n' for p in files))

    def test_complete_snapshot(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            self.assertEqual(len(check(root)), 8)

    def test_corrupt_missing_and_unsafe_archives(self):
        for scenario in ('corrupt', 'missing', 'asset', 'unsafe', 'duplicate', 'path'):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                self.fixture(root, missing={'missing': 'web/index.html', 'asset': 'web/assets/app.js'}.get(scenario), unsafe=scenario == 'unsafe')
                if scenario == 'corrupt':
                    (root / 'dashboard_linux_amd64.deb').write_bytes(b'changed')
                if scenario == 'duplicate':
                    p = root / 'checksums.txt'
                    p.write_text(p.read_text() * 2)
                if scenario == 'path':
                    (root / 'checksums.txt').write_text('0' * 64 + '  ../outside\n')
                with self.assertRaises(ValueError):
                    check(root)


if __name__ == '__main__':
    unittest.main()
