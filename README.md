# MCS-Backup

This backup solution has been created to backup data to S3 buckets using
[restic][restic]. As a wrapper it takes care of scheduling backups and
collecting backup and S3 metrics in order to make backup status more visible.

Features:

  * Backups are encrypted (restic)
  * Backups are stored in S3 buckets (restic)
  * Backups are incremental (restic)
  * Data is deduplicated (restic)
  * Backups are versioned (restic)
  * Data retention can be defined (restic)
  * Backups are performed regularly (mcs-backup)
  * Backup metrics are provided (mcs-backup)
  * Stale locks can be automatically removed (mcs-backup)
  * Hooks (pre, post, pipe-in/out) (mcs-backup)

Detailed configuration documentation and integration examples are available
in the [docs](docs/) directory.

## Usage
As MCS-Backup is scheduling backups and provides prometheus metrics, it should
be started as a background service. In a kubernetes context it can run as
sidecar container or stand-alone deployment, depending on the usage scenario.

### File Backup
In order to backup plain files, `mcs-backup` needs to be able to access the file
system to be backed up. In a Kubernetes context, it is therefore best used as a
sidecar container to the main application, as this provides easy access to all
volumes mounted in the application container ([example][sidecar]).

### Database Backup
For example, if a mariadb is to be backed up, `mcs-backup` calls a [hook][hooks]
script which then calls mysqldump. The dump can either be written to the file
system (pre-backup-hook) and then be backed up as a regular file, or directly to
stdout (pipe-in-hook), in that case piping the data directly into restic.

### Restore
Since restoring data is the most important part of backup, and usually has to be
done at the most inconvenient times, possibly under time pressure, this process
should be as easy as possible to perform. With a properly configured mcs-backup
a simple command `mcs-backup restore latest` is enough.

## Scheduling of Backups
Backups are scheduled with cron-like expressions (e.g. `0 */2 * * *`)

### Maintenance Windows
In case you want to disable automatically scheduled backups for a maintenance
window, you can do this manually. For example, `mcs-backup backup --maintenance
1h` disables the backup for 1 hour. After the specified time, backup is
automatically reactivated.

## Changelog

  * `v1.5.0` Initial public release
  * `v1.5.1` Cleanup Paths in Dockerfile


[restic]:    https://github.com/restic/restic
[sidecar]:   test/deploy/demo/base/_common/deployment.yaml#L26-L55
[hooks]:     docs/hooks.md