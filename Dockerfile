# syntax=docker/dockerfile:1
# Multi-stage, non-root, distroless runtime for air-infrastructure-service.
#
# The reason this service was ported from Python: the runtime image below has
# no interpreter, no package manager and no shell — a static binary, its policy
# document, and nothing else to patch or provision.

FROM golang:1.25-alpine AS build
WORKDIR /src

# Dependencies first, so a source-only change does not re-download the module
# cache on every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO off so the result is genuinely static and runs on a scratch-like base.
# -trimpath keeps build paths out of the binary; the version is a constant in
# the source, so there is nothing to stamp in here.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/air-infrastructure-service ./cmd/air-infrastructure-service

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app

COPY --from=build /out/air-infrastructure-service /app/air-infrastructure-service
# The policy document is read at boot from a path relative to the working
# directory. It is configuration, not code: mount over it to change policy
# without rebuilding.
COPY deploy ./deploy

# distroless/static:nonroot already runs as uid 65532; stated explicitly so a
# base image change cannot quietly promote this process to root.
USER 65532:65532
EXPOSE 8080

# Exec form, because there is no shell in this image. The binary probes its own
# /v1/health — liveness only: /v1/ready reports store health without gating on
# it, so probing it here would restart a working container whenever a store
# blinked.
HEALTHCHECK --interval=15s --timeout=3s --retries=5 \
  CMD ["/app/air-infrastructure-service", "-healthcheck"]

ENTRYPOINT ["/app/air-infrastructure-service"]
