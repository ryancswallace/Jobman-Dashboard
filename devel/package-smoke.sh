#!/bin/sh
set -eu
# Inspect package file lists; never install or start a service on the developer host.
root=${1:-dist}
for file in "$root"/*.deb; do
  dpkg-deb --contents "$file" | grep -F 'opt/jobman-dashboard/snapshot/bin/jobman-dashboard' >/dev/null
  dpkg-deb --contents "$file" | grep -F 'opt/jobman-dashboard/snapshot/web/index.html' >/dev/null
  if dpkg-deb --contents "$file" | grep -E '/(etc|usr/lib/systemd)/' >/dev/null; then
    echo 'snapshot must not install active configuration or service units' >&2
    exit 1
  fi
done
for file in "$root"/*.rpm; do
  rpm -qlp "$file" | grep -F '/opt/jobman-dashboard/snapshot/bin/jobman-log-broker' >/dev/null
done
for file in "$root"/*.apk; do
  tar -tzf "$file" | grep -F 'opt/jobman-dashboard/snapshot/web/index.html' >/dev/null
done
echo 'snapshot package layouts verified; no services installed'
