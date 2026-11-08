# Deployment configuration and native variants

Keep organization settings, documentation, images and build profiles in a private
configuration repository. Render one explicit JSON file per process role outside
the public source checkout. The service accepts no runtime inheritance or template
expressions: resolve overlays before validation, retain a provenance manifest and
deploy the reviewed result. Shared public examples remain synthetic.

## Validation and schemas

Use absolute paths for configuration and referenced files. These commands validate
structure and semantics offline without opening credential files, contacting an
identity provider, database or Control, starting listeners, or migrating state:

```sh
jobman-dashboard --mode validate-config --check-mode api --config /private/config/api.json
jobman-dashboard --mode validate-config --check-mode worker --config /private/config/worker.json
jobman-dashboard --mode validate-config --check-mode operator --config /private/config/operator.json
jobman-log-broker --mode validate-config --config /private/config/broker.json
```

`serve` validates the combined role. `api` additionally applies API-only role
restrictions. The existing `check-config` mode remains the local credential and
material preflight and must run in the target environment before service
activation. It does not establish live dependency or authorization acceptance.

Export the current decoder's structural JSON Schema, without configuration input:

```sh
jobman-dashboard --mode config-schema --check-mode api > api.schema.json
jobman-dashboard --mode config-schema --check-mode worker > worker.schema.json
jobman-dashboard --mode config-schema --check-mode operator > operator.schema.json
jobman-log-broker --mode config-schema > broker.schema.json
```

These Draft 2020-12 schemas reject unknown fields and describe types. They do not
replace native validation of required values, URL/path rules, bounds, duplicate
JSON keys, or cross-field security constraints. `configurationRevision` remains
the existing identity/recovery fence, separate from a document format version.
Increase it according to the existing rollout rules, never for cosmetic reasons
alone. Secret references remain filenames; inline secrets are not supported.

## Public presentation

The optional `presentation` object belongs to `serve`/`api` configuration only.
If supplied, include its required fields as below. Other fields default to empty
text or arrays. Omitting the object preserves the generic product presentation.

```json
{
  "schemaVersion": 1,
  "productName": "Jobman Dashboard",
  "organizationName": "",
  "authLabel": "Sign in with your organization",
  "headerText": "",
  "footerText": "Private workspace",
  "logoPath": "/logo.svg",
  "logoDarkPath": "/logo-transparent-dashboard.svg",
  "helpLinks": [],
  "notices": [],
  "contentLinkScheme": "jobman-dashboard"
}
```

`GET /presentation` is intentionally unauthenticated and returns only these
allowlisted fields. Never put internal hostnames, credentials, account details,
private procedures or sensitive notices here. Web and native clients display
plain text; HTML, scripts and runtime CSS are not configuration inputs.

Names and authentication labels are limited to 120 UTF-8 bytes, organization name
to 200, header/footer to 1,000. Notices contain a unique `id` (64 bytes), `text`
(4,000 bytes), and `severity` (`info` or `warning`). Help links contain `label`
(120 bytes) and a credential-free HTTPS `url` (2,048 bytes). At most eight notices
and eight links are allowed. Text is trimmed and excludes control characters.
These notices communicate information; they are not recorded consent or access
control. Authorization continues to use the trusted identity and Control policy.

Logo paths are canonical local absolute URL paths ending in `.svg`, `.png` or
`.webp`, limited to 256 bytes. Publish reviewed assets under those paths in the
external web staging tree; remote image URLs and path traversal are rejected.
Native logos and icons are compiled assets, supplied through the build profile.
The native client accepts server presentation only when its content-link scheme
matches the scheme already registered in the signed app.

Optional `theme` contains both `light` and `dark` objects, each with `accent` and
`foreground` in six-digit hexadecimal form (for example, `#00675f`). Each pair must provide at least 4.5:1 contrast;
the accent must also meet 4.5:1 against the fixed reading surfaces for its mode.
Validation runs on the server and both clients. Web links, accents, button fills,
button text and keyboard focus use the validated colors; native action/link tint
uses the appropriate accent. Status colors and reading backgrounds remain fixed.
No CSS fragments, URLs, fonts or executable styles are accepted.

For example, a generic teal theme is:

