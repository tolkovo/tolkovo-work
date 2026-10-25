# syntax=docker/dockerfile:1
#
# One image for every role: the server binary with the built panel inside.
#   docker build -t tolkovo:dev .
#   docker run tolkovo:dev serve --role=all     (api + realtime + worker)
#   docker run tolkovo:dev migrate up           (as the table owner)
# Operations guide: deploy/README.md.

# --- Panel: static files from apps/panel (Vite) ------------------------------
# Built on the build machine's platform: the output is the same for every
# target architecture.
FROM --platform=$BUILDPLATFORM node:22.20-alpine AS panel
WORKDIR /src
# The version of "packageManager" in package.json. Not corepack: the one in
# Node 22.12 failed to verify current npm registry signatures.
RUN npm install -g pnpm@10.14.0
# Manifests first, so dependencies are cached until the lockfile changes.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/panel/package.json apps/panel/
COPY apps/widget/package.json apps/widget/
COPY apps/landing/package.json apps/landing/
COPY packages/api-client/package.json packages/api-client/
COPY packages/config/package.json packages/config/
COPY packages/i18n/package.json packages/i18n/
COPY packages/ui/package.json packages/ui/
RUN --mount=type=cache,id=pnpm,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile --filter "@tolkovo/panel..."
COPY packages packages
COPY apps/panel apps/panel
RUN pnpm --filter @tolkovo/panel run build

# --- Server: a static Go binary, migrations embedded --------------------------
FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine AS server
WORKDIR /src/apps/server
COPY apps/server/go.mod apps/server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY apps/server ./
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/tolkovo ./cmd/tolkovo
# Directory for the fs storage driver, owned by the runtime user, so a fresh
# named volume mounted here is writable.
RUN mkdir -p /out/files

# --- Runtime: no shell, no package manager, not root --------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=server /out/tolkovo /usr/local/bin/tolkovo
COPY --from=panel /src/apps/panel/dist /usr/share/tolkovo/panel
COPY --from=server --chown=65532:65532 /out/files /var/lib/tolkovo/files
ENV TOLKOVO_ENV=production \
    TOLKOVO_HTTP_ADDR=:8080 \
    TOLKOVO_LOG_FORMAT=json \
    TOLKOVO_PANEL_DIR=/usr/share/tolkovo/panel \
    TOLKOVO_STORAGE_FS_ROOT=/var/lib/tolkovo/files
USER 65532:65532
EXPOSE 8080
VOLUME ["/var/lib/tolkovo/files"]
# Meaningful for roles that serve HTTP (all, api, realtime); compose turns it
# off for the worker and for migrate.
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=3 CMD ["tolkovo", "healthcheck"]
ENTRYPOINT ["tolkovo"]
CMD ["serve", "--role=all"]

LABEL org.opencontainers.image.title="Tolkovo" \
      org.opencontainers.image.description="Open-source customer support: Telegram, website chat, AI first line" \
      org.opencontainers.image.source="https://github.com/tolkovo/tolkovo" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later"
