# Production image: compile in a full Go image, ship only the static binary on
# distroless (no shell, no package manager, runs as non-root) — about 6 MB to download.
# Base images are pinned by digest; Dependabot proposes updates as PRs.

# ---- build ------------------------------------------------------------------
# --platform=$BUILDPLATFORM compiles natively on the build machine and
# cross-compiles for each target (GOOS/GOARCH): Go needs no emulator to build
# linux/arm64 on an amd64 runner.
FROM --platform=$BUILDPLATFORM golang:1.27.1-trixie@sha256:433790e515d27dc6003e847e644cc0af956985cf315c1c58a3b73ee2dd305183 AS build

WORKDIR /src

# Dependencies first: this layer stays cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/server ./cmd/server

# ---- runtime ----------------------------------------------------------------
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

COPY --from=build /out/server /server

# 65532 is distroless's "nonroot" user; numeric so runAsNonRoot checks can verify it.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/server"]
CMD ["api"]
