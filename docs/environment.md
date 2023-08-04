[<< back to index](readme.md)

## Environment Variables

Overview:
<table>
    <thead>
        <tr>
            <th>Variable</th>
            <th>Required?</th>
            <th>Description</th>
        </tr>
    </thead>
    <tbody>
        <tr>
            <td colspan="3">&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;s3:</td>
        </tr>
        <tr>
            <td><code>AWS_ACCESS_KEY_ID</code></td>
            <td>yes</td>
            <td>S3 Access Key ID</td>
        </tr>
        <tr>
            <td><code>AWS_SECRET_ACCESS_KEY</code></td>
            <td>yes</td>
            <td>S3 Secret Access Key</td>
        </tr>
        <tr>
            <td colspan="3">&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;mcs-backup config:</td>
        </tr>
        <tr>
            <td><code>BACKUP_HTTP_PORT</code></td>
            <td>no</td>
            <td>default: <code>9000</code></td>
        </tr>
        <tr>
            <td><code>BACKUP_PATHS</code></td>
            <td>no</td>
            <td>e.g. "<code>foo:bar</code>":&nbsp;&nbsp;&nbsp;&nbsp;only subdirectories <code>foo</code> and
                <code>bar</code> will be backed up/restored
            </td>
        </tr>
        <tr>
            <td><code>BACKUP_PRUNE_INTERVAL</code></td>
            <td>no</td>
            <td>e.g. "<code>168h</code>":&nbsp;&nbsp;&nbsp;&nbsp;prune will be run every 168 hours; default: <code>120h</code>
            </td>
        </tr>
        <tr>
            <td><code>BACKUP_ROOT</code></td>
            <td>no</td>
            <td>default: "<code>/mnt</code>"</td>
        </tr>
        <tr>
            <td><code>CRON_SCHEDULE_FILE</code></td>
            <td>no</td>
            <td>points to file containing schedule</td>
        </tr>
        <tr>
            <td><code>CRON_SCHEDULE</code></td>
            <td>no</td>
            <td>e.g. "<code>0 */2 * * *</code>", default: no automatic backup</td>
        </tr>
        <tr>
            <td><code>EXCLUDE_PATHS</code></td>
            <td>no</td>
            <td>e.g. "<code>bar:baz</code>":&nbsp;&nbsp;&nbsp;&nbsp;<code>bar</code> and
                <code>baz</code> will be excluded from backup
            </td>
        </tr>
        <tr>
            <td><code>RETENTION_POLICY</code></td>
            <td>yes</td>
            <td>no default</td>
        </tr>
        <tr>
            <td colspan="3">
                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;
                metrics:</td>
        </tr>
        <tr>
            <td><code>INFLUXDB_DATABASE</code></td>
            <td>no</td>
            <td>e.g. <code>mcs</code>,
                has to pre-exist</td>
        </tr>
        <tr>
            <td><code>INFLUXDB_ORG</code></td>
            <td>no</td>
            <td></td>
        </tr>
        <tr>
            <td><code>INFLUXDB_TOKEN</code></td>
            <td>no</td>
            <td></td>
        </tr>
        <tr>
            <td><code>INFLUXDB_URL</code></td>
            <td>no</td>
            <td>e.g. <code>http://influxdb.backup-monitoring.svc:8086</code></td>
        </tr>
        <tr>
            <td><code>LOKI_URL</code></td>
            <td>no</td>
            <td>e.g. <code>http://loki-dev.backup-monitoring.svc:3100</code></td>
        </tr>
        <tr>
            <td><code>METRICS_LABELS</code></td>
            <td>no</td>
            <td>e.g. <code>{"namespace":"foo","service":"bar"}</code></td>
        </tr>
        <tr>
            <td><code>S3_METRICS_TIMEOUT</code></td>
            <td>no</td>
            <td>default: <code>5s</code></td>
        </tr>
        <tr>
            <td colspan="3">
                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;
                hook scripts:</td>
        </tr>
        <tr>
            <td><code>PRE_BACKUP_SCRIPT</code></td>
            <td>no</td>
            <td>script to run before backup</td>
        </tr>
        <tr>
            <td><code>POST_BACKUP_SCRIPT</code></td>
            <td>no</td>
            <td>script to run after backup</td>
        </tr>
        <tr>
            <td><code>PRE_RESTORE_SCRIPT</code></td>
            <td>no</td>
            <td>script to run before restore</td>
        </tr>
        <tr>
            <td><code>POST_RESTORE_SCRIPT</code></td>
            <td>no</td>
            <td>script to run after restore</td>
        </tr>
        <tr>
            <td><code>PIPE_IN_SCRIPT</code></td>
            <td>no</td>
            <td>script that dumps data to <code>stdout</code></td>
        </tr>
        <tr>
            <td><code>PIPE_OUT_SCRIPT</code></td>
            <td>no</td>
            <td>script that reads data from <code>stdin</code></td>
        </tr>
        <tr>
            <td colspan="3">
                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;
                restic:</td>
        </tr>
        <tr>
            <td><code>RESTIC_REPOSITORY</code>
            <td rowspan="3">yes</td>
            <td>e.g. "<code>s3:s3.example.com/bucket-name</code>"</td>
        </tr>
        <tr>
            <td colspan="3">
                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;
                &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;<i>or...</i></td>
        </tr>
        <tr>
            <td><code>RESTIC_REPOSITORY_BASE</code><br>
                <code>RESTIC_REPOSITORY_PATH</code>
            </td>
            <td><code>RESTIC_REPOSITORY</code> = <code>RESTIC_REPOSITORY_BASE</code>
                + <code>/</code> + <code>RESTIC_REPOSITORY_PATH</code></td>
        </tr>
        <tr>
            <td><code>RESTIC_PASSWORD</code></td>
            <td>yes</td>
            <td>Password for backup encryption</td>
        </tr>
    </tbody>
</table>