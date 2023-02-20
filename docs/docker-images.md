[<< back to index](readme.md)

# Docker Images

The provided [mcs-backup image][image] can be used as is for backing up volumes.
However, if databases are to be backed up or restored, database-specific tools
are required. The recommended procedure is to use the corresponding image of the
database, as this usually contains all the necessary tools for dumping or
restoring data. `mcs-backup` and `restic` can easily be copied into this image.
Since both tools are statically linked executables, there are no further
dependencies that need to be copied into the container image. Here is an minimal
example of how this can be implemented for mariadb using the bitnami image.

```
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
```

For a working backup, wrapper scripts, either as pre- and post-hook scripts, or
pipe-in and pipe-out scripts, still need to be added. Environment variables are
also missing, but it is recommended to add them dynamically when the container
is launched.

[image]: https://hub.docker.com/r/nxcc/mcs-backup/tags
