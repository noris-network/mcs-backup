# Configuration

mcs-backup is configured with environment variables. Optionally the same
settings can be given in a YAML file (`MCS_BACKUP_CONFIG=/etc/mcs-backup.yaml`).
Precedence: **environment > YAML file > defaults**. A variable that is set, even
to an empty string, wins over the file.

`mcs-backup config check` prints the effective configuration with all defaults
and reports every problem at once; `mcs-backup info` (= `config check --connect`)
additionally opens the repositories. Errors in the configuration stop `serve`
with exit code 2, so a broken setup shows up at deployment time and not at 3 am.

## YAML file

Keys are the variable names. Scalars are used as they are, lists are joined
(`BACKUP_PATHS` and `EXCLUDE_PATHS` with `:`, `RESTIC_*_ARGS` as shell-quoted
arguments) and mappings become JSON:

```yaml
RESTIC_REPOSITORY: s3:https://s3.example.com/backups/app
RESTIC_PASSWORD_FILE: /run/secrets/restic-password
BACKUP_ROOT: /data
CRON_SCHEDULE: "15 2 * * *"
RETENTION_POLICY:
  weekly: 8
  daily: 7
  last: 10
RESTIC_BACKUP_ARGS:
  - --one-file-system
  - --exclude-caches
METRICS_LABELS:
  app: shop
```

Secrets belong into `*_FILE` variables or the
environment, not into a file that is checked into a repository.

## Repository and credentials

| Variable | Default | Meaning |
| --- | --- | --- |
| `RESTIC_REPOSITORY` | – | Repository, any restic backend (`s3:…`, `rest:…`, `sftp:…`, `b2:…`, `azure:…`, `gs:…`, a path). |
| `RESTIC_REPOSITORY_BASE`, `RESTIC_REPOSITORY_PATH` | – | Alternative to `RESTIC_REPOSITORY`: composed as `<base>/<path>`. |
| `RESTIC_REPOSITORY_FILE` | – | File that contains the repository URL (passed to restic). |
| `RESTIC_PASSWORD`, `RESTIC_PASSWORD_FILE`, `RESTIC_PASSWORD_COMMAND` | – | One of them is required. |
| `AWS_*`, `B2_*`, `AZURE_*`, `GOOGLE_*`, `RESTIC_*` | – | Passed to restic unchanged, see the [restic documentation][restic-env]. |
| `S3_USE_V1` | `false` | Use the list-objects v1 API (`-o s3.list-objects-v1=true`) for S3 compatible storage that needs it. |
| `RESTIC_CACHE_DIR` | restic default (`$HOME/.cache`) | Passed through. Put it on a volume, see [the examples](examples/). |
| `APPEND_ONLY` | `false` | The repository only accepts new data, see [append-only](append-only.md). |
| `MCS_BACKUP_RESTIC` | `restic` | The restic executable. |

Secrets are masked (`***`) in the log, in run results, in notifications and on
the access page of the web UI. A variable holds a secret if its name contains
`PASSWORD`, `PASSWD`, `PASSPHRASE`, `SECRET`, `TOKEN` or `WEBHOOK`, or has
`PASS`, `PWD`, `KEY` or `SAS` as a later word (`RCLONE_CONFIG_PASS`,
`MYSQL_PWD`, `B2_ACCOUNT_KEY`). Names that end in `_FILE`, `_COMMAND` or `_ID`
and boolean values are not secrets. The password in a URL
(`https://user:password@host`) is masked in every variable. Name the variables
of your hooks and of the notification command the same way.

A missing repository is created by the service on its first start (`restic init`).
That includes the bucket (S3 and compatible servers) or container (Azure, Google Cloud
Storage), as in 1.x, if the credentials may create it; without that right the service logs
`Access Denied` and tries again.
`restore-if-empty` and the client commands never create one.

## What is backed up

