# Troubleshooting

| Symptom | First safe check |
| --- | --- |
| Toolchain mismatch | Compare `make versions` with the three root version files; clear inherited GOROOT and select pinned Go |
| Web build/type failures | Run `npm ci --prefix web`, then `make contracts-check` and `npm run typecheck --prefix web` |
| Database tests skipped | Supply the explicit disposable database URL described in [testing](TESTING.md) |
| Configured process refuses startup | Run role-matched configuration checks and inspect safe error classifications in [process modes](PROCESS_MODES.md) |
| Sign-in or revoked-access behavior | Verify issuer/audience/client/directory mappings using [authentication](AUTHENTICATION.md); never weaken validation |
| Missing/stale deployment results | Inspect source status and safe observations in [operator status](OPERATOR_STATUS.md) |
| Logs unavailable | Verify broker metadata, allowlists, service ACLs and generation mapping in [log broker](LOG_BROKER.md) |
| Notifications missing | Check authorization, registration, holds and delivery classifications in [pipeline](NOTIFICATION_PIPELINE.md) |
| Native build fails | Confirm full Xcode and follow [native setup](../ios/README.md); Linux cannot build the iPhone app |
| Candidate build refuses source | Use a clean checkout of the intended commit; preserve unrelated files rather than deleting or packaging them |

Use [SUPPORT.md](../SUPPORT.md) for safe issue information. Do not copy secret-bearing
configuration, raw logs, tokens or production database rows into tickets.
