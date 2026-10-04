# Native binding and offline sign-out protocol

A notification installation uses a Keychain installation UUID and a 32-byte
installation proof. Each binding generation additionally requires a detach-only
credential before it can deliver push. The client generates a random credential
UUID (`revocationId`) and 32 random bytes (`revocationCredential`, canonical
unpadded base64url, 43 characters). The server stores only a purpose-separated
hash bound to the credential ID, installation and immutable private target
binding. Public device responses never expose binding IDs or secrets.

## Two acknowledged phases

1. Persist the credential pair in Keychain before making any request.
2. Authenticated `POST /api/v1/devices/{installationId}/revocation-reservations`
   sends exactly `{installationSecret, revocationId, revocationCredential}`.
   Use `If-None-Match: *` only for an unseen installation; otherwise use its
   positive current `If-Match` revision. Creation produces an **unbound**
   installation. The safe receipt contains `installationId`, decimal-string
   `revision`, `revocationId`, and `intent` (`bind`, `switch`, or `existing`).
3. Persist the acknowledged receipt, then recheck the initiating account,
   session and scene generation. Never start phase two after local sign-out or
   before the reservation response has been received and saved.
4. For explicit user attachment/switch, call the existing `bind`/`switch` route
   with positive `If-Match`, the same pair, installation proof and registration
   fields. The reserved private target becomes the binding atomically with
   credential activation. Normal startup uses token refresh and cannot rebind
   remote removal. For an already owned binding, call
   `POST /api/v1/devices/{installationId}/revocation-credentials` with the same
   proof/pair and positive revision to activate without changing preferences,
   token or ownership.

First reservation is revision 1; each later new reservation, binding or
activation increments it. Exact pair replays for the same current target return
the current receipt without mutation/audit, even after a lost response. An
active pair may be replayed through the reservation route to verify it still
belongs to the current generation. Binding submissions themselves remain
revision-checked: after an uncertain response, inspect/replay the reservation
instead of blindly submitting another binding operation. A revoked or
noncurrent pair conflicts and must never be treated as authority for a new
binding. Existing-binding credential recovery temporarily pauses delivery while
any existing-intent reservation is pending for that target.

## Offline sign-out

Immediately delete user bearer/session credentials. Queue only the origin,
revocation ID and narrow secret, then POST exactly
`{revocationId, revocationCredential}` to
`/auth/native/device-revocations` when connectivity is available. Never send
Authorization, Cookie, Origin, conditional headers, installation proofs, device
tokens or owner IDs. A known pending reservation is cancelled under the same
lock as binding admission; a known current target is detached and its encrypted
token erased. An existing-intent pending reservation also detaches its exact
current target. A stale credential never detaches a newer binding or account.

Syntactically valid unknown, wrong, revoked and successful requests all return
empty 204. Unknown requests create no records. Retain queued capabilities on
429/503 or transport uncertainty. The endpoint bounds JSON to 512 bytes, runs
at most eight concurrent requests per handler, uses a five-second request
deadline, and admits 60 requests/second with burst 120 per replica. These bounds
use constant memory; reverse proxies may apply a stricter deployment-wide limit.

An unknown revocation may race a late first reservation. That reservation alone
cannot bind or deliver: phase two requires the native client to acknowledge it
while the original session remains current. Existing-intent late reservations
pause delivery. A client with no acknowledged capability must honestly show an
unconfirmed server state; it cannot promise remote detachment when its request
never reached the server. During ordinary same-binding credential replacement,
keep the previous acknowledged capability until the new one is acknowledged;
do not enqueue the old capability while retaining that same binding.

## Persistence and upgrade behavior

Migration 13 is additive. Existing bindings without an activated credential are
push-ineligible until their reservation/activation flow completes. `Device`
contains `revocationReady` so clients can explain this state. Worker candidate
selection, evaluation enqueue and final provider preparation share the same
eligibility predicate. Enabling or refreshing a token cannot bypass it.

All mutations preserve global-device-advisory-lock then installation-lock order.
Credential/binding/token changes and audit entries commit atomically. Closing a
binding clears every associated credential hash and retains opaque ID
tombstones, preventing later reuse. No unauthenticated negative-ID store exists.
Authenticated admission allows at most 500 retained credential IDs per
installation, five pending reservations, and five IDs per target binding.
New reservations/activations use the existing 30-mutations-per-minute bound.
Revocation, remote removal and genuine stopping changes remain available when
admission is exhausted. There is no invented inactivity expiration or automatic
tombstone pruning.
