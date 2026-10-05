ARG GO_VERSION=1.26.6
ARG GO_FEATURE_VERSION=1.26

ARG NODE_VERSION=26.5.1
FROM node:${NODE_VERSION}-bookworm-slim@sha256:9e6f9357d371591e32ab6f2d8a26d63bdd0d17c29eee3f4f3e7e454d9634bf73 AS node
FROM golang:${GO_VERSION}-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36 AS toolchain
FROM mcr.microsoft.com/devcontainers/go:2-${GO_FEATURE_VERSION}-bookworm@sha256:090a56c6c9c4e80a1573c18c1225eb851b8c86c199fc929b07ea2a67f7b4850f

COPY --from=toolchain /usr/local/go /usr/local/go
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
ARG NPM_VERSION=11.17.0
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
    && npm install --global npm@${NPM_VERSION} --ignore-scripts --no-audit --fund=false

ARG GOPLS_VERSION=v0.23.0
ARG DELVE_VERSION=v1.27.0

RUN apt-get update \
    && export DEBIAN_FRONTEND=noninteractive \
    && apt-get install --yes --no-install-recommends \
        ca-certificates \
        curl \
        git \
        jq \
        less \
        make \
        openssh-client \
        postgresql-client \
        procps \
        python3 \
        ripgrep \
        rpm \
        shellcheck \
        zsh \
    && rm -rf /var/lib/apt/lists/*

USER vscode
RUN go install golang.org/x/tools/gopls@${GOPLS_VERSION} \
    && go install github.com/go-delve/delve/cmd/dlv@${DELVE_VERSION}
