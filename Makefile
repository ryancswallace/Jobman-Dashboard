SHELL := /bin/bash
.DEFAULT_GOAL := help
.DELETE_ON_ERROR:

export GOTOOLCHAIN := go$(shell cat go.version)
# Repository gates and artifacts always consume the pinned public module graph.
export GOWORK := off
unexport GOROOT

.PHONY: help format format-check contracts contracts-check package-check candidate test test-db vet build web ios-core ios-simulator check dev

help:
	@awk 'BEGIN {FS = ":.*## "; print "Usage: make <target>"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-24s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

format: ## Format Go and web sources.
	gofmt -w cmd internal devel
	cd web && npm run format

format-check:
	@test -z "$$(gofmt -l cmd internal devel)" || (gofmt -l cmd internal devel; exit 1)
	cd web && npm run format:check

contracts: ## Regenerate TypeScript and Swift contracts.
	python3 scripts/generate-contracts.py

contracts-check: ## Verify contract generation and compatibility tests.
	python3 scripts/generate-contracts.py --check
	python3 -m unittest discover -s contracts -p 'test_*.py'

package-check: ## Verify candidate packaging boundaries.
	python3 -m unittest discover -s scripts -p 'test_*.py'
	python3 -m unittest discover -s devel -p 'test_*.py'
	python3 -m unittest discover -s deploy/postgres -p 'test_*.py'

candidate: ## Build canonical committed-source candidate bundles (VERSION and OUTPUT_DIR required).
	@test -n "$$VERSION" -a -n "$$OUTPUT_DIR" || (echo 'Set VERSION=vX.Y.Z-rc.N and OUTPUT_DIR to a new absolute directory.'; exit 1)
	python3 scripts/build-release.py --version "$$VERSION" --output-directory "$$OUTPUT_DIR"

test: ## Run Go race tests and web tests.
	go test -race -shuffle=on ./...
	cd web && npm test

test-db: ## Require and run PostgreSQL integration tests.
	@test -n "$$JOBMAN_DASHBOARD_TEST_DATABASE_URL" || (echo 'Set JOBMAN_DASHBOARD_TEST_DATABASE_URL to a disposable-schema test database.'; exit 1)
	go test -race -count=1 ./internal/store

vet:
	go vet ./...

build: ## Build both local service executables.
	mkdir -p bin
	go build -trimpath -o bin/jobman-dashboard ./cmd/jobman-dashboard
	go build -trimpath -o bin/jobman-log-broker ./cmd/jobman-log-broker

web: ## Install locked web dependencies and build static assets.
	cd web && npm ci && npm run build

ios-core: ## Test the native Swift package on macOS.
	./ios/scripts/test-core.sh

ios-simulator: ## Build the native simulator application on macOS.
	./ios/scripts/build-simulator.sh

quick-check: toolchain-check mod-check contracts-check package-check format-check vet test docs-check build ## Run the normal Go/web development gate.

check: quick-check repository-check web cross-build ## Run the complete portable gate; native checks are separate.

dev: ## Run the loopback-only synthetic fixture server.
	go run ./cmd/jobman-dashboard --fixture

# Tool versions intentionally match the sibling repository baseline.
BIN_DIR := bin
GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION := v1.6.0
ACTIONLINT_VERSION := v1.7.12
GORELEASER_VERSION := v2.17.0
NFPM_VERSION := v2.47.0
SYFT_VERSION := v1.46.0
CSPELL_VERSION := 10.0.1
GOLANGCI_LINT ?= bin/golangci-lint
GOVULNCHECK ?= bin/govulncheck
ACTIONLINT ?= bin/actionlint
GORELEASER ?= bin/goreleaser
NFPM ?= bin/nfpm
SYFT ?= bin/syft
COVERAGE_MIN ?= 30
FUZZ_TIME ?= 10s
FUZZ_PARALLEL ?= 2
IMAGE ?= jobman-dashboard:local
export PAGER := cat
export GIT_PAGER := cat
export GH_PAGER := cat

