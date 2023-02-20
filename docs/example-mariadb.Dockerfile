####### mcs-backup image
#       see https://hub.docker.com/r/nxcc/mcs-backup/tags
FROM    nxcc/mcs-backup:v1.5.1 as mcs-backup

####### main image
#       see https://hub.docker.com/r/bitnami/mariadb/tags
FROM    bitnami/mariadb:10.10.3

####### configure defaults
ENV     HOME=/tmp \
        TZ=Europe/Berlin \
        BACKUP_ROOT=/mnt \
        RETENTION_POLICY='{"daily":7,"last":10}'

####### make mcs-backup the entrypoint
ENTRYPOINT ["mcs-backup", "serve"]

####### install backup and restic
COPY    --from=mcs-backup /usr/bin/mcs-backup /usr/bin/
COPY    --from=mcs-backup /usr/bin/restic     /usr/bin/
