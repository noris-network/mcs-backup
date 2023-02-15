
## Environment Variables

Overview:
| Variable                   | Required | Description
|----------------------------|----------|---------------------------------------
| s3:
| `AWS_ACCESS_KEY_ID`        | yes      | S3 Access Key ID
| `AWS_SECRET_ACCESS_KEY`    | yes      | S3 Secret Access Key
| mcs-backup config:
| `BACKUP_HTTP_PORT`         | no       | default: `9000`
| `BACKUP_PATHS`             | no       | `foo:bar`: only subdirectories  `foo` and `bar` will be backed up/restored
| `BACKUP_ROOT`              | no       | default: `/mnt`
| `CRON_SCHEDULE_FILE`       | no       | points to file containing schedule
| `CRON_SCHEDULE`            | no       | e.g. `0 */2 * * *`, default: no automatic backup
| `EXCLUDE_PATHS`            | no       | e.g. `fax:baz`: `bar` and `baz` will be excluded from backup
| `RETENTION_POLICY`         | no       | default: ``
| metrics:                   |          |
| `INFLUXDB_DATABASE`        | no       | e.g. `mcs`, has to pre-exist
| `INFLUXDB_ORG`             | no       |
| `INFLUXDB_TOKEN`           | no       |
| `INFLUXDB_URL`             | no       | e.g. `http://influxdb.backup-monitoring.svc:8086`
| `LOKI_URL`                 | no       | e.g. `http://loki-dev.backup-monitoring.svc:3100`
| `METRICS_LABELS`           | no       | e.g. `{"namespace":"foo","service":"bar"}`
| `S3_METRICS_TIMEOUT`       | no       | default: `5s`
| hook scripts:
| `PIPE_IN_SCRIPT`           | no       | script that dumps data to STDOUT
| `PIPE_OUT_SCRIPT`          | no       | script that read data from STDIN
| `POST_BACKUP_SCRIPT`       | no       | script to run after backup
| `POST_RESTORE_SCRIPT`      | no       | script to run after restore
| `PRE_BACKUP_SCRIPT`        | no       | script to run before backup
| `PRE_RESTORE_SCRIPT`       | no       | script to run before restore
| restic:
| `RESTIC_REPOSITORY`        | yes      | e.g. `s3:s3.example.com/bucket-name`
| `RESTIC_REPOSITORY_BASE`   | no       | when `RESTIC_REPOSITORY` is empty and `_BASE` and `_PATH` are...
| `RESTIC_REPOSITORY_PATH`   | no       | ...set, they are concatenated (_BASE+"/"+_PATH) and used instead
| `RESTIC_PASSWORD`          | yes      | Password for backup encryption

