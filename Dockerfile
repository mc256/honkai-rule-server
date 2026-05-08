# syntax=docker/dockerfile:1
# Multi-stage build per plan.md §Target Platform: static binary in a scratch image.

FROM golang:1.26-alpine AS build
WORKDIR /src

# Allow the Go toolchain to fetch a newer minor version on demand if go.mod
# bumps past this base image (e.g., after a future `go get` of a dep that
# requires a newer Go). Without this, alpine's stock GOTOOLCHAIN=local
# refuses to compile and the docker build fails on a future routine bump.
ENV GOTOOLCHAIN=auto

# Cache dependency downloads in a separate layer
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/server \
    ./cmd/server

# Final image: scratch + CA bundle (we do HTTPS to upstream subscription providers)
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/server /server

# Bake the served-config template at a known path (009 FR-004) so the
# container needs only operator-supplied config (subs, own-proxies, tokens)
# at runtime. The chart's SERVED_CONFIG_TEMPLATE_PATH defaults to this path.
COPY --from=build /src/templates/served-config.template.yaml /etc/honkai/served-config.template.yaml

EXPOSE 8080
ENTRYPOINT ["/server"]
