# Installation

The canonical engineering candidate provides Linux amd64/arm64 services and
compiled web assets. Follow [Linux installation](LINUX_INSTALLATION.md) for checksum
verification, roles, migration, private configuration and service setup. Read
[release status](FINAL_CANDIDATE.md) before selecting an exact candidate.

The native app requires a separately signed build; follow [iPhone packaging](../ios/README.md).
Unsigned CI archives cannot be installed on phones. Internal distribution and
actual company-managed-device acceptance remain external release gates.

Local GoReleaser snapshots additionally produce archives and deb/rpm/apk packages
for repository validation. Packages install into `/opt/jobman-dashboard/snapshot`
and place examples under `/usr/share/doc/jobman-dashboard`. They do not create
users, install active units, switch `current`, migrate, or start services. They are
not a supported production upgrade channel. See [RELEASE.md](../RELEASE.md).

Container builds are described in [containers](CONTAINERS.md). Production hosting,
identity/network changes and company distribution require the approvals already
recorded in the [implementation prompt](IMPLEMENTATION_PROMPT.md).
