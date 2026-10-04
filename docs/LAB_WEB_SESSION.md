# Synthetic Lab web-session protocol acceptance

`TestLabDeployedWebSession` exercises the deployed Dashboard BFF with real
Keycloak authorization code/S256 exchange, using the existing synthetic Alice
account. It is an opt-in HTTP protocol test. It does not render a browser, run a
browser driver, change browser policy, or establish corporate AD FS acceptance.

The test uses the existing private Lab credential file and public CA. Only its
own HTTP transport maps `dashboard.lab.test:8443` to `10.77.0.10:8443` and
`oidc.lab.test:8443` to `10.77.0.21:8443`. TLS verifies the original hostnames and
CA. Host DNS, trust stores, services and configuration remain unchanged.
Credentials, authorization codes, tokens, cookies and HTTP bodies stay in
memory and are omitted from test output and artifacts.

## Prerequisites and execution

Coordinate with the Lab operator and obtain independent review of the exact
test before a live run. The deployed API, Keycloak and authorized Control sources
must be healthy. Run without another Alice preferences editor or acceptance test
changing those preferences. This test creates normal short-lived sign-in/session
records, changes only Alice's appearance preference and restores its original
value. The two successful preference writes advance the ordinary revision counter;
the counter and audit/session records are not rewound.

Offline helper regressions and live-test compilation, with no Lab access:

```sh
env -u GOROOT -u JOBMAN_DASHBOARD_LAB_ROOT -u JOBMAN_DASHBOARD_LAB_RUNTIME \
  -u JOBMAN_DASHBOARD_LAB_WEB_SESSION GOTOOLCHAIN=go1.26.6 \
  go test -race -tags integration ./internal/auth \
  -run '^TestLab(WebSession|DeployedWebSession)' -count=1
```

After review and explicit Lab coordination, from the exact accepted checkout:

```sh
env -u GOROOT GOTOOLCHAIN=go1.26.6 \
  JOBMAN_DASHBOARD_LAB_ROOT=/Users/rcw/home/code/jobman-lab \
  JOBMAN_DASHBOARD_LAB_RUNTIME=1 JOBMAN_DASHBOARD_LAB_WEB_SESSION=1 \
  go test -race -tags integration ./internal/auth \
  -run '^TestLabDeployedWebSession$' -count=1 -timeout=5m -v
```

The test first uses the existing native PKCE/JWKS helper to verify the same
subject's signed issuer/client/immutable directory GUID. Its bearer-authenticated
deployed account is compared with the web-session bootstrap account; public
bootstrap need not expose the private subject or directory GUID. Real BFF callback
processing separately verifies its web ID token on the server.

## Assertions and bounds

- Anonymous API requests are denied. Login returns only the exact configured
  issuer authorization path with the web client, S256, nonce, state, callback and
  resource audience. The fixed Keycloak login form is submitted only to the pinned
  identity origin. All redirects are followed manually through a finite sequence.
- Login and session cookies are host-only, path `/`, `Secure`, `HttpOnly`, and
  `SameSite=Lax`; login lasts five minutes and the session expiry is bounded by
  the server's eight-hour ceiling. Callback clears the login cookie.
- Replaying the consumed callback with its original login cookie is denied and
  issues no session cookie. The original valid session remains usable afterward.
- Cookie and native bootstraps identify the same account, with complete current
  source discovery and no fixture mode. Cookie bootstrap supplies the CSRF token;
  bearer bootstrap does not. Responses are not cacheable.
- Missing CSRF, wrong CSRF and wrong Origin writes return authentication failures
  and leave settings unchanged. A legitimate cookie-authenticated appearance
  update uses `If-Match`, advances one revision, and persists. A second legitimate
  update restores original preference values, independently verified with bearer
  authentication.
- Logout clears the cookie. Both the cleared jar and the exact old cookie,
  deliberately replayed outside that jar, are rejected. The old cookie also
  cannot authorize another logout.

The native helper has a 45-second deadline; the main web flow has two minutes.
Cleanup has an independent 60-second deadline. Each HTTP request has a 15-second
timeout, response headers are bounded to 32 KiB, bodies to 1 MiB, form/request
bodies to 4 KiB, and redirect URLs to 16 KiB. No automatic retries occur.

Session cleanup is registered before the callback can admit a session or any
callback assertion can fail. It recovers the cookie from the in-memory jar and
CSRF token from authenticated bootstrap when needed, logs out, and independently
verifies revocation. Missing recoverable credentials are reported as uncertainty.
If a preference reply is lost, cleanup uses the
independently authenticated account to restore only the exact attempted successor
revision and values. It recognizes unchanged or already-restored settings. It
refuses to overwrite concurrent changes and reports a sanitized failure requiring
operator inspection. It also attempts normal web logout if the test exits early.

## Evidence boundaries

Record the test commit, deployed candidate/configuration revision, command and
sanitized pass/failure log after execution. The test must not be described as live
acceptance until it actually runs. Automated HTTP cookie-attribute checks do not
prove browser SameSite enforcement, rendered web navigation, accessibility or a
managed iPhone flow. Existing browser-policy restrictions remain in force. Actual
AD FS and corporate directory acceptance remain separate release gates.
