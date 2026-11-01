# Hermetic end-to-end image: the real, statically linked ucmon binary plus a
# compiled e2e test binary, run as an unprivileged user with no network, a
# read-only root and a fixture /etc/fstab. Every base image is pinned by
# digest, so a rebuild months later tests the same environment. The only
# network access is at build time (go mod download, verified against go.sum).
#
# Build and run through e2e/run.sh (or `make e2e`): the volumes and sandbox
# flags the tests rely on are part of how the container is started.

FROM golang:1.25.14-bookworm@sha256:3b4a11519ad929d1e1d261a12cff056f0c85b735253d7d861346b9c6f8b36437 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY cmd ./cmd
COPY internal ./internal
COPY e2e ./e2e
# Same flags as the release build (Makefile): a static, cgo-free binary.
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
RUN go build -ldflags '-s -w' -o /out/ucmon ./cmd/ucmon \
 && go test -c -tags e2e -o /out/e2e.test ./e2e

# promtool validates /metrics with the parser Prometheus itself uses.
FROM prom/prometheus:v3.5.0@sha256:63805ebb8d2b3920190daf1cb14a60871b16fd38bed42b857a3182bc621f4996 AS prom

FROM debian:bookworm-slim@sha256:99c0b25c46d0214d5a2091092e7908907dcaecd6a962cde0ac700acee222b6f3
COPY --from=build /out/ucmon /usr/local/bin/ucmon
COPY --from=build /out/e2e.test /usr/local/bin/e2e.test
COPY --from=prom /bin/promtool /usr/local/bin/promtool
COPY e2e/testdata/fstab /etc/fstab
RUN useradd --uid 10001 --no-create-home --home-dir /tmp tester
USER tester
ENV HOME=/tmp TZ=UTC LANG=C.UTF-8 TERM=xterm-256color
WORKDIR /tmp
ENTRYPOINT ["/usr/local/bin/e2e.test", "-test.v", "-test.timeout=5m"]
