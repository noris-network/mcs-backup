# Migrating from mcs-backup 1.x

Version 2 is a rewrite. Your configuration, your repositories and your hooks
carry over; the command line, the metric names and a few behaviours change.
Existing repositories need no conversion: 1.x snapshots are restored, used as
parents for incremental backups, and cleaned up by the retention like any other.

## Checklist

1. Install restic **0.17 or newer** (the image contains 0.20.0).
2. Start 2.x with the old environment. `mcs-backup config check` lists what is
   wrong or ignored before anything runs.
3. Give every instance that **shares a repository** its own `MCS_BACKUP_HOST`
   (see [retention](#retention-only-touches-its-own-snapshots)).
4. Update dashboards and alerts to the new [metric names](#metrics); the
   shipped PrometheusRule and Grafana dashboard in [examples/](examples/) cover them.
5. Replace anything that used the 1.x HTTP API (see below).
6. Check scripts and runbooks against the changed commands.

## Variables that keep working

`RESTIC_REPOSITORY`, `RESTIC_REPOSITORY_BASE`/`_PATH`, `RESTIC_PASSWORD*`,
`AWS_*`, `S3_USE_V1`, `BACKUP_ROOT`, `BACKUP_PATHS`, `EXCLUDE_PATHS`,
`RETENTION_POLICY`, `CRON_SCHEDULE`, `CRON_SCHEDULE_FILE`, `TZ`,
`AUTO_UNLOCK_AFTER`, `BACKUP_HTTP_PORT`, `METRICS_LABELS`, the four hook
variables, `PIPE_IN_SCRIPT`, `PIPE_OUT_SCRIPT`, `MCS_BACKUP_DEBUG` (switches on
debug logging).

* `BACKUP_PRUNE_INTERVAL=0` still switches pruning off (use an empty
  `PRUNE_SCHEDULE`). Other values are ignored with a warning: prune runs by
  `PRUNE_SCHEDULE` (default Sunday 03:00) now.
* `INFLUXDB_*`, `LOKI_URL` and `S3_METRICS_TIMEOUT` are ignored with a warning.
  Send logs from stdout with your log collector; events go to
  [`NOTIFY_COMMAND`](hooks.md#notifications).
* Not S3 only: every restic backend works, `RESTIC_REPOSITORY` is passed through.

## Behaviour that changes

### Hooks

* **A configured script that does not exist or is not executable is an error**
  at startup. 1.x ignored it silently and backed up without the hook.
* The post-backup hook **always runs**, also after a failed pre-hook, a timeout
  or a cancellation, and gets `MCS_BACKUP_RESULT`, `MCS_BACKUP_ERROR` and
  `MCS_BACKUP_SNAPSHOT`.
* A failing pre-hook is reported as a failure of the backup. 1.x could report
  hook failures wrongly.
* Arguments for the restore hooks are written after the snapshot, behind
  `--`: `mcs-backup restore latest -- --maintenance on`.

### Backups and safety

* An **empty source** never becomes a snapshot. With earlier snapshots it is a
  failure, on a fresh installation the run is `skipped`.
  `ALLOW_EMPTY_SNAPSHOT=true` keeps it from failing.
* A backup that is **much smaller** than the last one (`BACKUP_MAX_SHRINK`,
  default 50%) is stored, tagged `suspicious`, reported through a metric and an
  event, and stops the retention until `mcs-backup acknowledge` accepts it.
* A **retention that would delete a quarter of the snapshots** (and more than
  two) is held back until `mcs-backup approve-forget` (`FORGET_MAX_DELETE`).
  It catches typos in `RETENTION_POLICY`.
* A backup where restic could not read single files (exit code 3) keeps its
  snapshot and ends as `warning`.
* Failed scheduled backups are repeated twice after five minutes
  (`BACKUP_RETRIES`, `BACKUP_RETRY_DELAY`).
* Statistics and copy errors are warnings, they do not fail the backup.

### Retention only touches its own snapshots

`forget` runs with `--host $MCS_BACKUP_HOST` (default `mcs-backup`) and
`--group-by paths`, plus the host `dummy` that 1.x used, so old snapshots are
cleaned up. Snapshots of other hosts in a shared repository are never touched.
If you change the paths of a backup, the old path group keeps at most as many
snapshots as the policy allows.

**If several instances write to one repository, give each a different
`MCS_BACKUP_HOST`.** Otherwise they would count each other's snapshots against
their policy.

### Restore

* Restoring in place **needs a confirmation**: you type the snapshot name, or
  pass `--confirm` (required without a terminal).
* Before the data is replaced, a **safety snapshot** (tag `pre-restore`) is
  taken. If that fails the restore stops; `--no-safety-snapshot` continues
  deliberately.
* `restore` restores `exact` by default (files that are not in the snapshot are
  removed); `--merge` keeps them. `--include` restores parts.
* Free space is checked first; `--force` overrides a warning.
* **`POST_RESTORE_SCRIPT` also runs after a failed restore**, so that it can
  undo what `PRE_RESTORE_SCRIPT` did. A script that starts the application must
  check `MCS_BACKUP_RESULT` first, see [hooks](hooks.md#backup-and-restore-hooks).
* `restore-if-empty` is the command for init containers. It does not contact
  the repository when the directory already has data.

### Commands

| 1.x | 2.x |
| --- | --- |
| `mcs-backup forget` ran the retention | shows what the policy would delete; only the service deletes |
| the HTTP API with Basic Auth on the TCP port | gone; the port serves `/metrics`, `/healthz`, `/readyz`. Commands use a Unix socket, remote control is the optional [web UI](../README.md#web-ui) |
| `mcs-backup backup` / `restore` / `status` | the same names, new options, `--json` and [fixed exit codes](agents.md#command-line-for-scripts) |
| – | `snapshots`, `locks`, `unlock`, `keep`, `verify`, `log`, `tui`, `web`, `mcp`, `notify test`, `env` |

Disable and enable of backups lives in memory only: **`--disable` ends with a
restart**, so a forgotten disable cannot silently stop backups for good. A
maintenance window (`backup --maintenance 1h`) is stored and survives restarts.

### Probes

`/healthz` says the process is alive, `/readyz` that the configuration is
loaded and the API is up, **independent of the repository**: a storage outage
must not take the application out of its Service.

## Metrics

All metrics start with `mcsbackup_`. After a restart the service reads the
repository first, so `mcsbackup_last_success_timestamp_seconds` and
`mcsbackup_snapshots` are there within seconds. Results of runs
(`last_run_success{kind}`) appear after the first run since the start; alerts
on `== 0` do not fire for missing series.

| 1.x | 2.x |
| --- | --- |
| `snapshot_latest_unixtime` | `mcsbackup_last_success_timestamp_seconds` |
| `snapshots_available_total` | `mcsbackup_snapshots` |
| `success`, `duration_seconds` | `mcsbackup_last_run_success{kind}`, `mcsbackup_last_run_duration_seconds{kind}`, `mcsbackup_last_run_timestamp_seconds{kind}` |
| `files_*_total` | `mcsbackup_backup_files{state="new\|changed\|unmodified"}` |
| `data_processed_bytes`, `data_added_bytes` | `mcsbackup_backup_bytes{type="processed\|added"}` |
| `bucket_size_bytes` | `mcsbackup_repository_size_bytes` |
| `phase_running` | `mcsbackup_operation_running{operation}` |
| `objects_total`, S3 specific | dropped |

New: `repository_up`, `copy_repository_up`, `backup_suspicious`, `forget_held`,
`backup_enabled`, `maintenance_until_timestamp_seconds`,
`next_run_timestamp_seconds{kind}`, `notify_errors_total`, `webui_enabled`. The
Go runtime and process metrics are exported as well.

## Image

* Alpine and restic are current; the image exists for `linux/amd64` and
  `linux/arm64`.
* `curl` and `/opt/bin/cleanup-backup-root.sh` are still in the image, `yq` is gone.
* It runs as any user and with a read-only root file system, writing only to
  `/tmp` (HOME, restic cache, socket). Mount an `emptyDir` or a volume there.
* The default user is still root.
