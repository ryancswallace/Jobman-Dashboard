#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
xcodebuild -project JobmanDashboard.xcodeproj -scheme JobmanDashboard -configuration Debug \
  -destination 'generic/platform=iOS Simulator' -derivedDataPath "${TMPDIR:-/tmp}/jobman-dashboard-xcode-build" CODE_SIGNING_ALLOWED=NO build
