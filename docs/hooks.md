# Hooks
Hooks are triggered at various stages of the backup/restore process. Scripts are
executed in the configured backup root directory, or `/mnt` when not specified.

## pipe-in
If the [environment variable][envs] `PIPE_IN_SCRIPT` points to an existing
script, it will be executed and all output to `stdout` will be piped into
`restic` (think: `script | restic`). This is particularly useful when backing up
large databases, as no temporary disk space is required. In
addition, the backup is faster because data does not have to be written
temporarily to the file system.

Example "pipe-in" script for mariadb:
```
#!/bin/bash
exec mysqldump -h "$MARIADB_HOST" --single-transaction \
    -u "$MARIADB_ROOT_USER" -p"$MARIADB_ROOT_PASSWORD" \
    "$MARIADB_DATABASE"
```

## pipe-out
If the [environment variable][envs] `PIPE_OUT_SCRIPT` points to an existing
script, it will be executed and data from `restic` will be sent to the scripts's
stdin during recovery (think: `restic | script`). This can directly send the
data to the database, without going through the file system.

Example "pipe-out" script for mariadb:
```
#!/bin/bash
exec mysql -h "$MARIADB_HOST" \
    -u "$MARIADB_ROOT_USER" -p"$MARIADB_ROOT_PASSWORD" \
    "$MARIADB_DATABASE"
```

## pre-backup
If the [environment variable][envs] `PRE_BACKUP_SCRIPT` points to an existing
script, it will be executed before `restic` starts to backup the configured
directory. This could, for example, create a database dump in the backup root
directory, in case the preferred "pipe-in" method cannot be used for some reason. As
`restic` automatically compresses data, it is recommended that you do not
compress data yourself, as this would interfere with `restic`'s "content defined
chunking" based [deduplication][cdc].

## post-backup
If the [environment variable][envs] `POST_BACKUP_SCRIPT` points an existing
script, it will be executed afer the `restic` backup has finished. This could
e.g. clean up database dumps created during the `pre-backup` phase.

Example "post-backup" script:
```
#!/bin/bash
find "${BACKUP_ROOT?not set}" -delete
```

## pre-restore
If the [environment variable][envs] `PRE_RESTORE_SCRIPT` points to an existing
script, it will be executed before `restic` starts to restore the configured
directory. This could, for example, clean up the backup root directory to ensure
that no unwanted files are left behind.

## post-restore
If the [environment variable][envs] `POST_RESTORE_SCRIPT` points to an existing
script, it will be executed after `restic` has restored data to the configured
directory. This could, for example, import a restored data dump into a database.

[envs]:       environment.md
[cdc]:        https://restic.readthedocs.io/en/latest/100_references.html#backups-and-deduplication
