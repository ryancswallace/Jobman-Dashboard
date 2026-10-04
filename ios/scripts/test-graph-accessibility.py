#!/usr/bin/env python3
"""Opt-in synthetic simulator display test; restores exact per-device settings."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--device', required=True, help='Existing booted iPhone simulator UUID')
    args = parser.parse_args()
    if not re.fullmatch(r'[0-9A-Fa-f-]{36}', args.device):
        parser.error('Expected simulator UUID')
    evidence = Path(tempfile.mkdtemp(prefix='jobman-dashboard-graph-accessibility-'))
    def command(*parts, timeout=30):
        return subprocess.run(parts, cwd=ROOT, check=True, capture_output=True, text=True, timeout=timeout).stdout.strip()
    def ui(*parts):
        return command('xcrun', 'simctl', 'ui', args.device, *parts)
    def motion(*parts):
        return command('xcrun', 'simctl', 'spawn', args.device, 'defaults', *parts)
    devices = json.loads(command('xcrun', 'simctl', 'list', 'devices', 'booted', '--json'))
    matches = [d for group in devices['devices'].values() for d in group if d['udid'] == args.device]
    if len(matches) != 1 or not matches[0]['name'].startswith('iPhone'):
        raise RuntimeError('Expected one existing booted iPhone simulator')
    before = {'device': args.device, 'contentSize': ui('content_size'),
              'reduceMotion': motion('read', 'com.apple.Accessibility', 'ReduceMotionEnabled')}
    sizes = {'extra-small', 'small', 'medium', 'large', 'extra-large', 'extra-extra-large', 'extra-extra-extra-large',
             'accessibility-medium', 'accessibility-large', 'accessibility-extra-large', 'accessibility-extra-extra-large', 'accessibility-extra-extra-extra-large'}
    if before['contentSize'] not in sizes or before['reduceMotion'] not in {'0', '1'}:
        raise RuntimeError('Unknown baseline; refusing to change simulator preferences')
    (evidence / 'before.json').write_text(json.dumps(before, indent=2) + '\n')
    status = None
    try:
        ui('content_size', 'accessibility-extra-extra-extra-large')
        motion('write', 'com.apple.Accessibility', 'ReduceMotionEnabled', '-bool', 'true')
        env = dict(os.environ, TEST_RUNNER_JOBMAN_GRAPH_ACCESSIBILITY='1')
        with (evidence / 'test.log').open('w') as output:
            status = subprocess.run(['xcodebuild', '-project', 'JobmanDashboard.xcodeproj', '-scheme', 'JobmanDashboard',
                '-destination', 'platform=iOS Simulator,id=' + args.device, '-derivedDataPath', str(evidence / 'build'),
                '-resultBundlePath', str(evidence / 'display.xcresult'), '-collect-test-diagnostics', 'never',
                '-only-testing:DashboardUITests/GraphCeilingUITests/testLargeGraphAccessibleTextAndMotionSettings',
                'CODE_SIGNING_ALLOWED=NO', 'test'], cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT, timeout=600).returncode
    finally:
        # Independent restoration attempts: failure restoring one must not skip the other.
        failures = []
        for restore in [lambda: ui('content_size', before['contentSize']),
                        lambda: motion('write', 'com.apple.Accessibility', 'ReduceMotionEnabled', '-bool', 'true' if before['reduceMotion'] == '1' else 'false')]:
            try:
                restore()
            except Exception as error:
                failures.append(type(error).__name__)
        after = {'device': args.device, 'contentSize': ui('content_size'),
                 'reduceMotion': motion('read', 'com.apple.Accessibility', 'ReduceMotionEnabled')}
        (evidence / 'after.json').write_text(json.dumps(after, indent=2) + '\n')
        (evidence / 'result.json').write_text(json.dumps({'testExit': status, 'restored': before == after and not failures, 'restoreErrors': failures}, indent=2) + '\n')
        print(evidence)
        if failures or before != after:
            raise RuntimeError('Simulator settings restoration was not verified; inspect retained evidence')
    if status != 0:
        raise SystemExit(status or 1)
    # Guard against a skipped opt-in test being mistaken for display acceptance.
    log = (evidence / 'test.log').read_text()
    if "testLargeGraphAccessibleTextAndMotionSettings]' passed" not in log or ' skipped ' in log:
        raise RuntimeError('Expected actual passing opt-in display test, not a skip')


if __name__ == '__main__':
    main()
