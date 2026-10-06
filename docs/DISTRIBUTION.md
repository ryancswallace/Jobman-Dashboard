# Candidate distribution

Dashboard publishes engineering release candidates. A published candidate is not
production acceptance: real AD FS/direct AD, APNs, signed managed-device delivery,
manual accessibility and pilot/operations gates remain open. The unsigned iPhone
archive is retained for provenance; it cannot be installed on a phone.

## Artifact contract

A release selects one clean main commit and a new `vX.Y.Z-rc.N` version. Never
replace a published tag, asset or container version. The release workflow requires
successful current main-commit CI, repository checks, CodeQL, fuzz and Scorecard
runs, then separately approved publication through the `main` environment.

- Linux amd64 and arm64 archives are built twice from committed source. Their
  checksums must agree. Both services and the web assets share that source.
- DEB, RPM and APK packages are made from those verified archives with pinned
  nFPM. They are built twice; package and manifest checksums must agree.
- Package lifecycle tests use disposable Ubuntu, Fedora and Alpine containers.
  They verify installation, reinstallation and removal. The separate local
  upgrade profile additionally stages two candidate versions and checks explicit
  activation/rollback and configuration/data preservation.
- A non-root multi-platform image is assembled from the same Linux binaries
  and web files. It performs no second compilation. The workflow compares the
  extracted payload on both platforms with the canonical archives.
- SPDX inventories cover every distributable archive/package and both container
  platforms. GitHub build attestations cover release files, the checksum manifest
  and the immutable container digest.
- An unsigned iPhone archive has an explicit marketing version/build and the same
  source revision. Its inventory and archive hashes are verified before inclusion.

Publication first stages a draft, then downloads and checks every asset and its
attestation. Only verified drafts become prereleases. No candidate becomes a
stable release or updates a `latest` container tag. If publication partially
fails, retain its version and evidence; fix the cause and choose a new RC.
Package-registry upload is a separate repeatable operation for a published RC.

## Publish a candidate

After merging reviewed changes and waiting for main CI, choose the next unused
candidate and a greater native build number. For example:

```sh
gh workflow run release.yml --repo ryancswallace/Jobman-Dashboard --ref main \
  -f version=v0.1.0-rc.10 -f nativeBuild=12
```

Review and approve the concrete `main` deployment after builds complete. The
workflow uses GitHub's scoped token and keyless attestation identity; no personal
access token or private signing key belongs in the source tree. Registry or
organization policy may require an administrator to permit GHCR publication.
Verify anonymous pulls before advertising a public container; publishing must
not silently change repository access policy.

The versioned image is
`ghcr.io/ryancswallace/jobman-dashboard:v0.1.0-rc.10`. Deployment should pin the
`sha256:` digest in the release's attested `container.json`. It has no shell or
package manager, uses UID/GID `10001:10001`, and contains the web tree at
`/usr/share/jobman-dashboard/web`. Supply explicit private configuration, trust
anchors and correctly owned data mounts per service role; keep the root filesystem
read-only, drop capabilities and retain private networking. Override the entrypoint
with `/usr/local/bin/jobman-log-broker` for the broker role. The API/worker use
`/usr/local/bin/jobman-dashboard` with the documented process mode. Do not mount
all service credentials into a common container. See [process modes](PROCESS_MODES.md)
and [Linux installation](LINUX_INSTALLATION.md).

## Inspect a retained draft

The publisher locates drafts through GitHub's authenticated release list and
checks the exact release ID. A read-only check can repeat the full downloaded
asset, attestation and image verification without publishing or changing it:

```sh
python3 scripts/publish-release.py verify-draft \
  --repository ryancswallace/Jobman-Dashboard --version v0.1.0-rc.9 \
  --revision d47ea417a4ae77a0bb0e6872bbbbcb6cd3ae5627 \
  --input /absolute/path/to/downloaded-draft-assets
```

This example inspects the retained RC9 draft. Success reports `published: false`;
it does not turn a failed publication into an accepted release. The next run
still selects a new candidate rather than replacing retained tags or artifacts.

## Verify downloads

Download the candidate into a new empty directory. Resolve its tag to the full
source commit and verify the checksum manifest's attestation against the approved
release workflow and that commit before trusting its contents:

```sh
gh release download v0.1.0-rc.10 --repo ryancswallace/Jobman-Dashboard --dir ./rc10
cd rc10
gh attestation verify SHA256SUMS --repo ryancswallace/Jobman-Dashboard \
  --signer-workflow ryancswallace/Jobman-Dashboard/.github/workflows/release.yml \
  --source-ref refs/heads/main --source-digest <full-approved-commit> \
  --deny-self-hosted-runners
sha256sum -c SHA256SUMS
```

Verify the chosen package/archive attestation with the same options. Check
`package-manifest.json` for package-to-archive identity, and `build.json` inside
the archive for the compiled dependency graph and source revision. On macOS,
`shasum -a 256 -c SHA256SUMS` performs the checksum verification. Checksums establish
integrity; the attestation binds those checksums to the publisher and source.

