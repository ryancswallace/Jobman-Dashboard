# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.26.6
ARG NODE_VERSION=26.5.1
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-bookworm-slim@sha256:9e6f9357d371591e32ab6f2d8a26d63bdd0d17c29eee3f4f3e7e454d9634bf73 AS web
ARG NPM_VERSION=11.17.0
WORKDIR /src/web
RUN npm install --global npm@${NPM_VERSION} --ignore-scripts --no-audit --fund=false
COPY web/package*.json ./
RUN npm ci --ignore-scripts --no-audit --fund=false
COPY contracts/typescript/ /src/contracts/typescript/
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36 AS build
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOWORK=off GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=development
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false \
    -ldflags="-X github.com/ryancswallace/jobman-dashboard/internal/buildinfo.Version=${VERSION} -X github.com/ryancswallace/jobman-dashboard/internal/buildinfo.Revision=${VCS_REF} -X github.com/ryancswallace/jobman-dashboard/internal/buildinfo.BuiltAt=${BUILD_DATE}" \
    -o /out/ ./cmd/jobman-dashboard ./cmd/jobman-log-broker

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6 AS runtime
RUN apk add --no-cache ca-certificates tini tzdata \
    && addgroup -S -g 10001 dashboard \
    && adduser -S -D -u 10001 -G dashboard -h /var/lib/jobman-dashboard dashboard \
    && mkdir -p /etc/jobman-dashboard /var/lib/jobman-dashboard /run/jobman-dashboard \
    && chown dashboard:dashboard /var/lib/jobman-dashboard /run/jobman-dashboard
COPY --from=build /out/ /usr/local/bin/
COPY --from=web /src/web/dist/ /usr/share/jobman-dashboard/web/
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/jobman-dashboard/
ARG VERSION=development
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="jobman-dashboard" \
    org.opencontainers.image.source="https://github.com/ryancswallace/Jobman-Dashboard" \
    org.opencontainers.image.version="$VERSION" \
    org.opencontainers.image.revision="$VCS_REF" \
    org.opencontainers.image.created="$BUILD_DATE" \
    org.opencontainers.image.licenses="MIT"
USER 10001:10001
WORKDIR /var/lib/jobman-dashboard
STOPSIGNAL SIGTERM
ENTRYPOINT ["/sbin/tini", "--", "jobman-dashboard"]
