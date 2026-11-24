#       https://hub.docker.com/_/golang
FROM    --platform=$BUILDPLATFORM golang:1.27.4-trixie AS builder
WORKDIR /src
ARG     TARGETOS
ARG     TARGETARCH
ARG     VERSION
COPY    go.mod go.sum ./
RUN     go mod download
COPY    . ./
RUN     CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
        go build -trimpath -ldflags="-s -w -X 'main.build=$VERSION'" -o /out/mcs-backup ./cmd/mcs-backup

#       https://hub.docker.com/_/alpine
FROM    alpine:3.24.4

#       https://github.com/restic/restic/releases
ARG     RESTIC_VERSION=0.20.0
ARG     RESTIC_SHA256_AMD64=0ccd9fa7885074a24603b051e01bf9bd8b5e45d6a1791e9d9887e9e9372107f7
ARG     RESTIC_SHA256_ARM64=1188daded239bccb5952a01d2ad33e77a0d3dc34f9d9fc1e7fdda618d44938ef
ARG     TARGETARCH

#       curl stays for hooks and notification commands written for 1.x
RUN     set -eux \
        && apk --no-cache add curl \
        && case "$TARGETARCH" in \
               amd64) sum=$RESTIC_SHA256_AMD64 ;; \
               arm64) sum=$RESTIC_SHA256_ARM64 ;; \
               *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
           esac \
        && curl -fsSLo /tmp/restic.bz2 \
               "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_${TARGETARCH}.bz2" \
        && echo "$sum  /tmp/restic.bz2" | sha256sum -c - \
        && bunzip2 /tmp/restic.bz2 \
        && install -m 755 /tmp/restic /usr/bin/restic \
        && rm /tmp/restic \
        && chmod 777 /mnt

COPY    --from=builder /out/mcs-backup /usr/bin/mcs-backup
COPY    --chmod=755 scripts/cleanup-backup-root.sh /opt/bin/cleanup-backup-root.sh

#       The container runs with any UID and a read-only root file system: HOME,
#       the restic cache and the control socket all live in /tmp, so mount an
#       emptyDir (or a cache volume) there.
ENV     HOME=/tmp \
        TZ=GMT \
        BACKUP_ROOT=/mnt \
        RETENTION_POLICY='{"weekly":8,"daily":7,"last":10}'

WORKDIR /tmp

CMD     ["mcs-backup", "serve"]