.PHONY: setup bootstrap tools versions toolchain-check download mod-check tidy lint workflow-check shellcheck vulncheck docs-check spellcheck docs coverage coverage-check fuzz repository-check quick-check all ci cross-build docker-check docker-image docker-smoke release-check release-build snapshot sbom artifact-check update update-all unittest integration-test

all ci: check
unittest: test
integration-test: test-db
setup: bootstrap ## Install pinned quality tools, Go modules and locked web dependencies.
bootstrap: toolchain-check tools download
	npm ci --prefix web

toolchain-check: ## Require the exact Go, Node and npm versions from version files.
	@test "$$(go env GOVERSION)" = "go$$(cat go.version)" || (echo 'Go version differs from go.version'; exit 1)
	@test "$$(node --version)" = "v$$(cat node.version)" || (echo 'Node version differs from node.version'; exit 1)
	@test "$$(npm --version)" = "$$(cat npm.version)" || (echo 'npm version differs from npm.version'; exit 1)

versions: ## Show pinned language and quality-tool versions.
	@cat go.version node.version npm.version
	@printf '%s\n' 'golangci-lint $(GOLANGCI_LINT_VERSION)' 'govulncheck $(GOVULNCHECK_VERSION)' 'actionlint $(ACTIONLINT_VERSION)' 'GoReleaser $(GORELEASER_VERSION)' 'Syft $(SYFT_VERSION)' 'cspell $(CSPELL_VERSION)'

download: ## Download and verify Go modules.
	go mod download
	go mod verify

mod-check: ## Verify immutable module downloads and tidy metadata.
	go mod verify
	go mod tidy -diff

tidy: ## Refresh module metadata intentionally.
	go mod tidy

lint: tool-golangci-lint ## Analyze the Linux Go runtime and web types.
	GOOS=linux CGO_ENABLED=0 $(GOLANGCI_LINT) run ./...
	npm run typecheck --prefix web

