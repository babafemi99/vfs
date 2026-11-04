# syntax=docker/dockerfile:1
# The trade app API: a static Go binary on distroless, running as non-root.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8091
# Distroless has no shell or curl: the binary checks its own /healthz.
HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=6 CMD ["/api", "healthcheck"]
ENTRYPOINT ["/api"]