| Variable | Default | Meaning |
| --- | --- | --- |
| `BACKUP_ROOT` | – (required; `/mnt` in the image) | Directory the backup runs in. Hooks run here as well, restores go here. |
| `BACKUP_PATHS` | `.` | Colon separated list of paths relative to `BACKUP_ROOT`. |
| `EXCLUDE_PATHS` | – | Colon separated list of patterns, passed to `--exclude`. |
| `MCS_BACKUP_HOST` | `mcs-backup` | Host name stored in snapshots. Retention only ever touches snapshots of this host (and 1.x snapshots with host `dummy`). **Instances that share a repository need different values.** |
| `RETENTION_POLICY` | – (required unless `APPEND_ONLY`; the image sets `{"weekly":8,"daily":7,"last":10}`) | JSON object with the `forget` keep options, e.g. `{"daily":7,"weekly":8,"monthly":12}`. Snapshots tagged `keep` are never forgotten. |
| `RESTIC_BACKUP_ARGS` | – | Additional arguments for `restic backup`, e.g. `--one-file-system --limit-upload 2000`. |
| `RESTIC_FORGET_ARGS`, `RESTIC_PRUNE_ARGS`, `RESTIC_CHECK_ARGS`, `RESTIC_RESTORE_ARGS` | – | Additional arguments for the other operations, e.g. `RESTIC_CHECK_ARGS=--read-data-subset=2%`. |

Additional arguments are split like a shell does (quotes work). Arguments that
mcs-backup sets itself (`--json`, `-r`/`--repo`, `--password*`, `--host`, `--tag`,
`--parent`, `--group-by`, `--dry-run`, `--target`, `--delete`, …) are rejected
at startup.

## Schedules

Cron expressions with five fields (or `@daily` and friends), evaluated in the
time zone `TZ` (default: the local zone; the image sets `GMT`). An empty value
switches the schedule off.

