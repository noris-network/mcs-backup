# Hooks
Hooks are triggered in different phases of the backup/restore process. Scripts
are executed in the configured backup root directory, or `/mnt` when not set.

## pipe-in
When the environment variable `PIPE_IN_SCRIPT` points to some existing script,
it is executed and all output to `stdout` is piped into `restic` (think: `script
| restic`). This is particularly beneficial if, for example, large databases are
to be backed up, as no temporary disk space is required. In addition, the backup
is faster because data does not have to be temporarily written to the file
system.

Example "pipe-in" script for mariadb:
```
#!/bin/bash
exec mysqldump -h "$MARIADB_HOST" --single-transaction \
    -u "$MARIADB_ROOT_USER" -p"$MARIADB_ROOT_PASSWORD" \
    "$MARIADB_DATABASE"
```

## pipe-out
When the environment variable `PIPE_OUT_SCRIPT` points to some existing script,
then it is executed and data from `restic` is sent to the scripts's stdin during
recovery (think: `restic | script`). This can directly send the data to the
database, without taking the detour via the file system.

Example "pipe-out" script for mariadb:
```
#!/bin/bash
exec mysql -h "$MARIADB_HOST" \
    -u "$MARIADB_ROOT_USER" -p"$MARIADB_ROOT_PASSWORD" \
    "$MARIADB_DATABASE"
```

## pre-backup
When the environment variable `PRE_BACKUP_SCRIPT` points to some existing
script, it is executed before `restic` is starting to backup the configured
directory. This could, for example, creata a database dump in the backup root
directory, if the preferred "pipe-in" method cannot be used for some reason. As
`restic` automatically compresses data, it is recommended not to compress dumps,
as this would interfere with `restic`'s "content defined chunking" based
deduplication.

## post-backup
When the environment variable `POST_BACKUP_SCRIPT` points to some existing
script, it is executed afer `restic` backup finished. This could e.g. cleanup
database dumps created in the `pre-backup` phase.

Example "post-backup" script:
```
#!/bin/bash
find "${BACKUP_ROOT?not set}" -delete
```

## pre-restore
When the environment variable `PRE_RESTORE_SCRIPT` points to some existing
script, it is executed before `restic` is starting to restore the configured
directory. This could be, for example, clean up the backup root directory to
make sure that no unwanted files remain.


## post-restore
When the environment variable `POST_RESTORE_SCRIPT` points to some existing
script, it is executed after `restic` restored data to the configured directory.
This could, for example, import a restored data dump into a database.
