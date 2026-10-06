import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import struct
import tempfile
import unittest
from unittest.mock import patch


def load(name):
    spec = importlib.util.spec_from_file_location(name.replace('-', '_'), Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


release = load('publish-release')
registry = load('check-image-unused')
cloudsmith = load('publish-cloudsmith')
REVISION = 'a' * 40
VERSION = 'v0.1.0-rc.8'
REPO = 'owner/dashboard'


class PublicationTests(unittest.TestCase):
    def args(self, **values):
        return argparse.Namespace(version=VERSION, revision=REVISION, repository=REPO, **values)

    def test_stable_and_shell_versions_rejected_before_network(self):
        for version in ('v0.1.0', 'v0.01.0-rc.1', 'v0.1.0-rc.0', 'v0.1.0-rc.1;id'):
            with self.subTest(version=version), patch.object(release, 'api') as api:
                args = self.args(); args.version = version
                with self.assertRaises(ValueError):
                    release.preflight(args)
                api.assert_not_called()

    def test_later_failed_or_running_gate_blocks_older_success(self):
        successful = {'id': 1, 'head_sha': REVISION, 'head_branch': 'main', 'event': 'push', 'status': 'completed', 'conclusion': 'success'}
        release.latest_gate([successful], REVISION)
        for status, conclusion in (('completed', 'failure'), ('in_progress', None), ('completed', 'cancelled')):
            with self.subTest(status=status, conclusion=conclusion), self.assertRaises(ValueError):
                release.latest_gate([successful, successful | {'id': 2, 'status': status, 'conclusion': conclusion}], REVISION)
        for changed in ({'head_sha': 'b' * 40}, {'head_branch': 'feature'}, {'event': 'pull_request'}):
            with self.assertRaises(ValueError):
                release.latest_gate([successful | changed], REVISION)

    def test_existing_tag_blocks_preflight_without_mutation(self):
        responses = [{'sha': REVISION}]
        responses += [{'workflow_runs': [{'id': 1, 'head_sha': REVISION, 'head_branch': 'main', 'event': 'push', 'status': 'completed', 'conclusion': 'success'}]} for _ in release.GATES]
        responses += [[{'ref': 'refs/tags/' + VERSION}]]
        with patch.dict(os.environ, {'GITHUB_REF': 'refs/heads/main', 'GITHUB_SHA': REVISION}), patch.object(release, 'api', side_effect=responses) as api:
            with self.assertRaisesRegex(ValueError, 'Tag already exists'):
                release.preflight(self.args())
            self.assertTrue(all(len(call.args) == 1 for call in api.call_args_list))

    def test_checksums_reject_extra_missing_tampered_duplicate_and_unsafe_names(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / 'artifact').write_bytes(b'candidate')
            release.checksums(self.args(input=root))
            original = (root / 'SHA256SUMS').read_text()
            self.assertEqual(set(release.read_checksums(root)), {'artifact'})
            for content in (original + original, original.replace('artifact', '../outside'), original.replace('artifact', '/absolute')):
                (root / 'SHA256SUMS').write_text(content)
                with self.assertRaises(ValueError): release.read_checksums(root)
            (root / 'SHA256SUMS').write_text(original)
            (root / 'extra').write_bytes(b'extra')
            with self.assertRaises(ValueError): release.read_checksums(root)
            (root / 'extra').unlink()
            (root / 'artifact').write_bytes(b'changed')
            with self.assertRaises(ValueError): release.read_checksums(root)
            (root / 'artifact').unlink()
            with self.assertRaises(ValueError): release.read_checksums(root)
            (root / 'artifact').symlink_to(root / 'SHA256SUMS')
            with self.assertRaises(ValueError): release.read_checksums(root)

    def test_stage_requires_complete_payload_before_any_mutation(self):
        with tempfile.TemporaryDirectory() as temp, patch.object(release, 'preflight'), patch.object(release, 'api') as api, patch.object(release, 'run') as run:
            root = Path(temp); (root / 'arbitrary').write_bytes(b'x')
            release.checksums(self.args(input=root))
            with self.assertRaisesRegex(ValueError, 'incomplete'):
                release.stage(self.args(input=root))
            api.assert_not_called(); run.assert_not_called()

    def test_failed_remote_attestation_never_publishes_draft(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for name in release.expected_assets(VERSION):
                (root / name).write_bytes(b'fixture')
            release.checksums(self.args(input=root))
            record = {'id': 405033021, 'tag_name': VERSION, 'draft': True, 'prerelease': True, 'assets': [{'name': p.name} for p in root.iterdir()]}
            commands = []
            def command(args, **kwargs):
                commands.append(args)
                if args[:3] == ['gh', 'release', 'download']:
                    target = Path(args[args.index('--dir') + 1])
                    for path in root.iterdir(): shutil.copyfile(path, target / path.name)
            with patch.object(release, 'api', side_effect=[[record], record, {'object': {'sha': REVISION}}]), patch.object(release, 'run', side_effect=command), patch.object(release, 'attest', side_effect=ValueError('wrong source')):
                with self.assertRaisesRegex(ValueError, 'wrong source'):
                    release.publish(self.args(input=root))
            self.assertFalse(any(command[:3] == ['gh', 'release', 'edit'] for command in commands))

    def test_draft_discovery_paginates_list_then_fetches_exact_id(self):
        unrelated = [{'id': n + 1, 'tag_name': f'v0.0.0-rc.{n + 1}', 'draft': False} for n in range(100)]
        draft = {'id': 405033021, 'tag_name': VERSION, 'draft': True, 'prerelease': True, 'assets': []}
        with patch.object(release, 'api', side_effect=[unrelated, [draft], draft]) as api:
            self.assertEqual(release.release_for_tag(REPO, VERSION), draft)
        self.assertEqual([call.args[0] for call in api.call_args_list], [f'repos/{REPO}/releases?per_page=100&page=1', f'repos/{REPO}/releases?per_page=100&page=2', f'repos/{REPO}/releases/405033021'])

    def test_draft_discovery_rejects_duplicates_identity_changes_and_unbounded_lists(self):
        draft = {'id': 405033021, 'tag_name': VERSION, 'draft': True}
        for responses in ([[draft, draft]], [[draft], draft | {'tag_name': 'v0.1.0-rc.99'}], [[draft | {'id': '405033021'}]]):
            with self.subTest(responses=responses), patch.object(release, 'api', side_effect=responses), self.assertRaises(ValueError):
                release.release_for_tag(REPO, VERSION)
        with patch.object(release, 'api', return_value=[{'id': n + 1, 'tag_name': 'unrelated'} for n in range(100)]) as api:
            with self.assertRaisesRegex(ValueError, 'bounded discovery'):
                release.release_for_tag(REPO, VERSION)
            self.assertEqual(api.call_count, 10)

    def test_verify_draft_uses_real_list_and_id_shapes_and_never_mutates(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for name in release.expected_assets(VERSION):
                (root / name).write_bytes(b'fixture')
            container = {'image': 'ghcr.io/owner/dashboard', 'digest': 'sha256:' + 'e' * 64, 'version': VERSION, 'revision': REVISION, 'canonicalPayloadVerified': True}
            (root / 'container.json').write_text(json.dumps(container))
            release.checksums(self.args(input=root))
            draft = {'id': 405033021, 'tag_name': VERSION, 'draft': True, 'prerelease': True, 'assets': [{'id': n, 'name': path.name} for n, path in enumerate(root.iterdir())]}
            def download(args, **kwargs):
                self.assertEqual(args[:3], ['gh', 'release', 'download'])
                target = Path(args[args.index('--dir') + 1])
                for path in root.iterdir(): shutil.copyfile(path, target / path.name)
            def api_response(path, *args):
                self.assertFalse(args, 'Verification must use only read APIs')
                if path == f'repos/{REPO}/releases?per_page=100&page=1': return [draft]
                if path == f'repos/{REPO}/releases/405033021': return draft
                if path == f'repos/{REPO}/git/ref/tags/{VERSION}': return {'object': {'sha': REVISION}}
                self.fail('Unexpected endpoint (draft lookup by tag would return404): ' + path)
            with patch.object(release, 'api', side_effect=api_response), patch.object(release, 'run', side_effect=download), patch.object(release, 'attest') as attest, patch.object(release, 'capture', return_value=container['digest']), patch.object(release, 'preflight') as preflight:
                self.assertEqual(release.verify_draft(self.args(input=root)), 405033021)
                preflight.assert_not_called()
                self.assertEqual(attest.call_count, len(release.expected_assets(VERSION)) + 2)
                self.assertEqual(attest.call_args.args[0], 'oci://' + container['image'] + '@' + container['digest'])

    def test_publish_updates_only_verified_id_as_nonlatest_prerelease(self):
        published = {'id': 405033021, 'tag_name': VERSION, 'draft': False, 'prerelease': True}
        with patch.object(release, 'verify_draft', return_value=405033021) as verify, patch.object(release, 'api', return_value=published) as api:
            args = self.args()
            release.publish(args)
            verify.assert_called_once_with(args)
            api.assert_called_once_with(f'repos/{REPO}/releases/405033021', '--method', 'PATCH', '-F', 'draft=false', '-F', 'prerelease=true', '-f', 'make_latest=false')

    def test_image_absence_requires_explicit_registry_result(self):
        for stderr in ('ERROR: ghcr.io/owner/app:v0.1.0-rc.8: not found', 'manifest unknown'):
            self.assertTrue(registry.absent(subprocess.CompletedProcess([], 1, '', stderr)))
        for code, stderr in ((0, ''), (1, 'unauthorized'), (1, 'network timeout'), (1, 'DNS host not found'), (1, '500 internal server error')):
            self.assertFalse(registry.absent(subprocess.CompletedProcess([], code, '', stderr)))

    def test_cloudsmith_shared_repository_keeps_rc_identity_and_verifies_six_packages(self):
        packages = {f'jobman-dashboard_{VERSION}_linux_{arch}.{kind}':
                    f'{arch}-{kind}'.encode() for arch in ('amd64', 'arm64') for kind in ('deb', 'rpm', 'apk')}
        checksums = {name: release.hashlib.sha256(data).hexdigest() for name, data in packages.items()}
        stored, uploads = {}, []

        def run(args, **kwargs):
            if args[:3] == ['gh', 'release', 'download']:
                root = Path(args[args.index('--dir') + 1])
                for name, data in packages.items():
                    (root / name).write_bytes(data)
                return
            self.assertEqual(args[:2], ['cloudsmith', 'push'])
            self.assertTrue(args[3].startswith('jobman/stable/'))
            name = Path(args[4]).name
            self.assertEqual(args[5:], ['--tags', 'rc,source-sha256-' + checksums[name]])
            uploads.append(name)
            stored[name] = {'filename': name, 'checksum_sha256': checksums[name], 'is_sync_completed': True}

        def capture(args):
            self.assertEqual(args[:4], ['cloudsmith', 'list', 'packages', 'jobman/stable'])
            return json.dumps({'data': list(stored.values())})

        def api(path):
            if '/releases/tags/' in path:
                return {'draft': False, 'prerelease': True, 'tag_name': VERSION}
            return {'object': {'type': 'commit', 'sha': REVISION}}

        with patch.dict(os.environ, {'CLOUDSMITH_API_KEY': 'synthetic-test-key', 'GITHUB_REPOSITORY': REPO}, clear=True), \
             patch('sys.argv', ['publish-cloudsmith.py', '--version', VERSION]), \
             patch.object(cloudsmith.release, 'api', side_effect=api), \
             patch.object(cloudsmith.release, 'run', side_effect=run), \
             patch.object(cloudsmith.release, 'capture', side_effect=capture), \
             patch.object(cloudsmith.release, 'attest') as attest, \
             patch.object(cloudsmith.release, 'read_checksums', return_value=checksums), \
             patch('builtins.print'):
            cloudsmith.main()
            self.assertEqual(set(uploads), set(packages))
            self.assertEqual(len(uploads), 6)
            self.assertEqual(attest.call_count, 7)
            cloudsmith.main()
            self.assertEqual(len(uploads), 6, 'a verified rerun must not upload duplicates')

    def test_cloudsmith_idempotence_requires_completed_checksum_or_rpm_comparison(self):
        self.assertEqual(cloudsmith.package_state({'data': []}, 'candidate.deb', 'a' * 64)[0], 'missing')
        item = {'filename': 'candidate.deb', 'checksum_sha256': 'a' * 64, 'is_sync_completed': True}
        self.assertEqual(cloudsmith.package_state({'data': [item]}, 'candidate.deb', 'a' * 64)[0], 'present')
        self.assertEqual(cloudsmith.package_state({'data': [item | {'is_sync_completed': False}]}, 'candidate.deb', 'a' * 64)[0], 'pending')
        for items in ([item, item], [item | {'checksum_sha256': 'b' * 64}], [item | {'is_sync_failed': True}], [{'filename': 'candidate.deb', 'is_sync_completed': True, 'tags': {'info': ['source-sha256-' + 'a' * 64]}}]):
            with self.assertRaises(ValueError): cloudsmith.package_state({'data': items}, 'candidate.deb', 'a' * 64)

    def test_rpm_signatures_may_change_but_main_header_and_payload_may_not(self):
        def rpm(signature, body=b'canonical payload'):
            lead = bytes.fromhex('edabeedb') + bytes(92)
            header = bytes.fromhex('8eade80100000000') + struct.pack('>II', 0, len(signature))
            section = header + signature
            section += bytes((-len(section)) % 8)
            return lead + section + bytes.fromhex('8eade80100000000') + bytes(8) + body
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            source, signed = root / 'source.rpm', root / 'signed.rpm'
            source.write_bytes(rpm(b'original signature'))
            signed.write_bytes(rpm(b'new registry signature with another length'))
            self.assertEqual(cloudsmith.rpm_content(source), cloudsmith.rpm_content(signed))
            signed.write_bytes(rpm(b'resigned', b'changed payload'))
            self.assertNotEqual(cloudsmith.rpm_content(source), cloudsmith.rpm_content(signed))
            signed.write_bytes(rpm(b'resigned')[:-10])
            self.assertNotEqual(cloudsmith.rpm_content(source), cloudsmith.rpm_content(signed))
            signed.write_bytes(b'not an RPM')
            with self.assertRaises(ValueError): cloudsmith.rpm_content(signed)

    def test_signed_rpm_download_is_compared_beyond_source_marker(self):
        def rpm(signature, payload):
            lead = bytes.fromhex('edabeedb') + bytes(92)
            section = bytes.fromhex('8eade80100000000') + struct.pack('>II', 0, len(signature)) + signature
            section += bytes((-len(section)) % 8)
            return lead + section + bytes.fromhex('8eade80100000000') + bytes(8) + payload
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / 'candidate.rpm'
            source.write_bytes(rpm(b'original', b'canonical payload'))
            for payload, accepted in ((b'canonical payload', True), (b'mutated payload', False)):
                stored = rpm(b'registry signature', payload)
                record = {'checksum_sha256': release.hashlib.sha256(stored).hexdigest()}
                def download(args, **kwargs):
                    self.assertEqual(args[:2], ['cloudsmith', 'download'])
                    output = Path(args[args.index('--outfile') + 1])
                    self.assertEqual(output.name, source.name)
                    output.write_bytes(stored)
                with patch.object(cloudsmith.release, 'run', side_effect=download):
                    if accepted:
                        cloudsmith.verify_stored_package(record, source, 'jobman/stable', VERSION)
                    else:
                        with self.assertRaisesRegex(ValueError, 'outside its signature'):
                            cloudsmith.verify_stored_package(record, source, 'jobman/stable', VERSION)

    def image_index(self):
        return {'manifests': [
            {'platform': {'os': 'linux', 'architecture': arch},
             'digest': 'sha256:' + digit * 64,
             'mediaType': 'application/vnd.oci.image.manifest.v1+json'}
            for arch, digit in (('amd64', '1'), ('arm64', '2'))
        ] + [{'platform': {'os': 'unknown', 'architecture': 'unknown'},
              'digest': 'sha256:' + '3' * 64,
              'mediaType': 'application/vnd.oci.image.manifest.v1+json'}]}

    def test_platform_manifest_selection_rejects_ambiguous_or_invalid_children(self):
        index = self.image_index()
        self.assertEqual(release.platform_manifests(index), {'amd64': 'sha256:' + '1' * 64, 'arm64': 'sha256:' + '2' * 64})
        invalid = [
            {}, {'manifests': []},
            {'manifests': index['manifests'] + [index['manifests'][0]]},
            {'manifests': index['manifests'][1:]},
        ]
        for change in ({'digest': 'sha256:bad'}, {'digest': None},
                       {'digest': 'sha256:' + '2' * 64},
                       {'platform': {'os': 'linux', 'architecture': '386'}},
                       {'platform': {'os': 'unknown', 'architecture': 'amd64'}},
                       {'mediaType': 'application/vnd.oci.image.index.v1+json'}):
            invalid.append({'manifests': [index['manifests'][0] | change, *index['manifests'][1:]]})
        for candidate in invalid:
            with self.subTest(index=candidate), self.assertRaises(ValueError):
                release.platform_manifests(candidate)

    def test_container_verification_uses_child_digests_with_classic_store(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            context, output = root / 'context', root / 'output'
            output.mkdir()
            for arch in release.ARCHES:
                for folder in ('bin', 'web'):
                    directory = context / arch / folder
                    directory.mkdir(parents=True)
                    (directory / 'payload').write_bytes((arch + '/' + folder).encode())
            image, index_digest = 'ghcr.io/owner/dashboard', 'sha256:' + 'f' * 64
            children = {arch: image + '@' + digest for arch, digest in release.platform_manifests(self.image_index()).items()}
            commands, pulled = [], {}
            def inspect(args):
                commands.append(args)
                if args[:4] == ['docker', 'buildx', 'imagetools', 'inspect']:
                    self.assertEqual(args[4], image + '@' + index_digest)
                    return json.dumps(self.image_index())
                if args[:2] == ['docker', 'create']:
                    arch = args[args.index('--platform') + 1].split('/')[1]
                    self.assertEqual(args[-1], children[arch])
                    return arch + '-container'
                if args[:2] == ['docker', 'inspect']:
                    return json.dumps([{'Config': {'User': '10001:10001', 'Labels': {'org.opencontainers.image.revision': REVISION, 'org.opencontainers.image.version': VERSION}}}])
                if args[:2] == ['docker', 'run']:
                    arch = args[args.index('--platform') + 1].split('/')[1]
                    self.assertEqual(args[-2], children[arch])
                    return json.dumps({'version': VERSION, 'revision': REVISION, 'architecture': arch, 'os': 'linux'})
                self.fail('Unexpected capture command: ' + repr(args))
            def execute(args, **kwargs):
                commands.append(args)
                if args[:2] == ['docker', 'pull']:
                    arch = args[args.index('--platform') + 1].split('/')[1]
                    # Model the classic store's refusal to overwrite a digest
                    # already materialized for another architecture.
                    if args[-1] in pulled and pulled[args[-1]] != arch:
                        raise RuntimeError('cannot overwrite digest')
                    pulled[args[-1]] = arch
                    self.assertEqual(args[-1], children[arch])
                elif args[:2] == ['docker', 'cp']:
                    arch = args[2].split('-container:')[0]
                    folder = 'web' if args[2].endswith('/web') else 'bin'
                    shutil.copytree(context / arch / folder, args[3])
                elif args[:2] != ['docker', 'rm']:
                    self.fail('Unexpected run command: ' + repr(args))
            def unconfigured(args, **kwargs):
                commands.append(args)
                arch = args[args.index('--platform') + 1].split('/')[1]
                self.assertEqual(args[-1], children[arch])
                return subprocess.CompletedProcess(args, 1, '', 'configuration required')
            with patch.object(release, 'capture', side_effect=inspect), patch.object(release, 'run', side_effect=execute), patch.object(release.subprocess, 'run', side_effect=unconfigured):
                release.verify_container(self.args(input=context, output=output, image=image, image_digest=index_digest, runtime_architecture='all'))
            self.assertEqual(len(pulled), 2)
            self.assertEqual(len([args for args in commands if args[:2] == ['docker', 'run']]), 6)
            receipt = json.loads((output / 'container.json').read_text())
            self.assertEqual(receipt['digest'], index_digest)
            self.assertEqual(receipt['platforms'], ['linux/amd64', 'linux/arm64'])
            self.assertTrue(receipt['canonicalPayloadVerified'])

    def test_container_payload_comparison_rejects_changed_extra_or_linked_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            expected, actual = Path(temp) / 'expected', Path(temp) / 'actual'
            expected.mkdir(); actual.mkdir()
            (expected / 'app').write_bytes(b'canonical')
            shutil.copyfile(expected / 'app', actual / 'app')
            release.same_payload(expected, actual)
            (actual / 'app').write_bytes(b'rebuilt')
            with self.assertRaisesRegex(ValueError, 'changed canonical'): release.same_payload(expected, actual)
            shutil.copyfile(expected / 'app', actual / 'app')
            (actual / 'extra').write_bytes(b'extra')
            with self.assertRaisesRegex(ValueError, 'inventory'): release.same_payload(expected, actual)
            (actual / 'extra').unlink(); (actual / 'app').unlink()
            (actual / 'app').symlink_to(expected / 'app')
            with self.assertRaisesRegex(ValueError, 'nonregular'): release.same_payload(expected, actual)

    def assembly_fixture(self, root):
        linux, packages, native = (root / name for name in ('linux', 'packages', 'native'))
        for directory in (linux, packages, native): directory.mkdir()
        records = []
        for arch in release.ARCHES:
            archive = f'jobman-dashboard_{VERSION}_linux_{arch}.tar.gz'
            (linux / archive).write_bytes(b'canonical archive ' + arch.encode())
            for suffix in ('deb', 'rpm', 'apk'):
                name = f'jobman-dashboard_{VERSION}_linux_{arch}.{suffix}'
                (packages / name).write_bytes(b'canonical package ' + name.encode())
                records.append({'file': name, 'version': VERSION, 'revision': REVISION, 'architecture': arch, 'archive': archive, 'archiveSHA256': release.digest(linux / archive), 'sha256': release.digest(packages / name)})
        (packages / 'package-manifest.json').write_text(json.dumps({'packages': records}))
        for directory in (linux, packages): release.checksums(self.args(input=directory))
        (native / 'JobmanDashboard-unsigned.xcarchive.tar.gz').write_bytes(b'unsigned native fixture')
        (native / 'archive-files.json').write_bytes(b'[]')
        receipt = {'completed': True, 'mode': 'unsigned', 'installable': False, 'revision': REVISION, 'version': '0.1.0', 'build': '8', 'archiveTarSHA256': release.digest(native / 'JobmanDashboard-unsigned.xcarchive.tar.gz'), 'archiveInventorySHA256': release.digest(native / 'archive-files.json'), 'sourceArchiveSHA256': 'b' * 64, 'source': 'clean-committed-git-archive', 'toolchains': {}, 'bundle_id': 'org.jobman.dashboard', 'executableSHA256': 'c' * 64, 'infoSHA256': 'd' * 64, 'command': ['private runner path'], 'archive': '/private/runner/path'}
        (native / 'build-receipt.json').write_text(json.dumps(receipt))
        return self.args(input=linux, packages=packages, native=native, output=root / 'assets'), receipt

    def test_assembly_preserves_payload_and_sanitizes_native_provenance(self):
        with tempfile.TemporaryDirectory() as temp:
            args, receipt = self.assembly_fixture(Path(temp))
            release.assemble(args)
            self.assertEqual((args.native / 'JobmanDashboard-unsigned.xcarchive.tar.gz').read_bytes(), (args.output / 'JobmanDashboard-unsigned.xcarchive.tar.gz').read_bytes())
            metadata = json.loads((args.output / 'native-provenance.json').read_text())
            self.assertNotIn('command', metadata); self.assertNotIn('archive', metadata)
            self.assertEqual(metadata['revision'], REVISION)

    def test_assembly_rejects_native_receipt_from_another_source_or_signed_archive(self):
        for change in ({'revision': 'f' * 40}, {'mode': 'development'}, {'installable': True}, {'version': '0.2.0'}, {'archiveTarSHA256': '0' * 64}):
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temp:
                args, receipt = self.assembly_fixture(Path(temp))
                (args.native / 'build-receipt.json').write_text(json.dumps(receipt | change))
                with self.assertRaises(ValueError): release.assemble(args)


if __name__ == '__main__':
    unittest.main()
