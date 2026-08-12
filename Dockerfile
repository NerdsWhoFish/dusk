# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26
ARG NODE_VERSION=24

# The UI is built here rather than copied in, so `docker build` needs no node
# and cannot pick up whatever happened to be in web/dist locally. It is
# architecture independent, so it runs once on the build platform.
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web

WORKDIR /web

COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --fund=false

COPY web/ ./
RUN npm run build

# The build stage pins itself to the BUILD platform and cross-compiles with Go's
# own GOOS/GOARCH. Letting Docker run an arm64 toolchain under QEMU instead
# produces the same binary, slowly enough to dominate the whole release.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

# Dependencies change far less often than source, so they get their own layer.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

# After the source, so go:embed picks up the real bundle rather than the
# placeholder committed to keep an unbuilt checkout compiling.
COPY --from=web /web/dist ./web/dist

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown

# CGO_ENABLED=0 is ADR-0017, not an optimisation: a cgo dependency takes
# cross-compilation and the static base image with it.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
        -o /out/dusk ./cmd/dusk

# Static distroless: no shell and no package manager, so there is nothing in the
# image to exploit that we did not put there. It does carry CA certificates,
# which Dusk needs to reach GitHub.
FROM gcr.io/distroless/static-debian12:nonroot AS final

ARG VERSION
ARG COMMIT

LABEL org.opencontainers.image.title=dusk \
      org.opencontainers.image.description="A service catalog that maintains itself" \
      org.opencontainers.image.source=https://github.com/FetchHQ/dusk \
      org.opencontainers.image.licenses=Apache-2.0 \
      org.opencontainers.image.version=${VERSION} \
      org.opencontainers.image.revision=${COMMIT}

COPY --from=build /out/dusk /usr/local/bin/dusk

# Numeric on purpose: Kubernetes `runAsNonRoot` cannot verify a named user, and
# 65532 is distroless's nonroot.
USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/dusk"]
