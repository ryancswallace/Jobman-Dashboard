# Compatibility

See the [release record](RELEASE_HANDOFF.md) for published artifacts and the
[Lab catalog](LAB_RUN_CATALOG.md) for the exact runtime tuple used by each
integration test.

`go.mod`/`go.sum` pin compiled Core and Diagnose dependencies. Control is deployed
separately; its version, source identity and advertised contracts must match the
validated tuple. Dashboard v0.1.0 and v0.1.1 embed Core v1.10.1 and
Diagnose v0.7.0-rc.3 for deterministic reports. Upgrading a standalone Diagnose
binary leaves those compiled dependencies unchanged. See
[first-release scope](FIRST_RELEASE_SCOPE.md) for deployment acceptance.

Services ship for Linux amd64/arm64. The web client is browser-based; native delivery
is iPhone-first, with iPad deferred. Generated TypeScript and Swift contracts share
one OpenAPI source. Keep both clients compatible when changing fields or errors.
Database migrations are forward-only and existing committed migrations must not
be rewritten. Consult [upgrading](UPGRADING.md) before replacing a runtime.