```json
{
  "light": { "accent": "#00675f", "foreground": "#ffffff" },
  "dark": { "accent": "#78d9cb", "foreground": "#14141d" }
}
```

Omitting `theme` preserves the current web purple and native teal defaults. The
existing web accent pairs are light `#6242d2`/`#ffffff` and dark
`#b39aff`/`#14141d`; existing default button styling is preserved when omitted.

## New-account defaults and bounded operations

`userDefaults` is optional on API/combined configuration. If supplied, specify all
three fields: `timezone` (IANA name, default `UTC`), `appearance` (`system`, `light`
or `dark`, default `system`) and `refreshSeconds` (`0`, `5`, `10` or `30`, default
`5`). Defaults initialize newly created accounts only. Existing preferences and
user edits survive subsequent logins and deployment changes; this is not policy.

Optional `operations` values use zero/omission for the documented default:

| Field | Default | Supported range | Roles |
| --- | --- | --- | --- |
| `databaseMaxConnections` | 16 | 1–128 | API, combined, worker |
| `databaseMinConnections` | 1 | 1–configured maximum | API, combined, worker |
| `databaseConnectTimeoutSeconds` | 5 | 1–30 | API, combined, worker |
| `sessionLifetimeSeconds` | 28800 | 300–28800 | API, combined |
| `monitoringTimeoutMilliseconds` | 1500 | 100–8000 | API, combined |

The OIDC token expiration can shorten a configured browser session. Existing
idle expiry, active-session revocation, authorization deadlines, protocol limits
and replay safeguards remain mandatory. Pool settings apply per process; size
total database capacity across all processes before increasing them.

Optional `retention` requires both `reportDays` (1–3650, default 30) and `inboxDays`
(1–30, default 30). It applies on API/combined/worker processes that create the
corresponding records. Configure consistent values across roles. Changes set the
expiry of future records only; existing expiry and audit/replay history are
unchanged. Inbox retention cannot exceed the replay journal's supported window.
Retention purges are performed by the existing retention worker.

Apply the updated [PostgreSQL grants](../deploy/postgres/grants.json) through the
existing reviewed provisioning workflow when upgrading: account preference and
expiry inserts now explicitly name the configured columns. No schema migration
is required for these settings. See [process modes](PROCESS_MODES.md),
[configuration](CONFIGURATION.md), and [operations](OPERATIONS.md) for all existing
identity, TLS, service, logging, notification and report configuration.

## Native build profiles

Use an external versioned JSON profile for a separately registered native app:

```json
{
  "schemaVersion": 1,
  "displayName": "Example Jobs",
  "bundleIdentifier": "org.example.jobs",
  "authScheme": "org.example.jobs.auth",
  "contentScheme": "org.example.jobs.content",
  "authLabel": "Sign in with Example"
}
```

Optional `logoLightFile`, `logoDarkFile` and `appIconFile` reference regular PNG
files, absolute or relative to the profile. Each is at most 8 MiB and 8192 pixels
per dimension; icons must be 1024×1024. Review image content and platform icon
requirements before release. No signing identities, private keys or provisioning
profiles belong in this JSON. Profiles are bounded to 16 KiB and reject unknown
fields, duplicate keys, build-variable expressions and malformed identities.

```sh
python3 ios/scripts/stage-profile.py --profile /private/config/native.json --validate-only
python3 ios/scripts/stage-profile.py --profile /private/config/native.json --output-directory /private/build/native
```

The output must be a new canonical absolute directory outside all repository
checkouts, with an existing parent directory. Staging copies the required public
sources, materializes the verified generated-contract and fixture aliases,
substitutes reviewed assets and public build identity, and generates an isolated
Xcode project. It does not sign, build, archive or publish the app. Local signing
files, caches and environment files are excluded. The public source project and
its default identities remain unchanged. The output can then follow the existing
[native release](../ios/README.md) and real-device acceptance workflow.

Coordinate the profile with `oidc.nativeClientID`, the registered exact
`oidc.nativeRedirectURI` (`authScheme://callback`), the presentation content
scheme, APNs topic, Apple App ID, entitlements and distribution registration.
Authentication and content schemes must differ; custom schemes must be lowercase
and domain-qualified. Signing and real identity-provider/APNs/device acceptance
require the target organization's registered values and infrastructure.
