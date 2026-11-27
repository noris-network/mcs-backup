# Append-only repositories

An append-only repository protects the backups against the machine that makes
them: if the machine or its credentials are compromised, the attacker can add
snapshots but cannot delete or overwrite old ones. This is the usual setup for
an off-site copy, or for the main repository of a host you do not fully trust.

```console
APPEND_ONLY=true            # the main repository
COPY_APPEND_ONLY=true       # the secondary copy
```

## What changes

With `APPEND_ONLY` (or `COPY_APPEND_ONLY` for the copy) mcs-backup

* never runs `forget`, `prune` or `unlock --all` there,
* does not tag snapshots there. With `APPEND_ONLY`, `keep`, `unkeep` and
  `acknowledge` are refused, and the web UI and the MCP server do not offer
  them; with `COPY_APPEND_ONLY` alone, tag changes are not mirrored to the copy,
* does not need `RETENTION_POLICY`,
* still checks plausibility: an empty or much smaller backup is reported through
  the metric `mcsbackup_backup_suspicious` and the event `backup.suspicious`, but
  cannot be tagged,
* still runs `check` and the restore test `verify` (both only read).

Somebody else has to delete old data: see [below](#deleting-old-data).

## What the storage must allow

restic creates a lock in the repository even for a backup and removes it again.
`--no-lock` does not apply to `backup`. The storage therefore has to allow
**deleting objects below `locks/`** and nothing else. Supported:

* **rest-server** with `--append-only`. It allows exactly this.
* **S3 or MinIO** with a policy that grants `DeleteObject` only on `locks/*`.
* Anything else that can express "may delete lock files only".

Not supported are WORM or Object Lock buckets that forbid deleting *every*
object: restic could not remove its locks, and every operation would fail with
a lock error.

`mcs-backup info` (= `config check --connect`) creates a lock and removes it
again when a repository is append-only, to find out about a wrong policy early
instead of in the next night's backup.

### rest-server

```console
rest-server --path /srv/restic --append-only --private-repos --htpasswd-file /srv/restic/.htpasswd
```

```console
RESTIC_REPOSITORY=rest:https://backup:secret@restic.example.com/shop
```

Credentials in the URL are fine for a rest-server and are the way to give the
copy repository its own identity.

### S3

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:ListBucket"],
      "Resource": "arn:aws:s3:::example-backups"
    },
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:PutObject"],
      "Resource": "arn:aws:s3:::example-backups/shop/*"
    },
    {
      "Effect": "Allow",
      "Action": ["s3:DeleteObject"],
      "Resource": "arn:aws:s3:::example-backups/shop/locks/*"
    }
  ]
}
```

(The policy assumes the repository lives under the prefix `shop/`. Versioning
of the bucket adds another layer of protection against overwriting.)

## Deleting old data

Run `forget` and `prune` from a separate, privileged process that has full
access to the repository: a cron job on the backup server, or for a
rest-server a second instance without `--append-only` that listens on
localhost only:

```console
rest-server --path /srv/restic --listen 127.0.0.1:8001 &
restic -r rest:http://127.0.0.1:8001/shop forget --host shop --group-by paths \
    --keep-daily 7 --keep-weekly 8 --prune
```

Use the same `--host` as the instance's `MCS_BACKUP_HOST`, and group by paths as
mcs-backup does. Check that the snapshots are healthy before pruning; a
shrunken or suspicious backup is exactly what append-only is meant to survive.