| Variable | Default | Meaning |
| --- | --- | --- |
| `CRON_SCHEDULE` | – (no scheduled backups) | When backups run. |
| `CRON_SCHEDULE_FILE` | – | File with the expression, re-read when it changes. Not together with `CRON_SCHEDULE`. |
| `PRUNE_SCHEDULE` | `0 3 * * 0` | When `prune` runs (also on the secondary repository). |
| `CHECK_SCHEDULE` | `0 4 1 * *` | When `check` runs. |
| `VERIFY_SCHEDULE` | `0 5 * * 6` | When the [restore test](#restore-test) runs. |
| `BACKUP_CATCH_UP` | `true` | After a start, a suspend, or the end of a maintenance window: run one backup if the repository has no snapshot or the last scheduled time is newer than the last snapshot. Needs `CRON_SCHEDULE`. |

What is due while another operation runs waits for the slot (once): first the
backup, then prune, check and verify. Only a backup that is due while a backup
is still running is skipped and logged.

## Timeouts, retries, safety

| Variable | Default | Meaning |
| --- | --- | --- |
| `BACKUP_TIMEOUT` | `12h` | Covers hooks, backup, forget and copy. `0` = no limit. |
| `PRUNE_TIMEOUT`, `CHECK_TIMEOUT`, `VERIFY_TIMEOUT` | `12h` | |
| `RESTORE_TIMEOUT` | `0` (none) | |
| `POST_HOOK_TIMEOUT` | `5m` | The post-backup and the post-restore hook run even after a timeout or cancellation and get their own limit. |
| `SHUTDOWN_TIMEOUT` | `25s` | Time to stop the running operation after SIGTERM (Kubernetes waits 30 s by default; raise `terminationGracePeriodSeconds` together with this for big dumps). Waiting notifications are sent within the rest of it, at least 3 s. |
| `BACKUP_RETRIES` | `2` | Scheduled and caught-up backups that fail are repeated this often, as complete runs including hooks. |
| `BACKUP_RETRY_DELAY` | `5m` | Delay between attempts; the slot is free meanwhile. |
| `ALLOW_EMPTY_SNAPSHOT` | `false` | An empty source is not an error. restic does not store empty snapshots anyway, the run then ends as `skipped`. |
| `BACKUP_MAX_SHRINK` | `50%` | A backup that processed this much less data than the last regular snapshot is stored but tagged `suspicious`, and no `forget` runs until it is acknowledged. `0` = off. |
| `FORGET_MAX_DELETE` | `25%` | A `forget` that would delete more than this share of the snapshots (and more than two) is held back until someone approves it (`approve-forget`). |
| `AUTO_UNLOCK_AFTER` | `0` (off) | If a run finds the repository locked and **all** locks are older than this, they are removed and the run is repeated. Keep it above `10m`: restic refreshes the lock of a running process every 5 minutes. |

## Secondary copy

After every successful backup the new snapshot can be copied to a second
repository (`restic copy`). It follows the same retention rules.

| Variable | Default | Meaning |
| --- | --- | --- |
| `COPY_REPOSITORY` | – | The second repository. |
| `COPY_PASSWORD`, `COPY_PASSWORD_FILE` | – | Exactly one of them is required with `COPY_REPOSITORY`. |
| `COPY_APPEND_ONLY` | `false` | The second repository is append-only (typical for off-site copies). |

`restic copy` opens both repositories in one process with the same environment,
so the backends can use different credentials only if they use different
variables (for example S3 for the main and B2 or a rest-server with credentials
in the URL for the copy), or one identity must have access to both.

## Hooks and pipes

Scripts must exist and be executable; `serve` refuses to start otherwise.
Details are in [hooks.md](hooks.md).

| Variable | Default | Meaning |
| --- | --- | --- |
| `PRE_BACKUP_SCRIPT`, `POST_BACKUP_SCRIPT` | – | Run before and after every backup. |
| `PRE_RESTORE_SCRIPT`, `POST_RESTORE_SCRIPT` | – | Run before and after every restore. Extra arguments of the restore command are passed on. |
| `PIPE_IN_SCRIPT` | – | Writes the data to stdout; its output is stored as a single file instead of `BACKUP_PATHS`. |
| `PIPE_OUT_SCRIPT` | – | Reads the data from stdin during a restore. |
| `PIPE_IN_FILENAME` | `stdin` | File name inside the snapshot in pipe mode. |

## Restore test

`verify` restores the latest regular snapshot into a sandbox and checks it with
`restic restore --verify`. It also works with `APPEND_ONLY`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `VERIFY_MODE` | `sample` | `sample` restores a random selection, `full` the whole snapshot. |
| `VERIFY_SAMPLE_FILES` | `100` | Upper bound of the sample (files). |
| `VERIFY_SAMPLE_BYTES` | `256MiB` | Upper bound of the sample (size). |
| `VERIFY_DIR` | system temp directory | Sandbox; must not be inside `BACKUP_ROOT`. Always emptied afterwards; if that fails, the test ends with a warning. If the free space is too small, the test is skipped with a warning. |
| `VERIFY_SCRIPT` | – | Is called with the sandbox path in `MCS_BACKUP_VERIFY_DIR`; its exit code decides. In pipe mode the dump is streamed to it on stdin. Without it, pipe mode has no restore test. |
| `VERIFY_COPY` | `false` | Test the secondary repository as well. |

## Notifications

mcs-backup has no built-in channels. It runs one command of yours for the events
you select; see [hooks.md](hooks.md#notifications) and the examples for
`notify-send`, `osascript` and webhooks.

| Variable | Default | Meaning |
| --- | --- | --- |
| `NOTIFY_COMMAND` | – | Run with `sh -c`. Event data is in `MCS_BACKUP_*` variables and as JSON on stdin. |
| `NOTIFY_ON` | `failure,warning,recovered` | Comma separated: `failure`, `warning`, `recovered`, `success`, `all`. |
| `NOTIFY_TIMEOUT` | `30s` | |

## Service, API and web UI

| Variable | Default | Meaning |
| --- | --- | --- |
| `BACKUP_HTTP_PORT` | `9000` | `/metrics`, `/healthz`, `/readyz`. Nothing else is served here. |
| `MCS_BACKUP_SOCKET` | `<tmp>/mcs-backup.sock` | Control socket (mode 0660, no authentication). The CLI, TUI and MCP server use it; `--socket` overrides it per command. |
| `MCS_BACKUP_STATE_DIR` | `<tmp>/mcs-backup` | Where maintenance windows are stored so that they survive a restart. |
| `MCS_BACKUP_WEB_ADDR` | – (off) | `host:port` of the web UI. It can also be switched on at runtime (`mcs-backup web`). |
| `MCS_BACKUP_WEB_TOKEN` | – | Optional fixed token for HTTP Basic Auth (any user name), for use behind an ingress. Use a long random one: a token below 16 characters earns a warning. After 5 wrong tokens in a row Basic Auth is refused for 30 seconds (status 429); sessions keep working. |
| `METRICS_LABELS` | – | YAML or JSON map of constant labels added to every metric; `$VAR` is expanded. |
| `MCS_BACKUP_LOG_FORMAT` | `json` | `json` or `text`. Logs go to stdout. |
| `MCS_BACKUP_DEBUG` | `false` | Debug logging. |
| `MCS_BACKUP_CONFIG` | – | YAML file, see above. |

`<tmp>` is `$TMPDIR` or `/tmp`. See [migration-1x.md](migration-1x.md) for
variables of version 1 that are ignored or renamed.

[restic-env]: https://restic.readthedocs.io/en/stable/040_backup.html#environment-variables
