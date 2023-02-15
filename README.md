# MCS-Backup

This backup solution has been created to backup data to S3 buckets using
[restic][restic]. As a wrapper it takes care of scheduling backups and
collecting backup and S3 metrics in order to make backup status more visible.

Features:

  * Backups are encrypted (resic)
  * Backups are stored in S3 buckets (resic)
  * Backups are incremental (resic)
  * Data is deduplicated (resic)
  * Backups are versioned (resic)
  * Data retention can be defined (resic)
  * Backups are performed regularly (mcs-backup)
  * Backup metrics are provided (mcs-backup)
  * Stale locks can be automatically removed (mcs-backup)
  * Hooks (pre, post, pipe-in/out) (mcs-backup)

## Usage
As MCS-Backup is scheduling backups and provides prometheus metrics, it should
be started as a background service. In a kubernetes context it can run as
sidecar container or stand-alone deployment, depending on the usage scenario.

### File Backup
In order to backup plain files, `mcs-backup` needs to be able to access the file
system to be backed up. In a Kubernetes context, it is therefore best used as a
sidecar container to the main application, as this provides easy access to all
volumes mounted in the application container.

### Database Backup
For example, if a mariadb is to be backed up, `mcs-backup` calls a pre hook
script which then calls mysqldump. The dump can either be written to the file
system and then saved as a regular file, or directly to stdout, in that case
piping the data directly into restic.

### Restore
Since restoring data is the most important part of backup, and usually has to be
done at the most inconvenient times, possibly under time pressure, this process
should be as easy as possible to perform. With a properly configured mcs-backup
a simple command `mcs-backup restore latest` is enough.

## Scheduling of backups
Backups are scheduled with cron-like expressions (e.g. `0 */2 * * *`)

## Hooks
wip...


[restic]: https://github.com/restic/restic