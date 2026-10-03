#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
ios_destination="${IOS_TEST_DESTINATION:-platform=iOS Simulator,name=iPhone 18 Pro}"
xcodebuild -project JobmanDashboard.xcodeproj -scheme JobmanDashboard \
  -destination "$ios_destination" -derivedDataPath "${TMPDIR:-/tmp}/jobman-dashboard-xcode-ui-tests" \
  -collect-test-diagnostics never CODE_SIGNING_ALLOWED=NO test
