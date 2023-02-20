[<< back to index](readme.md)

# Configuration

> An overview of all environment variables that are
relevant for mcs-backup is [available elsewhere][envs].

## Metrics
MCS-Backup provides metrics for different topics:
  * Backup status (state, last run, snapshots, etc.)
  * S3 repository (size, change, object count, etc.)
  * Service health (cpu & memory usage, etc.)

Events such as backup start, various backup phases and the backup end are stored
in InfluxDB (2.x). Sending events to influxdb is activated if
`INFLUXDB_DATABASE`, `INFLUXDB_ORG`, `INFLUXDB_TOKEN` and `INFLUXDB_URL` are
set. When `mcs-backup` is started, a connection test is performed.

Ongoing metrics such as memory and cpu usage of the
mcs-backup service, disk usage of the S3 storage, are exposed for Prometheus.

If `LOKI_URL` is set, the log output is sent to [loki][loki].

## Storage
For historical reasons, only S3 compatible backends can currently be used as
storage backend for the backup repository (e.g. [minio][minio]). However, since
restic is much more flexible and supports various storage backends, it is
planned to remove this limitation. The S3 storage backend requires the
environment variables `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` to be set.
In addition, `RESTIC_REPOSITORY` must provide the url to access the desired
bucket.

## Backup
Environment variables are passed on to the restic processes started by
mcs-backup. Restic requires at least `RESTIC_REPOSITORY` and `RESTIC_PASSWORD`
to be set. To automatically start regular backups, `CRON_SCHEDULE` must contain
a valid cron expression, e.g. "5 */3 * * *" (every three hours, 5 minutes after
the hour). Otherwise, backups have to be started manually with `mcs-backup
backup`. The value must be a valid json, e.g. `{"weekly":8,"daily":7,"last":4}`.
The [exact functioning][retention] can be found in the restic documentation.


[envs]:       environment.md
[minio]:      https://min.io/
[retention]:  https://restic.readthedocs.io/en/stable/060_forget.html#removing-snapshots-according-to-a-policy
[loki]:       https://grafana.com/oss/loki/
