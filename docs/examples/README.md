# Examples

| Directory | Content |
| --- | --- |
| [kubernetes/](kubernetes/) | `sidecar.yaml`: application with a `restore-if-empty` init container and the backup sidecar, read-only root file system, any UID. `mariadb-pipe.yaml` (+ `mariadb-client.Dockerfile`): stand-alone backup of a MariaDB through a pipe. |
| [monitoring/](monitoring/) | `prometheusrule.yaml`, `servicemonitor.yaml`, `grafana-dashboard.json`. A test makes sure they only use metrics that exist. |
| [systemd/](systemd/) | Units for a server and for a user session. |
| [launchd/](launchd/) | LaunchAgent and LaunchDaemon for macOS. |
| [notify/](notify/) | `NOTIFY_COMMAND` scripts: `notify-send` (Linux), `osascript` (macOS), webhook (ntfy, healthchecks.io, generic). |
| [verify/](verify/) | `VERIFY_SCRIPT`s: dump completeness, import into a scratch MariaDB, checksums. |

An append-only setup (rest-server, S3 policy) is described in
[../append-only.md](../append-only.md).

Everything uses placeholder names (`example.com`, `change-me`); adapt it to your
environment before use. Scripts are meant to be copied, e.g. to
`/usr/local/share/mcs-backup/`, and to be made executable.
