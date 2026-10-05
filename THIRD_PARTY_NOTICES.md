# Third-party notices

Jobman Dashboard uses the [MIT License](LICENSE), matching the Jobman repository
family. Its binaries and web assets include separately licensed dependencies:

- CoreOS go-oidc and go-jose: Apache License 2.0.
- pgx and its pgpassfile, pgservicefile and puddle dependencies: MIT License.
- Jobman and Jobman Diagnose: MIT License.
- Go standard library and golang.org/x modules: BSD 3-Clause License.
- React, React DOM, React Router and Scheduler: MIT License.
- Development/build tools have their own upstream licenses and are not all shipped
  in application assets. The native core currently has no external Swift packages.

Exact Go dependencies are recorded by `go.mod`, `go.sum` and binary build metadata;
web dependencies are locked in `web/package-lock.json`. `make sbom` generates SPDX
inventories from built Linux archives/packages. Consult upstream license and NOTICE
files for complete terms; this summary does not replace them. Container base images
contain additional operating-system components; inventory them separately when
promoting a configured image. Apple toolchain/distribution terms are separate from
this repository's source license.
