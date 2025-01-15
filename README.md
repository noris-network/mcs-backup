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

## Usage
As MCS-Backup is scheduling backups and provides prometheus metrics, it should
be started as a service. In a kubernetes context it can run as sidecar container
or stand-alone deployment, depending on the usage scenario.

### File Backup
In order to backup plain files, `mcs-backup` needs to be able to access the file
system to be backed up. In a Kubernetes context, it is therefore best used as a
sidecar container to the main application, as this provides easy access to all
volumes mounted in the application container ([example][sidecar]).

### Database Backup
For example, if a mariadb is to be backed up, `mcs-backup` calls a [hook][hooks]
script which then calls mysqldump. The dump can either be written to the file
system (pre-backup-hook) and then be backed up as a regular file, or directly to
stdout (pipe-in-hook), in that case piping the data directly into restic (-->
see [hooks][hooks]). Since in this case no direct access to the database volume
is necessary, `mcs-backup` should be deployed separately from the database, i.e.
in the Kubernetes context it should run as an independant deployment.

### Restore
Since restoring data is the most important part of backup, and usually has to be
done at the most inconvenient times, possibly under time pressure, this process
should be as easy to perform as possible. With a properly configured mcs-backup
a simple `mcs-backup restore latest` command is sufficient.

## Scheduling of Backups
Backups are scheduled with cron-like expressions (e.g. `0 */2 * * *`)

### Maintenance Windows
In case you want to disable automatically scheduled backups for a maintenance
window, you can do this manually. For example, `mcs-backup backup --maintenance
1h` disables the backup for 1 hour. After the specified time, backup is
automatically reactivated.

## Documentation
Detailed configuration documentation and integration examples are available in
the [docs directory](docs).

## Changelog

  * `v1.5.0` Initial public release
  * `v1.5.1` Cleanup Paths in Dockerfile
  * `v1.5.2` Make https the default
  * `v1.5.3` Fix 'restore' for split repo-url (base/path)
  * `v1.5.4` Fix uncaught error when pipe cmd fails in restore
  * `v1.6.0` Allow to configure prune interval, update deps
  * `v1.6.1` routine update of deps
  * `v1.6.2` routine update of deps
  * `v1.6.3` routine update of deps
  * `v1.6.4` routine update of deps
  * `v1.6.5` routine update of Dockerfile
  * `v1.7.0` correct cron execution even when host was suspended between runs
  * `v1.7.1` skip backup when next run is in the past
  * `v1.7.2` update restic in Docker image
  * `v1.7.3` update base Docker image
  * `v1.7.4` update base Docker image
  * `v1.7.5` update base Docker image
  * `v1.7.6` update base Docker image
  * `v1.7.7` update base Docker image & deps
  * `v1.7.8` update base Docker image
  * `v1.7.9` update base Docker image & deps & go
  * `v1.7.10` update base Docker image & deps & go
  * `v1.7.11` update deps
  * `v1.7.12` update base Docker image
  * `v1.7.13` update base Docker image
  * `v1.7.14` update base Docker image & deps & go
  * `v1.7.15` update base Docker image & deps

[restic]:    https://github.com/restic/restic
[sidecar]:   test/deploy/demo/base/_common/deployment.yaml#L26-L48
[hooks]:     docs/hooks.md
