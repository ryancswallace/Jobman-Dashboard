# Separate process keys

The API's `encryption` master key protects interactive authentication state.
Workers must not receive that key. Dedicated token, report-policy and log-cursor
keys let each process receive only the cryptographic capability it uses. APNs
provider signing keys belong only to delivery workers.

For a new installation, provision independent private32-byte raw keys using the
organization's secret-management process. Configure device keys under
`notifications.tokenEncryption.current` and optional `previous` entries, each
with a `keyId` and absolute `keyFile`. These files are already purpose-separated
AES keys. The explicitly different `tokenEncryption` shape cannot be mixed with
legacy `previousTokenKeys`, whose files contain authentication master keys.

API and delivery processes need the same device key ring and IDs. Retain old
read keys until their tokens have been rotated or removed; missing/wrong keys
fail closed. Token ciphertext still authenticates installation, binding,
account, topic, environment, token version and key ID. Key separation does not
relax those checks or create authority to send a notification.

The API and report worker share `reports.policyKeyFile` for a stable, private
redaction-policy fingerprint. Each log-reading process supplies
`logCursorKeyFile`; a report worker may use its own independent key because its
bounded collection cursors do not cross the API process boundary. Keep keys
outside the static web tree, in regular0600 files readable by the intended
service identity. Role-specific runtime validation and installation instructions
are covered by [process modes](PROCESS_MODES.md).

## Migrating existing combined configurations

Changing a master key into an unrelated raw purpose key would make existing
device tokens unreadable and change policy fingerprints. Instead, export the
existing derivations offline, without changing IDs or the database:

```sh
mkdir -m 700 /private/operator-key-staging
jobman-dashboard keys export --config /etc/jobman-dashboard/config.json \
  --output-directory /private/operator-key-staging/dashboard-purpose-keys
```

The parent must already be private and owned by the operator running the command.
The destination must be new and resolve outside the existing web tree. The
command reads only the configured legacy master/read-key files. It does not
contact a service or modify the source configuration. It writes derived keys as
0600 files and writes/syncs a private `manifest.json` last. It never prints key
bytes or copies the master key. It refuses existing destinations and configurations
already using dedicated key fields. Retain an interrupted destination for private
inspection; use a new destination after resolving the failure.

Install the resulting purpose files into each process's private secret directory
with its intended ownership, then set the corresponding configuration paths from
the manifest. Preserve all exported device key IDs and include previous read
keys. Remove legacy `previousTokenKeys` when selecting `tokenEncryption`. Verify
configuration and representative existing token/policy behavior before removing
old secret access from workers. The API's own master remains in its API-only
secret directory; ordinary authentication-key rotation is a separate operation.

The combined `serve` configuration retains its legacy derivation when dedicated
fields are absent. An explicitly configured but unreadable purpose file never
falls back to the master. Tests verify bidirectional compatibility of old/new
device ciphertext, stable report fingerprints, distinct purposes, key sizes,
private permissions, complete previous-key export, no overwrite, and rejection
of static-tree aliases. This compatibility evidence is not a production key
migration or a claim that real APNs credentials have been supplied.
