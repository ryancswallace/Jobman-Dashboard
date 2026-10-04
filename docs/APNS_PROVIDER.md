# APNs provider transport

The provider in `internal/push` supports operator-configured sandbox and production
APNs topics with a private PKCS#8 P-256 signing key. JWT authentication uses ES256
and caches the provider token for at most 45 minutes. Requests use verified TLS
and HTTP/2 to Apple's fixed environment-specific origins, without redirects or an
ambient HTTP proxy. Connections, concurrent requests, response bodies, headers,
request duration and retries are bounded.

The visible payload contains only the generic “A monitored job has an update”
message, an opaque inbox UUID and schema version. No job/namespace name, command,
path, log excerpt or reusable credential is included. A stable delivery UUID is
used for `apns-id`, and the inbox UUID supplies the collapse identifier. APNs
expiration is capped to five minutes and never exceeds the delivery handoff
window. A provider acceptance means APNs accepted the request, not that the phone
presented it.

Retryable transport failures retain an ambiguous-outcome flag. HTTP429 uses bounded
backoff, HTTP5xx respects Apple's documented minimum 15-minute retry delay, and
`IdleTimeout` retries after closing idle connections. Invalid provider credentials,
topics and payloads are classified separately for operator action. A410 response
must carry a valid millisecond timestamp; device invalidation must additionally
match the current account binding and token registration version, and cannot
invalidate a registration newer than that timestamp. Rotating an identical token
still advances its registration version. Provider text is reduced to a bounded
known reason rather than persisted as an arbitrary response or logged with a token.

Device token storage uses authenticated AES-256-GCM with random nonces, a
purpose-derived key and additional authenticated data covering installation,
binding, account, topic, environment, token version and key ID. Installation
possession uses a separate 32-byte Keychain secret whose server representation is
only a purpose-bound hash. Explicit binding/switching and ordinary token refresh
are separate operations; refresh cannot undo remote removal, disablement or mute.

Apple primary references checked while implementing the provider:

- [Token-based connection authentication](https://developer.apple.com/documentation/usernotifications/establishing-a-token-based-connection-to-apns)
- [Sending notification requests](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns)
- [Handling APNs responses](https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns)

Local tests use a TLS/HTTP2 server and verify signatures, headers, the complete
payload, environment separation, response classification, timestamp fencing and
retry bounds. Real provider credentials, phone delivery and company-managed-device
acceptance remain external gates. The [durable delivery worker](NOTIFICATION_PIPELINE.md)
is wired into the configured runtime; local transport and storage tests do not
establish end-to-end delivery to a real phone.

## Operator configuration

`notifications.deviceTopics` explicitly allows up to16 `(topic, environment)`
pairs. An empty list leaves device registration unavailable. Adding a topic does
not send a notification. `notifications.apns` separately supplies approved provider
credentials for allowed pairs; it requires `events.enabled=true`. Credentials are
read from bounded private files outside the static web root. `check-config` loads
and validates them locally without contacting Apple.

For example, merge the following object into private configuration, replacing the
illustrative topic/Apple IDs/key path with approved values:

```json
{
  "notifications": {
    "deviceTopics": [
      {"topic": "internal.example.JobmanDashboard", "environment": "sandbox"}
    ],
    "previousTokenKeys": [],
    "apns": [
      {
        "topic": "internal.example.JobmanDashboard",
        "environment": "sandbox",
        "teamId": "ABCDE12345",
        "keyId": "FGHIJ67890",
        "privateKeyFile": "/run/secrets/dashboard-apns-key.p8"
      }
    ]
  }
}
```

The active token encryption key uses the existing `encryption.keyId/keyFile` with
a separate cryptographic purpose. During rotation, `previousTokenKeys` may contain
up to7 prior `{ "keyId": "old-key-id", "keyFile": "/run/secrets/old-token-key" }`
entries. Only the current key encrypts new registrations; retained read keys allow
existing tokens to remain decryptable. Refreshing a registration reseals it under
the current key. Remove an old key only after verifying no live binding needs it;
an absent key fails closed. This device keyring does not change session-cookie
key rotation or silently preserve sessions whose authentication key was removed.

Installations allow30 ordinary refresh/settings changes per fixed60-second window.
Creation and account-binding changes have separate admission bounds. Actual stop,
mute, detach, removal and OS-permission loss remain available when those ordinary
mutation limits are reached. No-op settings do not add audit records or revisions.
Token refresh always advances its registration version, including identical bytes.
