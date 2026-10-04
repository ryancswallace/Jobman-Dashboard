SHELL := /bin/bash
.DEFAULT_GOAL := help

export GOTOOLCHAIN := go$(shell cat go.version)
# Repository gates and artifacts always consume the pinned public module graph.
export GOWORK := off
unexport GOROOT

.PHONY: help format format-check contracts contracts-check test test-db vet build web ios-core ios-simulator check dev

help:
	@echo 'Dashboard: make check, make web, make ios-core, make ios-simulator, make dev'

format:
	gofmt -w cmd internal
	cd web && npm run format

format-check:
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)
	cd web && npm run format:check

contracts:
	python3 scripts/generate-contracts.py

contracts-check:
	python3 scripts/generate-contracts.py --check
	python3 -m unittest discover -s contracts -p 'test_*.py'

test:
	go test -race -shuffle=on ./...
	cd web && npm test

test-db:
	@test -n "$$JOBMAN_DASHBOARD_TEST_DATABASE_URL" || (echo 'Set JOBMAN_DASHBOARD_TEST_DATABASE_URL to a disposable-schema test database.'; exit 1)
	go test -race -count=1 ./internal/store

vet:
	go vet ./...

build:
	mkdir -p bin
	go build -trimpath -o bin/jobman-dashboard ./cmd/jobman-dashboard
	go build -trimpath -o bin/jobman-log-broker ./cmd/jobman-log-broker

web:
	cd web && npm ci && npm run build

ios-core:
	./ios/scripts/test-core.sh

ios-simulator:
	./ios/scripts/build-simulator.sh

check: contracts-check format-check vet test build

dev:
	go run ./cmd/jobman-dashboard --fixture
