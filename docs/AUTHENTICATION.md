# Authentication and private configuration

Status: implemented authentication/monitoring slice under test; no production rollout or release acceptance. Real AD FS and directory integration are still required. The Lab identity provider and local signed-token fixtures establish application behavior only.

## Configuration and commands

Copy `deploy/config.example.json` to an operator-owned absolute path and substitute approved private endpoints and identifiers. Configuration is strict bounded JSON. Unknown, repeated or wrongly cased properties fail validation; credentials are separate files. Public certificates/CA bundles may be mode `0644`; private keys, database URLs, the web client secret and encryption key must deny group/other access. Files must be regular and cannot be symlinks. Do not commit real configuration or secrets.

The HTTPS server presents the configured certificate directly. The certificate must identify `publicOrigin`. Browser/native requests use that exact HTTPS origin. There is no trusted forwarded-header mode or implicit proxy trust. Configure a separately approved TLS architecture before adding a reverse proxy that changes these assumptions.

```sh
jobman-dashboard --config /etc/jobman-dashboard/config.json --mode check-config
jobman-dashboard --config /etc/jobman-dashboard/config.json --mode migrate \
  --migration-database-url-file /run/secrets/dashboard-migration-database-url
jobman-dashboard --config /etc/jobman-dashboard/config.json --mode serve
```

`check-config` validates syntax and local keys/certificates without contacting AD FS, Control or PostgreSQL. `migrate` requires an explicit separate DDL identity; serving never runs migrations. The runtime validates the exact migration ledger before starting. Database URLs name a dedicated Dashboard database and require `sslmode=verify-full`; use the appropriate `sslrootcert` for private trust. Dashboard never queries a Control database in service operation. Lab test scripts use isolated schemas in the synthetic test server independently of production runtime configuration.

Encryption keys are exactly 32 raw bytes. Delegation signing keys are PKCS8 Ed25519 PEM. TLS keys use a matching certificate/key PEM pair. Text credential files contain one line; one terminal newline is accepted. Errors omit credential contents.

## AD FS registration contract

The installation needs three distinct identifiers: confidential web client, public native client, and Dashboard API audience. Register the exact web callback `https://<dashboard-host>/auth/callback` and native callback `jobman-dashboard-auth://callback`. Configure S256 PKCE, signed RS256 tokens, the approved immutable directory UUID claim, and the access-token client identity claim. `directoryIdClaim` and `clientIdClaim` are operator-selected claim names, not claims guessed from display names or email. Verify actual token shape with the identity owner before production acceptance.

Discovery, token exchange and signing-key requests use explicit private CA trust, bounded responses/timeouts, one allowed issuer origin and no redirects. Metadata advertising PKCE methods must include S256. An installation whose metadata omits that field still has to pass an actual PKCE exchange; there is no downgrade.

Browser login uses random state, nonce, PKCE, a five-minute encrypted one-use database transaction and a secure browser-binding cookie. Web session cookies are `Secure`, `HttpOnly`, `SameSite=Lax`, host-only and rotated after sign-in. The database retains only their SHA-256 hashes. Session creation, explicit revocation and capacity eviction are atomically audited without credential material. At most 20 web sessions remain active per account, with a 10,000-session global limit. A bounded maintenance pass removes expired/revoked sessions and login attempts each minute, and retains content-free audit for 90 days. Sessions expire after 30 minutes idle and at the earliest of eight hours, ID-token expiry, and returned access-token expiry. Browser flows do not request offline access or keep refresh tokens; renewal starts another AD FS SSO flow. This is authentication policy, not scheduled namespace-grant removal.

Bootstrap supplies a session-bound CSRF value; all cookie-authenticated mutations require it and the exact configured Origin. Native calls instead present an API-audience access token from the registered native client. ID tokens and tokens minted for another client/resource are rejected. Native refresh credentials remain in device-only Keychain storage.

Verified `(issuer, subject)` aliases join the same Dashboard account only through the configured signed directory UUID. Conflicting alias mappings fail rather than reassigning accounts. Display names and email never confer identity. Control separately requires an operator-verified mapping to its existing principal, preserving job ownership.

## Independent Control trust

Each configured source pins a unique Dashboard deployment UUID, expected Control instance UUID, HTTPS origin, CA bundle, client certificate, Ed25519 signing key, destination audience and explicit namespace UUIDs. These cannot come from request URLs or job data. The adapter refuses redirects and ignores ambient HTTP proxy settings.

Every read uses a fresh certificate-bound `Jobman-Delegation` assertion with at most 60 seconds of validity. Only monitoring operation classes can be signed. No arbitrary proxy, execution mutation or administrative mutation exists. Control verifies both service scope and current represented-user authority. `/me` must return the verified directory UUID and canonical Control principal ID; the latter supports truthful “my jobs” filtering.

Production reads require advertised `directory-authorization` and `read-delegation` features, complete verified namespace freshness, and current role capabilities. Old Controls without those features remain unavailable. The adapter rechecks grants after reading; changed authorization, principal mapping or recovery epoch discards the result. An outage is never treated as a zero count or a fresh permission grant.

The database prevents reusing a deployment UUID for a replacement Control instance. Increase `configurationRevision` for registry changes; an older revision cannot overwrite the registered identity. Control recovery epochs invalidate active browse sessions. Endpoint moves retain the expected instance identity; replacements require a new deployment UUID.

## Validation and remaining operational work

Automated signed-token TLS tests cover browser PKCE/nonce/state, replay, secure cookies, CSRF, sign-out, API audience/client/directory validation and clock policy. Mutual-TLS adapter tests cover source substitution, namespace mismatch, absent/stale authorization, redirects, body/page bounds, alias identity, revocation during reads and large revision fidelity. Real Lab PostgreSQL tests cover alias conflicts, session idle expiry/revocation, one-use login state, schema integrity, source registration and preference concurrency.

Key rotation currently starts a new web-session security epoch: changing the authentication encryption key invalidates outstanding login attempts and session CSRF proofs. Plan a sign-in reset and keep the old configuration only within the approved rollback procedure. Per-Control trust rotation/disablement, directory reconciliation, authoritative deployment acceptance, remaining record-family retention and packaging remain tracked in `IMPLEMENTATION_STATUS.md` and the design; passing these local tests does not waive them.
