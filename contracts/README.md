# Generated Dashboard transports

`api/openapi.json` is the source of truth. Generate and check transport-only DTOs and callable clients with Python's standard library:

```sh
python3 scripts/generate-contracts.py
python3 scripts/generate-contracts.py --check
python3 -m unittest discover -s contracts -p 'test_*.py'
```

Generated files are committed and include the exact OpenAPI SHA-256. `--check` never writes files and fails on drift. The manifest records the API document version, schema hash, model count, and operation IDs. There is no network-dependent generator or unpinned code-generation package.

The deliberately small OpenAPI 3.0.3 subset supports named object components, scalar/dictionary/array properties, required/optional/nullable fields, local schema references, scalar path/query/header parameters, JSON request bodies, and one successful JSON response (or 204) per operation. Unsupported composition, external references, untyped/mixed object maps, content encodings, missing path parameters, duplicate operation IDs, and duplicate parameters fail generation. Numeric bounds/patterns remain authored contract constraints; these generated static types do not replace server/domain validation.

Source enums remain strings so an unfamiliar source phase/outcome is inspectable. Dates and decimal-string revisions/byte offsets preserve their original wire strings. Swift required-but-nullable values use keyed `decode(Optional.self, forKey:)` so missing required fields are rejected, while explicit JSON null remains representable. Unknown additive response properties are ignored by Swift decoding.

## TypeScript

`typescript/dashboard.generated.ts` exports DTOs, parameter types, `DashboardClient`, and an injected generic `DashboardTransport`. The web application imports generated primary DTOs in `web/src/lib/api.ts`; `web/src/lib/transport.ts` provides the callable client's secure browser transport adapter. Application models remain separate.

## Swift

`swift/DashboardAPI.generated.swift` defines `DashboardAPI.*` Codable/Sendable models, typed query/header parameter structs, and a callable `DashboardClient`. Inject `DashboardHTTPTransport`; the transport must enforce approved origin/TLS, authentication, ephemeral/no-store behavior, cancellation, response-size bounds, and non-2xx errors before returning response data.

Example standalone compile and smoke checks (full Xcode integration remains owned by the native app):

```sh
swiftc -parse-as-library -emit-module -module-name DashboardTransport \
  -module-cache-path /private/tmp/jobman-dashboard-swift-module-cache \
  contracts/swift/DashboardAPI.generated.swift \
  -o /private/tmp/DashboardTransport.swiftmodule
swiftc -module-cache-path /private/tmp/jobman-dashboard-swift-module-cache \
  contracts/swift/DashboardAPI.generated.swift contracts/swift/ContractSmoke.swift \
  -o /private/tmp/jobman-contract-smoke
/private/tmp/jobman-contract-smoke
```

Clients never obtain credentials from this generator. Browser CSRF tokens, OAuth bearer tokens, cookie policy, retries and authorization freshness belong to the application transport/session layer.
