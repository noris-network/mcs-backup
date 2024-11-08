#       https://hub.docker.com/_/golang
FROM    golang:1.23.3 AS builder
WORKDIR /go/src/app
ARG     VERSION
COPY    . ./
RUN     CGO_ENABLED=0 go build -trimpath -tags timetzdata -ldflags="-s -w -X 'main.build=$VERSION'" -o mcs-backup ./cmd/mcs-backup

#       https://hub.docker.com/_/alpine
FROM    library/alpine:3.20.3

#       https://github.com/restic/restic/releases
ARG     RESTIC_VERSION=0.17.2
#       https://github.com/mikefarah/yq/releases/
ARG     YQ_VERSION=4.44.3
RUN     set -x \
        && apk --no-cache add curl \
        && curl -Lo /usr/bin/restic.bz2 \
                https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_amd64.bz2 \
        && bunzip2 /usr/bin/restic.bz2 \
        && chmod 755 /usr/bin/restic \
        && curl -Lo /usr/bin/yq \
                https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_amd64 \
        && chmod 755 /usr/bin/yq \
        && chmod 777 /mnt

COPY    --from=builder /go/src/app/mcs-backup /usr/bin/
COPY    scripts/cleanup-backup-root.sh /opt/bin/
RUN     chmod 755 /usr/bin/mcs-backup /opt/bin/cleanup-backup-root.sh

ENV     HOME=/tmp \
        TZ=GMT \
        BACKUP_ROOT=/mnt \
        RETENTION_POLICY='{"daily":7,"last":10}'

WORKDIR /tmp

CMD     ["mcs-backup", "serve"]
