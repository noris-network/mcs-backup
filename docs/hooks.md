# Hooks, pipes and notifications

## Backup and restore hooks

| Variable | Runs | If it fails |
| --- | --- | --- |
| `PRE_BACKUP_SCRIPT` | first step of every backup | the backup stops; no snapshot is made |
| `POST_BACKUP_SCRIPT` | last step of every backup, **always** (also after a failure, a timeout or a cancellation) | the run ends with the result `warning` |
| `PRE_RESTORE_SCRIPT` | before a restore, and before its safety snapshot | the restore stops |
| `POST_RESTORE_SCRIPT` | last step of every restore that got as far as the pre-restore hook, **always** (also after a failure, a timeout or a cancellation) | a good restore is reported as failed, although the data is already restored; a failed one keeps its error and gets a warning |

All scripts

* are started with `BACKUP_ROOT` as working directory,
* get the whole configuration as environment (so `restic` called from a script
  uses the same repository and password; the `RESTIC_*`, `AWS_*` … variables are
  all there),
* must exist and be executable when the service starts,
* write to stdout and stderr as they like; every line ends up in the run log
  (`mcs-backup log`),
* receive SIGTERM when the operation is cancelled or times out, and SIGKILL ten
  seconds later.

The post-backup and the post-restore hook additionally get the outcome, so one
script can both clean up and report:

| Variable | Value |
| --- | --- |
| `MCS_BACKUP_RESULT` | `success`, `warning`, `skipped`, `failure`, `suspicious`, `timeout` or `cancelled` |
| `MCS_BACKUP_ERROR` | the error message, empty on success |
| `MCS_BACKUP_SNAPSHOT` | backup: ID of the new snapshot, empty if none was made; restore: ID of the restored snapshot |

Both have their own time limit, `POST_HOOK_TIMEOUT` (default 5 minutes), so a
backup that ran into `BACKUP_TIMEOUT` can still clean up.

**Check `MCS_BACKUP_RESULT` in a post-restore hook.** It also runs when the
restore failed, so that it can undo what the pre-restore hook did. The data may
then be restored only in part: leave the maintenance mode, but do not start the
application on it as if nothing had happened.

```sh
#!/bin/sh
# POST_RESTORE_SCRIPT
case "$MCS_BACKUP_RESULT" in
  success|warning) rm -f .maintenance ;;
  *) echo "restore ended as $MCS_BACKUP_RESULT, staying in maintenance: $MCS_BACKUP_ERROR" >&2 ;;
esac
```

A restore that is refused before it begins (no confirmation, unknown snapshot,
too little space) runs neither hook.

**The pre-restore hook must not delete the data.** It runs before the safety
snapshot of an in-place restore, which saves what the hook leaves. Removing the
files that are not in the snapshot is the job of the restore itself (unless
`--merge` is given).

### Restore hooks get arguments

Everything after the snapshot on the command line is handed to both restore
hooks. Put `--` before arguments that look like flags:

```console
$ mcs-backup restore latest -- --maintenance on
```

runs `PRE_RESTORE_SCRIPT --maintenance on` and `POST_RESTORE_SCRIPT --maintenance on`.
Use this to tell the application what is going on. The sidecar of a pod is
stopped together with the pod, so an application that must stay in maintenance
mode while its data is replaced is best switched by the pre-restore hook, for
example by creating a flag file on the shared volume or by calling an API.

### Example: freeze a database for a file backup

```sh
#!/bin/sh
# PRE_BACKUP_SCRIPT: write a consistent dump next to the data
set -eu
mysqldump --single-transaction --all-databases > dumps/all.sql
```

```sh
#!/bin/sh
# POST_BACKUP_SCRIPT: remove the dump, tell the monitoring
set -u
rm -f dumps/all.sql
[ "$MCS_BACKUP_RESULT" = success ] && curl -fsS -m 10 https://hc-ping.example.com/uuid
```

