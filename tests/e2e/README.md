# Integration and end-to-end tests

Portable database integration lives beside the implementation in `internal/store`
and selected `internal/httpapi` tests. Run `make integration-test` with an explicit
disposable database. Web interaction tests live in `web/src`; native UI tests live
in `ios/UITests`. Avoid duplicating those tests here for directory symmetry.

Live synthetic Lab tests are in `internal/auth/lab_*_test.go` and
`internal/logs/lab_nfs_test.go`. Consult the [Lab catalog](../../docs/LAB_RUN_CATALOG.md)
and specific runbooks before opting in; generic CI does not touch the Lab.
