# Migrating from mcs-backup 1.x

Version 2 is a rewrite. Your configuration, your repositories and your hooks
carry over, with one exception: `/opt/bin/cleanup-backup-root.sh` is gone (see
[restore](#restore)). The command line, the metric names and a few behaviors
change. Existing repositories need no conversion: 1.x snapshots are restored,
used as parents for incremental backups, and cleaned up by the retention like
any other.

## Checklist

1. **Pin the image tag** to a version such as `v2.0.0`. Every release moves
   `latest`, so it jumps from 1.x to 2.x with 2.0.0 (a release candidate does
   not move it).
2. On a host, install restic **0.17 or newer** (the image contains 0.20.0).
3. **Remove `PRE_RESTORE_SCRIPT=/opt/bin/cleanup-backup-root.sh`.** The script
   is no longer in the image, and 2.x does not start with a hook script that
   does not exist. It is not needed either: an in-place restore removes the
   files that are not in the snapshot by itself. Running before the safety
   snapshot, the script would delete the data that the snapshot is meant to save.
4. Start 2.x with the old environment. `mcs-backup config check` lists what is
   wrong or ignored before anything runs.
5. Give every instance that **shares a repository** its own `MCS_BACKUP_HOST`
   (see [retention](#retention-only-touches-its-own-snapshots)).
6. Update dashboards and alerts to the new [metric names](#metrics); the
   shipped PrometheusRule and Grafana dashboard in [examples/](examples/) cover them.
7. Replace anything that used the 1.x HTTP API (see [commands](#commands)).
8. Check scripts, init containers and runbooks against the changed
   [commands](#commands): `restore` needs the running service now, and some
   options are gone.
9. Make room for the weekly [restore test](#new-scheduled-jobs) in the temporary
   directory, or switch it off.

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
* `AUTO_UNLOCK_AFTER` below `10m` is raised to `10m` with a warning: restic
  refreshes the lock of a running process every 5 minutes, so a younger lock may
  belong to one.
* `INFLUXDB_*`, `LOKI_URL` and `S3_METRICS_TIMEOUT` are ignored with a warning.
  Send logs from stdout with your log collector; events go to
  [`NOTIFY_COMMAND`](hooks.md#notifications).
* Not S3 only: every restic backend works, `RESTIC_REPOSITORY` is passed through.

## Behavior that changes

### Hooks

* **A configured script that does not exist or is not executable is an error**
  at startup. 1.x ignored it silently and backed up without the hook.
* The post-backup hook **always runs**, also after a failed pre-hook, a timeout
  or a cancellation, and gets `MCS_BACKUP_RESULT`, `MCS_BACKUP_ERROR` and
  `MCS_BACKUP_SNAPSHOT`.
* A failing pre-hook is reported as a failure of the backup. 1.x could report
  hook failures wrongly.
* The pre-restore hook runs **before the safety snapshot**, so it must not
  delete the data.
* Arguments for the restore hooks are written after the snapshot, behind
  `--`: `mcs-backup restore latest -- --maintenance on`.

### Backups and safety

* An **empty source** never becomes a snapshot. With earlier snapshots it is a
  failure, on a fresh installation the run is `skipped`.
  `ALLOW_EMPTY_SNAPSHOT=true` keeps it from failing.
* A backup that is **much smaller** than the last one (`BACKUP_MAX_SHRINK`,
  default 50%) is stored, tagged `suspicious`, reported through a metric and an
  event, and stops the retention until `mcs-backup acknowledge` accepts it.
* A **retention that would delete more than a quarter of the snapshots** (and
  more than two) is held back until `mcs-backup approve-forget`
  (`FORGET_MAX_DELETE`). It catches typos in `RETENTION_POLICY`.
* A backup where restic could not read single files (exit code 3) keeps its
  snapshot and ends as `warning`.
* Failed scheduled backups are repeated twice after five minutes
  (`BACKUP_RETRIES`, `BACKUP_RETRY_DELAY`).
* Statistics and copy errors are warnings, they do not fail the backup.

### New scheduled jobs

Besides the backups and `prune`, two jobs run by default:

* `check` of the repository structure, monthly (`CHECK_SCHEDULE`, default the
  1st at 04:00).
* The [restore test](configuration.md#restore-test), weekly (`VERIFY_SCHEDULE`,
  default Saturday 05:00). It restores a sample of the latest snapshot, at most
  100 files and 256 MiB, into the system temporary directory (`VERIFY_DIR`), so
  `/tmp` needs that much room. In pipe mode it runs only with a `VERIFY_SCRIPT`.

An empty schedule switches a job off.

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

* **`restore` goes through the running service.** 1.x ran restic itself, so it
  also worked in an init container or a job without `serve`. For an init
  container 2.x has `restore-if-empty`: it restores the latest snapshot into an
  empty `BACKUP_ROOT` and does not contact the repository when the directory
  already has data. `mcs-backup restic …` runs restic with the configuration of
  the instance, without the service.
* Restoring in place **needs a confirmation**: you type the snapshot name, or
  pass `--confirm` (required without a terminal).
* Before the data is replaced, a **safety snapshot** (tag `pre-restore`) is
  taken. If that fails the restore stops; `--no-safety-snapshot` continues
  deliberately.
* `restore` restores `exact` by default: files that are not in the snapshot are
  removed. 1.x left them in place; `--merge` keeps them as 1.x did. `--include`
  restores parts. `/opt/bin/cleanup-backup-root.sh`, which deleted everything
  in `BACKUP_ROOT`, is gone from the image.
* Free space is checked first; `--force` overrides a warning.
* **`POST_RESTORE_SCRIPT` also runs after a failed restore**, so that it can
  undo what `PRE_RESTORE_SCRIPT` did. A script that starts the application must
  check `MCS_BACKUP_RESULT` first, see [hooks](hooks.md#backup-and-restore-hooks).

### Commands

| 1.x | 2.x |
| --- | --- |
| `restore` ran restic itself, without the service | goes through the service, see [restore](#restore); `restore-if-empty` and `restic restore …` work without it |
| `forget [-n]` ran the retention | `forget` shows what the policy would delete; only the service deletes, `approve-forget` releases a held forget |
| `snapshots` passed its arguments to `restic snapshots` | lists the snapshots through the service, newest first, with their flags; `mcs-backup restic snapshots …` gives the old output |
| `env` with `-e`, `-s`, `-d` | `env --format export` (the default), `dotenv` or `docker`, for every backend |
| `-D`/`--debug` of `serve`, `restore`, `forget` | `MCS_BACKUP_DEBUG=true` |
| the HTTP API with Basic Auth on the TCP port, `serve --noauth` | gone; the port serves `/metrics`, `/healthz`, `/readyz`. Commands use a Unix socket, remote control is the optional [web UI](../README.md#web-ui) |
| `backup` (also `--enable`, `--disable`, `--status`, `--maintenance`), `info`, `restic`, `version` | the same names, new options, `--json` and [fixed exit codes](agents.md#command-line-for-scripts); `info` also checks the access to the repositories |
| – | `status`, `cancel`, `ls`, `find`, `diff`, `prune`, `check`, `verify`, `keep`, `unkeep`, `acknowledge`, `approve-forget`, `locks`, `unlock`, `log`, `notify test`, `restore-if-empty`, `config check`, `tui`, `web`, `kc`, `mcp` |

Disable and enable of backups lives in memory only: **`--disable` ends with a
restart**, so a forgotten disable cannot silently stop backups for good. A
maintenance window (`backup --maintenance 1h`) is stored and survives restarts.

### Probes

`/healthz` says the process is alive, `/readyz` that the configuration is
loaded and the API is up, **independent of the repository**: a storage outage
must not take the application out of its Service.

## Metrics

All metrics still start with `mcsbackup_`, but most names change. After a
restart the service reads the repository first, so
`mcsbackup_last_success_timestamp_seconds` and `mcsbackup_snapshots` are there
within seconds. Results of runs (`last_run_success{kind}`) appear after the
first run since the start; alerts on `== 0` do not fire for missing series.

| 1.x | 2.x |
| --- | --- |
| `mcsbackup_snapshot_latest_unixtime` | `mcsbackup_last_success_timestamp_seconds` |
| `mcsbackup_snapshots_available_total` | `mcsbackup_snapshots` |
| `mcsbackup_status_success{phase}`, `mcsbackup_status_duration_seconds{phase}` | `mcsbackup_last_run_success{kind}`, `mcsbackup_last_run_duration_seconds{kind}`, and new `mcsbackup_last_run_timestamp_seconds{kind}` |
| `mcsbackup_phase_running` | `mcsbackup_operation_running{operation}` |
| `mcsbackup_restic_files_new_total`, `…_changed_total`, `…_unmodified_total` | `mcsbackup_backup_files{state="new\|changed\|unmodified"}` |
| `mcsbackup_restic_files_processed_total` | the sum of `mcsbackup_backup_files` |
| `mcsbackup_restic_data_processed_bytes`, `mcsbackup_restic_data_added_bytes` | `mcsbackup_backup_bytes{type="processed\|added"}` |
| `mcsbackup_s3_bucket_size_bytes{bucket}` | `mcsbackup_repository_size_bytes`, for every backend |
| `mcsbackup_restic_dirs_*_total`, `mcsbackup_restic_data_blobs_total`, `mcsbackup_restic_tree_blobs_total`, `mcsbackup_status_inprogress_id`, `mcsbackup_status_error`, `mcsbackup_snapshots_forgot_total`, `mcsbackup_s3_objects_total`, `mcsbackup_s3_stats_*` | dropped |

The label `phase` becomes `kind`. A backup run includes its retention, so
`phase="backup"`, `"forget"` and `"overall"` all become `kind="backup"`;
`prune` stays `prune`. `getstats` and `s3stats` are gone,
`mcsbackup_stats_errors_total` counts failed statistics instead.

New: `info`, `repository_up`, `copy_repository_up`,
`copy_last_success_timestamp_seconds`, `backup_suspicious`, `forget_held`,
`backup_enabled`, `maintenance_until_timestamp_seconds`,
`next_run_timestamp_seconds{kind}`, `stats_errors_total`, `notify_errors_total`,
`webui_enabled`, `webui_until_timestamp_seconds`. The Go runtime and process
metrics are exported as well.

## Image

* Tags are `vX.Y.Z` as in 1.x. Every release moves `latest`, a release
  candidate does not.
* Alpine and restic are current; the image exists for `linux/amd64` and
  `linux/arm64`.
* `curl` is still in the image; `yq` and `/opt/bin/cleanup-backup-root.sh` are
  gone.
* It runs as any user and with a read-only root file system, writing only to
  `/tmp` (HOME, restic cache, socket, state directory, the sandbox of the
  restore test). Mount an `emptyDir` or a volume there.
* The default user is still root.
