# Test Coverage

## tested

backup
    * BACKUP_PATHS
    * EXCLUDE_PATHS
    * BACKUP_ROOT

enable/disable
    * backup --disable
    * backup --enable
    * backup --maintenance

hooks
    * PRE_BACKUP_SCRIPT
    * POST_BACKUP_SCRIPT
    * PRE_RESTORE_SCRIPT
    * POST_RESTORE_SCRIPT

pipes
    * PIPE_IN_SCRIPT
    * PIPE_OUT_SCRIPT

restore
    * restore latest
    * restore by snapshot id

retention
    * RETENTION_POLICY

env
    * RESTIC_REPOSITORY_BASE
    * RESTIC_REPOSITORY_PATH
    * METRICS_LABELS
    * CRON_SCHEDULE
    * CRON_SCHEDULE_FILE

metrics
    * METRICS_LABELS
    * INFLUXDB_URL
    * INFLUXDB_DATABASE
    * INFLUXDB_TOKEN
    * INFLUXDB_ORG

cron
    * CRON_SCHEDULE

logs
    * LOKI_URL
