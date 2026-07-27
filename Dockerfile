# Build vehicle for the release artifacts. There is no runtime image: the tool
# drives docker-compose on the host, so it is shipped as a host binary. Docker is
# used here only to get a hermetic, reproducible build.
#
# The builder always runs on the native build platform and cross compiles, which
# is free for a CGO-less Go binary and avoids emulation.
#
#   docker buildx build --target binary --output type=local,dest=dist \
#     --build-arg GOOS=linux --build-arg GOARCH=arm64 .

ARG GO_VERSION=1.24

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build

# Provided by buildx, used as the default target when GOOS/GOARCH are not set.
ARG TARGETOS
ARG TARGETARCH

# Explicit overrides, needed to produce a darwin binary from a linux builder.
ARG GOOS=
ARG GOARCH=

ARG VERSION=dev
ARG BUILD_TIME=unknown
ARG GIT_COMMIT=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 \
    GOOS="${GOOS:-$TARGETOS}" \
    GOARCH="${GOARCH:-$TARGETARCH}" \
    go build \
      -trimpath \
      -ldflags "-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME} -X main.GitCommit=${GIT_COMMIT}" \
      -o /out/cosmos-docker-upgrader \
      ./cmd/cosmos-docker-upgrader

# Exported with --output type=local, so the binary lands directly in the
# destination directory.
FROM scratch AS binary
COPY --from=build /out/cosmos-docker-upgrader /
