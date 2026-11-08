# Compatibility

The selected first full release is v0.1.0. The exact verified Dashboard,
Core, Control and Diagnose tuple belongs in [FINAL_CANDIDATE.md](FINAL_CANDIDATE.md)
and the chronological [Lab catalog](LAB_RUN_CATALOG.md). Do not assume an older
stable upstream tag supports Dashboard merely because its version string is stable.

`go.mod`/`go.sum` pin compiled Core and Diagnose dependencies. Control is deployed
separately; its version, source identity and advertised contracts must match the
validated tuple. The source embeds Core v1.10.1 and Diagnose v0.7.0-rc.3;
Control v0.3.0 is selected separately. Diagnose's candidate identity is an
accepted dependency limitation for deterministic embedding. Full-release metadata
records scoped eligibility without asserting corporate identity, APNs, device or
deployed Control acceptance. See [first-release scope](FIRST_RELEASE_SCOPE.md).

Services ship for Linux amd64/arm64. The web client is browser-based; native delivery
is iPhone-first, with iPad deferred. Generated TypeScript and Swift contracts share
one OpenAPI source. Keep both clients compatible when changing fields or errors.
Database migrations are forward-only and existing committed migrations must not
be rewritten. Consult [upgrading](UPGRADING.md) before replacing a runtime.