workflow-check: tool-actionlint ## Validate all GitHub Actions workflows.
	$(ACTIONLINT) .github/workflows/*.yml

shellcheck: ## Analyze developer and native shell scripts.
	shellcheck devel/*.sh ios/scripts/*.sh

vulncheck: tool-govulncheck ## Check reachable Go vulnerabilities and locked npm dependencies.
	$(GOVULNCHECK) ./...
	cd web && npm audit --audit-level=moderate

docs-check: ## Check repository-local documentation links.
	go run ./devel/docscheck -root .

spellcheck: ## Spell-check authored documentation with pinned cspell.
	npx --yes cspell@$(CSPELL_VERSION) lint --no-progress --dot

docs: docs-check spellcheck ## Validate documentation links and spelling.

coverage: ## Write an atomic Go coverage profile (database tests are opt-in).
	go test -race -shuffle=on -covermode=atomic -coverpkg=./... -coverprofile=coverage.txt ./...

coverage-check: coverage ## Enforce the shared 30 percent Go coverage floor.
	go tool cover -func=coverage.txt | awk -v minimum='$(COVERAGE_MIN)' -f devel/check-coverage.awk

fuzz: ## Exercise bounded browse-state decoding with two workers.
	go test ./internal/monitoring -run '^$$' -fuzz '^FuzzDecodeCursorIdentity$$' -fuzztime '$(FUZZ_TIME)' -parallel '$(FUZZ_PARALLEL)'

repository-check: lint workflow-check shellcheck docs release-check ## Validate repository automation, documentation and packaging.

cross-build: ## Compile both Linux release architectures.
	@for arch in amd64 arm64; do GOOS=linux GOARCH=$$arch CGO_ENABLED=0 go build -mod=readonly ./cmd/... || exit; done

docker-check: ## Validate runtime/devcontainer Dockerfiles and Compose configuration.
	docker build --check .
	docker build --check --file .devcontainer/Dockerfile .devcontainer
	docker compose config --quiet

docker-image: ## Build the unprivileged local image (does not publish).
	docker build --tag '$(IMAGE)' .

docker-smoke: docker-image ## Check both container binaries and fail-closed startup.
	./devel/container-smoke.sh '$(IMAGE)'

release-check: tool-goreleaser package-check ## Validate snapshot configuration and canonical candidate boundary tests.
	$(GORELEASER) check

release-build: tool-goreleaser ## Compile the GoReleaser Linux snapshot matrix.
	$(GORELEASER) build --snapshot --clean

snapshot: tool-goreleaser web ## Build local engineering archives and native Linux packages; never publish.
	$(GORELEASER) release --snapshot --clean --skip=publish,sign,sbom
	$(MAKE) artifact-check

sbom: tool-syft ## Generate SPDX inventories for every local snapshot archive/package.
	SYFT='$(abspath $(SYFT))' python3 devel/artifacts.py --sbom dist

artifact-check: ## Verify snapshot checksums, contents and platform coverage.
	python3 devel/artifacts.py dist

update: ## Regenerate contracts and the native project deterministically; no version upgrades.
	python3 scripts/generate-contracts.py
	python3 ios/scripts/generate-project.py

update-all: update format

.PHONY: tool-golangci-lint
tool-golangci-lint:
	@if ! $(GOLANGCI_LINT) version 2>/dev/null | grep -Fq '$(patsubst v%,%,$(GOLANGCI_LINT_VERSION))'; then \
		mkdir -p bin; GOBIN='$(abspath bin)' go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi

.PHONY: tool-govulncheck
tool-govulncheck:
	@if ! $(GOVULNCHECK) -version 2>/dev/null | grep -Fq '$(patsubst v%,%,$(GOVULNCHECK_VERSION))'; then \
		mkdir -p bin; GOBIN='$(abspath bin)' go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); \
	fi

.PHONY: tool-actionlint
tool-actionlint:
	@if ! $(ACTIONLINT) -version 2>/dev/null | grep -Fq '$(patsubst v%,%,$(ACTIONLINT_VERSION))'; then \
		mkdir -p bin; GOBIN='$(abspath bin)' go install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION); \
	fi

.PHONY: tool-goreleaser
tool-goreleaser:
	@if ! $(GORELEASER) --version 2>/dev/null | grep -Fq '$(patsubst v%,%,$(GORELEASER_VERSION))'; then \
		mkdir -p bin; GOBIN='$(abspath bin)' go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION); \
	fi

.PHONY: tool-syft
tool-syft:
	@if ! $(SYFT) version 2>/dev/null | grep -Fq '$(patsubst v%,%,$(SYFT_VERSION))'; then \
		mkdir -p bin; GOBIN='$(abspath bin)' go install github.com/anchore/syft/cmd/syft@$(SYFT_VERSION); \
	fi

tools: tool-golangci-lint tool-govulncheck tool-actionlint tool-goreleaser tool-syft

.PHONY: package-smoke
package-smoke: artifact-check ## Inspect native package layouts (requires dpkg-deb and rpm).
	./devel/package-smoke.sh dist

.PHONY: tool-nfpm release-packages release-package-smoke
tool-nfpm:
	@if ! go version -m '$(NFPM)' 2>/dev/null | grep -Fq '$(NFPM_VERSION)'; then \
		mkdir -p bin; GOBIN='$(abspath bin)' go install github.com/goreleaser/nfpm/v2/cmd/nfpm@$(NFPM_VERSION); \
	fi

release-packages: tool-nfpm ## Package verified canonical candidate archives (INPUT_DIR and OUTPUT_DIR required).
	@test -n "$$INPUT_DIR" -a -n "$$OUTPUT_DIR" || (echo 'Set INPUT_DIR and new OUTPUT_DIR.'; exit 1)
	python3 scripts/package-release.py --input-directory "$$INPUT_DIR" --output-directory "$$OUTPUT_DIR" --nfpm '$(abspath $(NFPM))'

release-package-smoke: ## Exercise candidate packages in disposable containers (PACKAGES_DIR, ARCH, optional PREVIOUS_PACKAGES_DIR).
	@test -n "$$PACKAGES_DIR" -a -n "$$ARCH" || (echo 'Set PACKAGES_DIR and ARCH=amd64 or arm64.'; exit 1)
	./devel/release-package-smoke.sh "$$PACKAGES_DIR" "$$ARCH" "$${PREVIOUS_PACKAGES_DIR:-$$PACKAGES_DIR}"
