# syntax=docker/dockerfile:1
# brokersim, the fake broker: a static Go binary on distroless, non-root.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/brokersim ./cmd/brokersim

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/brokersim /brokersim
USER nonroot:nonroot
EXPOSE 8090
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=6 CMD ["/brokersim", "healthcheck"]
ENTRYPOINT ["/brokersim"]
