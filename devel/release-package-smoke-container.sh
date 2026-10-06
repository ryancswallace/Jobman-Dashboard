#!/bin/sh
# Called only inside the disposable containers created by release-package-smoke.sh.
set -eu
test -f /.dockerenv
format=$1 architecture=$2 version=$3 previous_version=$4
name=jobman-dashboard-$(printf '%s' "${version#v}" | tr . -)
previous_name=jobman-dashboard-$(printf '%s' "${previous_version#v}" | tr . -)
release=/opt/jobman-dashboard/releases/$version
previous_release=/opt/jobman-dashboard/releases/$previous_version
install_package() {
    case "$format" in
        deb) dpkg --install "$1" ;;
        rpm) rpm --upgrade --replacepkgs "$1" ;;
        apk) apk add --no-network --allow-untrusted --cache-dir /apk-cache --cache-packages "$1" ;;
    esac
}
remove_package() {
    case "$format" in
        deb) dpkg --purge "$1" ;;
        rpm) rpm --erase "$1" ;;
        apk) apk del --no-network "$1" ;;
    esac
}
verify_release() {
    (cd "$1" && sha256sum -c SHA256SUMS)
    "$1/bin/jobman-dashboard" version | grep -F "$2"
    "$1/bin/jobman-log-broker" version | grep -F "$2"
    test -r "$1/web/index.html"
    test "$(stat -c '%u:%g:%a' "$1/bin/jobman-dashboard")" = 0:0:755
    test "$(stat -c '%u:%g:%a' "$1/web/index.html")" = 0:0:644
}
verify_retained() {
    sha256sum -c /retained.sha256
    test "$(readlink /opt/jobman-dashboard/current)" = "$1"
    # No active units or account creation is part of package installation.
    test ! -e /usr/lib/systemd/system/jobman-dashboard-api.service
    test ! -e /lib/systemd/system/jobman-dashboard-worker.service
    test ! -e /etc/systemd/system/jobman-log-broker.service
}
mkdir -p /etc/jobman-dashboard-api /etc/jobman-dashboard-worker /etc/jobman-log-broker \
    /var/lib/jobman-dashboard/reports /var/lib/jobman-log-broker /opt/jobman-dashboard/releases/operator-retained /apk-cache
printf 'synthetic private config\n' > /etc/jobman-dashboard-api/config.json
printf 'synthetic worker config\n' > /etc/jobman-dashboard-worker/config.json
printf 'synthetic broker config\n' > /etc/jobman-log-broker/config.json
printf 'synthetic retained report\n' > /var/lib/jobman-dashboard/reports/retained
printf 'synthetic ledger\n' > /var/lib/jobman-log-broker/retained
printf 'operator-owned previous release\n' > /opt/jobman-dashboard/releases/operator-retained/retained
sha256sum /etc/passwd /etc/group /etc/jobman-dashboard-api/config.json \
    /etc/jobman-dashboard-worker/config.json /etc/jobman-log-broker/config.json \
    /var/lib/jobman-dashboard/reports/retained /var/lib/jobman-log-broker/retained \
    /opt/jobman-dashboard/releases/operator-retained/retained > /retained.sha256
ln -s /opt/jobman-dashboard/releases/operator-retained /opt/jobman-dashboard/current
if [ "$previous_version" != "$version" ]; then
    install_package "/previous/jobman-dashboard_${previous_version}_linux_${architecture}.${format}"
    verify_release "$previous_release" "$previous_version"
    ln -s "$previous_release" /opt/jobman-dashboard/current.next
    mv -Tf /opt/jobman-dashboard/current.next /opt/jobman-dashboard/current
fi
active=$(readlink /opt/jobman-dashboard/current)
install_package "/packages/jobman-dashboard_${version}_linux_${architecture}.${format}"
verify_release "$release" "$version"
verify_retained "$active"
# Exercise the package manager's real replacement path, including APK fix.
if [ "$format" = apk ]; then
    apk fix --no-network --allow-untrusted --cache-dir /apk-cache --reinstall "$name"
else
    install_package "/packages/jobman-dashboard_${version}_linux_${architecture}.${format}"
fi
verify_release "$release" "$version"
verify_retained "$active"
if [ "$previous_version" != "$version" ]; then
    verify_release "$previous_release" "$previous_version"
fi
# Activation and rollback are explicit operator actions; packages never switch current.
ln -s "$release" /opt/jobman-dashboard/current.next
mv -Tf /opt/jobman-dashboard/current.next /opt/jobman-dashboard/current
verify_release /opt/jobman-dashboard/current "$version"
ln -s "$active" /opt/jobman-dashboard/current.next
mv -Tf /opt/jobman-dashboard/current.next /opt/jobman-dashboard/current
remove_package "$name"
test ! -e "$release/bin/jobman-dashboard"
verify_retained "$active"
if [ "$previous_version" != "$version" ]; then
    verify_release "$previous_release" "$previous_version"
    ln -s /opt/jobman-dashboard/releases/operator-retained /opt/jobman-dashboard/current.next
    mv -Tf /opt/jobman-dashboard/current.next /opt/jobman-dashboard/current
    remove_package "$previous_name"
fi
verify_retained /opt/jobman-dashboard/releases/operator-retained
echo "$format install, reinstall, explicit activation/rollback, removal and retention passed"