## Install, upgrade and remove native Linux packages

Packages are version-named, for example `jobman-dashboard-0-1-0-rc-10`. Install the
verified architecture-specific DEB/RPM/APK with the host's package manager. An APK
from GitHub is unsigned; `apk add --allow-untrusted <verified-file.apk>` is only
appropriate after the publisher attestation and checksum checks above. Prefer a
configured trusted repository when using normal package-manager distribution.

Each package owns only `/opt/jobman-dashboard/releases/<version>`. It contains
both executables, web assets, examples, systemd templates, grant tooling and
runbooks. It does not own `/opt/jobman-dashboard/current`, `/etc` configuration,
accounts, active service units, databases, reports or log-broker state. There are
no install/uninstall scripts that start services, run migrations or remove data.

Installing a new version stages it alongside the old package; it does not activate
an upgrade. Follow the backup, schema compatibility, grants and service validation
procedure in [Linux installation](LINUX_INSTALLATION.md). After those checks,
an operator can prepare a new `current` symlink and atomically rename it into place,
then restart the selected services. Retain the previous package for rollback.
A binary rollback is permitted only when its schema compatibility check accepts
the current database ledger; otherwise use the coordinated restore procedure.

Never remove the package behind `current` or a running process. Switch and restart
first, verify service identity, then remove an obsolete version by its exact
package name. Removal preserves private configuration/data because the package
does not own them. Package managers do not automatically activate the next RC;
installation and activation are deliberately separate operator actions.

## Cloudsmith setup

The optional `publish-cloudsmith-packages.yml` workflow uses a **personal
Cloudsmith API key**, matching the other Jobman repositories. A service account,
OIDC integration or paid plan is not required by this workflow. The key acts with
the permissions of its Cloudsmith user; it is not restricted to Dashboard merely
because it is stored in the Dashboard repository.

1. Sign into Cloudsmith with the personal account used to publish Jobman packages.
   Ensure that user has at least **Write** access to the `jobman/stable`
   repository shared with the other Jobman ecosystem publishers. No separate
   Dashboard repository is required.
2. Open the user icon at the top right, then **Personal API Keys**, or open
   [Personal API Keys](https://app.cloudsmith.com/settings/api-keys) directly.
   Reuse the current key from your password manager if available. Cloudsmith's
   **Refresh** action creates a new key and permanently disables the old one;
   do not refresh simply to add Dashboard if other publishers already use it.
   If rotation is necessary, save the new key and update every GitHub secret and
   other integration using the old key, including Jobman, Control and Diagnose.
   See the current [Cloudsmith API-key instructions](https://docs.cloudsmith.com/accounts-and-teams/api-key).
3. Open [Dashboard environment settings](https://github.com/ryancswallace/Jobman-Dashboard/settings/environments),
   select **main**, and under **Environment secrets** add or update
   `CLOUDSMITH_API_KEY` with the personal key. Never put the key in a configuration
   variable, repository file, command argument, issue, log or chat. GitHub does
   not reveal an existing secret's value for copying from a sibling repository.
4. The default destination is `jobman/stable`. If another approved
   repository is needed, add the **environment variable** `CLOUDSMITH_REPOSITORY`
   in the same `main` environment with the value `owner/repository`. This variable
   contains only the destination, not a credential.
5. Open **Actions → Publish Dashboard RC Linux packages → Run workflow**, choose
   branch **main**, and enter the published RC tag, currently `v0.1.0-rc.10`.
   Complete the existing `main` environment approval if prompted. Alternatively,
   use the command below. Successful completion verifies authentication, uploads
   missing packages, and verifies all six DEB/RPM/APK packages for both architectures.

The pinned Cloudsmith action receives `secrets.CLOUDSMITH_API_KEY` through its
`api-key` input; the publisher receives the same secret through its environment.
Neither step needs a service-account name, username, password or entitlement token.
An entitlement token is for package downloads and cannot replace this publishing
API key. Keep existing environment protection and release verification enabled.

```sh
gh workflow run publish-cloudsmith-packages.yml \
  --repo ryancswallace/Jobman-Dashboard --ref main -f version=v0.1.0-rc.10
```

This workflow accepts only published RCs, verifies the GitHub publisher and asset
hashes, refuses conflicting registry entries, and checks registry synchronization.
Cloudsmith may add its repository signature to RPMs; the publisher compares the
downloaded RPM header and payload with the source, allowing only the RPM signature
section to differ. Other formats must retain the uploaded checksum. The shared repository's name does not promote a Dashboard RC to a stable
release: package names and versions retain their RC identity, registry tags
include `rc`, and the GitHub release remains a prerelease. Use the repository's
Cloudsmith-generated APT/YUM/APK setup instructions and signing key for repository
installation. GitHub release downloads remain available if the optional package
registry is not configured.
