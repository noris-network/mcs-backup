#       https://hub.docker.com/_/golang
FROM    harbor.prod.paas.pop.noris.de/dockerhub/library/golang:1.20 AS builder
WORKDIR /go/src/app
ARG     VERSION
COPY    . ./
RUN     CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X 'main.build=$VERSION'" -o mcs-backup .

#       https://hub.docker.com/_/alpine
FROM    harbor.prod.paas.pop.noris.de/dockerhub/library/alpine:3.17

#       https://github.com/restic/restic/releases
ARG     RESTIC_VERSION=0.15.1
RUN     set -x \
        && apk --no-cache add curl \
        && curl -Lo /usr/bin/restic.bz2 https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_amd64.bz2 \
        && bunzip2 /usr/bin/restic.bz2 \
        && chmod 755 /usr/bin/restic \
        && chmod 777 /mnt \
        && apk del curl

COPY    --from=builder /go/src/app/mcs-backup /opt/mcs/bin/
RUN     chmod 755 /opt/mcs/bin/* &&\
        cd /opt/mcs/bin/ &&\
        ln -s mcsbackup backup

ENV     HOME=/tmp \
        TZ=Europe/Berlin \
        BACKUP_ROOT=/mnt \
        RETENTION_POLICY='{"daily":7,"last":10}' \
        PATH=/opt/mcs/bin:$PATH

WORKDIR /tmp

CMD     ["mcs-backup", "serve"]