The dump is part of the snapshot because the pre-hook ran in `BACKUP_ROOT`
before restic did.

## Pipe mode

With `PIPE_IN_SCRIPT` set, the backup is not a directory but the output of the
script, stored as one file called `PIPE_IN_FILENAME` (default `stdin`):

```console
PIPE_IN_SCRIPT=/scripts/dump.sh        # mysqldump … ; stdout becomes the snapshot
PIPE_OUT_SCRIPT=/scripts/load.sh       # mysql … ; stdin receives the snapshot
PIPE_IN_FILENAME=all.sql
```

* If the script fails, **no snapshot is stored** (restic is started with
  `--stdin-from-command`).
* A restore streams the file of the snapshot into `PIPE_OUT_SCRIPT`.
  This replaces live data, so it is treated like an in-place restore:
  confirmation is required, and a safety snapshot is taken first by running
  `PIPE_IN_SCRIPT` again. If the database is dead and the dump cannot run, the
  restore stops; continue deliberately with `restore --no-safety-snapshot`.
* The safety check for shrunken backups works on the processed bytes, so it
  applies to dumps too.
* Make sure a dump is complete: end it with a marker (`-- Dump completed`
  for mysqldump) and let the [restore test](configuration.md#restore-test)
  check for it with `VERIFY_SCRIPT`, which receives the dump on stdin in pipe
  mode.

## Restore test scripts

`VERIFY_SCRIPT` decides if a restore test passed by its exit code.

* File mode: the sample or the full snapshot is restored into a sandbox; the
  script is started in it, `MCS_BACKUP_VERIFY_DIR` names the directory.
  Example: `sha256sum -c checksums.txt`, `tar -tf database.tar >/dev/null`.
* Pipe mode: the dump is streamed to the script's stdin, nothing is stored.
  Example: `tail -n 1 | grep -q -- '-- Dump completed'`, or load the dump into a
  throwaway database and run a query.

## Notifications

`NOTIFY_COMMAND` is run with `sh -c` once per event that matches `NOTIFY_ON`.
It is independent of the hooks, and a broken command never changes the result
of an operation: failures show up in the log, in the metric
`mcsbackup_notify_errors_total` and as a warning in the status. Events are
sent one at a time, in order. At shutdown the events still waiting are sent
within what is left of `SHUTDOWN_TIMEOUT`, but at least 3 seconds; whatever
does not make it is logged.

Event data never becomes part of the command text. It is passed as environment
variables and as one JSON object on stdin:

| Variable | Content |
| --- | --- |
| `MCS_BACKUP_EVENT` | event name, see below |
| `MCS_BACKUP_KIND` | `backup`, `copy`, `prune`, `check`, `verify`, `restore` |
| `MCS_BACKUP_RESULT`, `MCS_BACKUP_MESSAGE`, `MCS_BACKUP_ERROR`, `MCS_BACKUP_SNAPSHOT` | what happened |
| `MCS_BACKUP_HOST` | the `MCS_BACKUP_HOST` of the instance |
| `MCS_BACKUP_TIME` | RFC 3339 timestamp |

The JSON on stdin has the same fields (`event`, `kind`, `result`, `message`,
`error`, `snapshot`, `repository`, `host`, `time`) and `repository` (`main` or
`copy`) for the repository events. restic, cloud and `COPY_*` credentials are
removed from the environment of the command.

| Events | `NOTIFY_ON` category |
| --- | --- |
| `<kind>.failed`, `backup.suspicious`, `repository.down` | `failure` |
| `<kind>.warning`, `retention.held` | `warning` |
| `<kind>.recovered`, `repository.up` | `recovered` |
| `<kind>.succeeded` | `success` |

`all` selects everything. Notifications are sent after the last retry,
`repository.down` and `repository.up` only when the state changes, and
`skipped` runs say nothing. Try the command with `mcs-backup notify test`.

Ready-to-use commands for `notify-send` (Linux desktop), `osascript` (macOS)
and webhooks are in [examples/](examples/).
