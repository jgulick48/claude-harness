# Web UI + `harness-core serve` image (docs/WEB.md section 7).
#   docker build -t claude-harness .
#   docker run -p 8080:8080 -v harness-data:/data -e HARNESS_ADMIN_PASSWORD=... claude-harness
# /data must be writable by uid 65532: use a named volume (initialised from the
# image) or `chown 65532:65532` a bind-mounted directory.

# --- web UI (platform independent, so it always builds natively) ---
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
# Only the renderer is built here; Electron itself isn't needed.
ENV ELECTRON_SKIP_BINARY_DOWNLOAD=1
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build:web

# --- core, with the UI embedded ---
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.1.0-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/ui/out/web/ internal/webserve/dist/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X github.com/jgulick48/claude-harness/internal/version.Version=${VERSION}" \
    -o /out/harness-core ./cmd/harness-core
# Pre-create /data owned by the runtime user so named volumes inherit it.
RUN mkdir -p /out/data && chown 65532:65532 /out/data

# --- runtime ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/harness-core /harness-core
COPY --from=builder --chown=65532:65532 /out/data /data
USER 65532:65532
ENV HOME=/data HARNESS_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/harness-core", "serve"]
