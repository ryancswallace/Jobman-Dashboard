# Compatibility

The current delivery is an engineering candidate. The exact verified Dashboard,
Core, Control and Diagnose tuple belongs in [FINAL_CANDIDATE.md](FINAL_CANDIDATE.md)
and the chronological [Lab catalog](LAB_RUN_CATALOG.md). Do not assume an older
stable upstream tag supports Dashboard merely because its version string is stable.

`go.mod`/`go.sum` pin compiled Core and Diagnose dependencies. Control is deployed
separately; its version, source identity and advertised contracts must match the
validated tuple. Canonical candidate metadata records first-party pins and leaves
final eligibility false until the external gates close.

Services ship for Linux amd64/arm64. The web client is browser-based; native delivery
is iPhone-first, with iPad deferred. Generated TypeScript and Swift contracts share
one OpenAPI source. Keep both clients compatible when changing fields or errors.
Database migrations are forward-only and existing committed migrations must not
be rewritten. Consult [upgrading](UPGRADING.md) before replacing a runtime.
