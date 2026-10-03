#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
ios_root="$PWD"
ios_developer_dir="$(xcode-select -p)"
ios_scratch="${TMPDIR:-/tmp}/jobman-dashboard-swift-build"
ios_cache="${TMPDIR:-/tmp}/jobman-dashboard-swift-cache"
mkdir -p "$ios_root/.build/clang-cache" "$ios_root/.build/configuration" "$ios_root/.build/security"
export CLANG_MODULE_CACHE_PATH="$ios_root/.build/clang-cache"
export SWIFTPM_MODULECACHE_OVERRIDE="$ios_root/.build/clang-cache"
if [ "$ios_developer_dir" = "/Library/Developer/CommandLineTools" ]; then
  # Apple's CLT contains Testing, but SwiftPM does not add these framework/runtime paths.
  swift test --disable-sandbox --scratch-path "$ios_scratch" --cache-path "$ios_cache" \
    --config-path "$ios_root/.build/configuration" --security-path "$ios_root/.build/security" \
    -Xswiftc -F -Xswiftc "$ios_developer_dir/Library/Developer/Frameworks" \
    -Xlinker -rpath -Xlinker "$ios_developer_dir/Library/Developer/Frameworks" \
    -Xlinker -rpath -Xlinker "$ios_developer_dir/Library/Developer/usr/lib"
else
  swift test --scratch-path "$ios_scratch" --cache-path "$ios_cache"
fi
