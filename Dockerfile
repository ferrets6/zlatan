# SPDX-License-Identifier: AGPL-3.0-or-later

FROM golang:1.26 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
# A static binary, so the runtime image needs no libc. The SQLite driver is
# modernc.org/sqlite precisely so this works with CGO off.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/migrate ./cmd/migrate

# The two tools Migrate drives. They are pinned to a version and verified
# against a checksum recorded here: an unpinned download in a build is how a
# build becomes unreproducible and, worse, silently different.
FROM alpine:3.21 AS tools
ARG RCLONE_VERSION=v1.75.1
ARG RCLONE_SHA256_AMD64=982b5aa772841168f8e380f139e9e787b2a105403e32b94da8676a0e1c0a13ab
ARG RCLONE_SHA256_ARM64=03f2504174034b6d004152ed7369251c9a9ec1f7e0836eda420f5c7a5ec0dff9
ARG IMMICH_GO_VERSION=v0.32.0
ARG IMMICH_GO_SHA256_AMD64=6e2ad86bafdadb9466d6515de7cb882726c0aea1a21d51164dff361d7d480a97
ARG IMMICH_GO_SHA256_ARM64=2c35d9284baae407ef9540bdac5f488971b0bdc7be758a4d7c05ab270af09fdb

RUN apk add --no-cache curl unzip
RUN mkdir -p /out
RUN set -eux; \
    arch="$(apk --print-arch)"; \
    case "$arch" in \
      x86_64)  rclone_arch=amd64; rclone_sha="$RCLONE_SHA256_AMD64"; ig_arch=x86_64; ig_sha="$IMMICH_GO_SHA256_AMD64" ;; \
      aarch64) rclone_arch=arm64; rclone_sha="$RCLONE_SHA256_ARM64"; ig_arch=arm64;  ig_sha="$IMMICH_GO_SHA256_ARM64" ;; \
      *) echo "unsupported architecture: $arch" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o /tmp/rclone.zip "https://downloads.rclone.org/${RCLONE_VERSION}/rclone-${RCLONE_VERSION}-linux-${rclone_arch}.zip"; \
    echo "${rclone_sha}  /tmp/rclone.zip" | sha256sum -c -; \
    unzip -q /tmp/rclone.zip -d /tmp/rclone; \
    install -m 0755 "/tmp/rclone/rclone-${RCLONE_VERSION}-linux-${rclone_arch}/rclone" /out/rclone; \
    curl -fsSL -o /tmp/immich-go.tar.gz "https://github.com/simulot/immich-go/releases/download/${IMMICH_GO_VERSION}/immich-go_Linux_${ig_arch}.tar.gz"; \
    echo "${ig_sha}  /tmp/immich-go.tar.gz" | sha256sum -c -; \
    mkdir -p /tmp/ig; \
    tar -xzf /tmp/immich-go.tar.gz -C /tmp/ig; \
    install -m 0755 /tmp/ig/immich-go /out/immich-go

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/migrate /usr/local/bin/migrate
COPY --from=tools /out/rclone /usr/local/bin/rclone
COPY --from=tools /out/immich-go /usr/local/bin/immich-go

# /data holds the SQLite file, the sealed tokens and pre-migration backups. It
# must be writable by uid 65532, which is what distroless nonroot runs as.
# /staging is the large, temporary, regenerable area the tools write to.
VOLUME ["/data", "/staging"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/migrate"]
CMD ["serve"]
