# mcs-backup

A simple backup service around [restic][restic], made for Kubernetes
(as a sidecar or stand-alone) but just as happy on a Linux or macOS host.

mcs-backup schedules the backups, keeps the retention, checks that the backups
are sane and restorable, and makes the state visible: through metrics, a
terminal UI, an optional web UI and, for AI agents, an MCP server. It adds as
little as possible to restic and takes care of the things that go wrong quietly.

* **Any restic backend**: S3, rest-server, SFTP, B2, Azure, GCS, local paths.
  Encrypted, deduplicated, incremental (restic).
* **Files or a stream**: back up a directory, or pipe a database dump straight
  into the repository with hooks.
* **Safe by default**: no empty snapshots, a much smaller backup is flagged
  instead of replacing good ones, a retention that would delete too much waits
  for approval, restores take a safety snapshot first, and a weekly restore test
  proves that the backups can be read back.
* **No false alarms after a restart**: the metrics are filled from the repository
  within seconds of a start.
* **Optional second copy** to another repository, and append-only repositories.
* **Operable by people who do not know it**: plain-language status, a TUI,
  confirmation for everything that destroys data.

## Quick start

With the container image (restic is included):

```console
docker run -d --name backup \
  -v /srv/data:/mnt \
  -e RESTIC_REPOSITORY=s3:https://s3.example.com/backups/data \
  -e RESTIC_PASSWORD=change-me \
  -e AWS_ACCESS_KEY_ID=… -e AWS_SECRET_ACCESS_KEY=… \
  -e CRON_SCHEDULE='15 2 * * *' \
  <image>

docker exec backup mcs-backup status
docker exec backup mcs-backup backup --note "first backup"
docker exec -it backup mcs-backup tui
```

On a host, install restic (0.17 or newer) and the `mcs-backup` binary, see
[platforms](docs/platforms.md). In Kubernetes see the
[sidecar example](docs/examples/kubernetes/sidecar.yaml).

At least a repository, a password and a retention policy are needed (the image
sets a default policy). `mcs-backup config check` tells what is missing or wrong
before the service starts; every setting is described in
[configuration](docs/configuration.md).

## How it works

`mcs-backup serve` is the service. It runs one operation at a time (backup,
restore, prune, check, restore test, forget), serves `/metrics`, `/healthz` and
`/readyz` on port 9000, and listens on a Unix socket. Everything else is a
client of that socket: the command line, the TUI, the web UI and the MCP server.
A second request while an operation runs is refused with the name of the running
one.

```
                       ┌──────────────────────────────┐
 mcs-backup status ───▶│                              │──▶ restic ──▶ repository
 mcs-backup tui    ───▶│ mcs-backup serve             │
 mcs-backup mcp    ───▶│  (schedule, one operation    │──▶ /metrics /healthz /readyz
 web UI (optional) ───▶│   at a time, state, logs)    │
                       └──────────────────────────────┘
```

## Day-to-day commands

| Command | Does |
| --- | --- |
| `status [--follow]` | What is going on, what needs attention, in words. |
| `backup [--note …] [--keep]` | Start a backup now. `--keep` protects the snapshot from the retention. |
| `backup --maintenance 1h` / `--disable` / `--enable` | Pause or resume the schedule. A maintenance window survives restarts; a plain `--disable` ends with a restart. |
| `snapshots` | The snapshots, newest first, with flags. |
| `ls [<snapshot> [<path>]]` | What a snapshot holds in a directory. |
| `find <pattern> [--snapshot …] [-i]` | Which snapshots have a file, and in which versions: the way to the snapshot to restore a lost file from. |
| `diff [<snapshot> [<older>]]` | What a snapshot lost, gained and changed. Without arguments: what the newest backup lost against the last regular one. |
| `restore [<snapshot>] [--target dir] [--include path] [--merge]` | Restore. Without `--target` it replaces the data in place, asks for confirmation and takes a safety snapshot first. |
| `restore-if-empty` | Disaster recovery for init containers: restores only into an empty `BACKUP_ROOT`, and does not contact the repository otherwise. |
| `verify [--full]` | The restore test. Runs weekly by default. |
| `keep` / `unkeep <snapshot>` | Protect a snapshot from the retention or release it. |
| `acknowledge <snapshot>` | Accept a backup that was flagged as suspiciously small. |
| `forget` / `approve-forget` | Show what the retention would delete; release a held forget. |
| `locks`, `unlock [--all]` | Look at and remove repository locks. |
| `log [kind]` | The log of the latest run. |
| `env` | The variables to run restic against this repository by hand. |
| `restic …` | restic with the environment of this instance. |
| `info`, `config check` | Check the configuration and the access to the repositories. |
| `tui`, `web`, `mcp` | The other user interfaces, see below. |

All commands accept `--json`. Exit codes: 0 ok, 1 failed, 2 usage or
configuration error (including a missing confirmation), 3 the service is not
reachable, 4 another operation is running.

## What it protects you from

* **Empty backups.** An empty source never becomes a snapshot: it fails when
  there are earlier snapshots (a volume that came up empty), and is skipped on a
  fresh installation.
* **Broken backups replacing good ones.** A backup far smaller than the last one
  (`BACKUP_MAX_SHRINK`) is stored but tagged `suspicious`, and the retention
  stops until a person accepts it.
* **Typos in the retention.** A forget that would delete a quarter of the
  snapshots waits for `approve-forget`. The retention touches only the snapshots
  of its own instance, so repositories can be shared safely.
* **Unwanted restores.** In-place restores need a typed confirmation and a safety
  snapshot (tag `pre-restore`) of the current data first.
* **Silent failures.** The post-hook always runs and learns the outcome; missing
  hook scripts stop the start; failed runs are retried; notifications and
  metrics report what happened.
* **Backups nobody can restore.** The restore test restores a sample of the
  latest snapshot every week and can run your own check on it.

## Terminal UI

`mcs-backup tui` shows overview, snapshots, locks and restic access, with the
running operation and its progress, and everything the command line can do:
start and cancel operations, restore with a dialog that shows the target, the
free space and the hooks, keep snapshots, handle locks and the schedule. Press
`?` for the keys.

To bring back a single file, press `o` in the snapshots tab: it browses the
files of the snapshot, `/` finds a file in all snapshots and `r` restores the
selected entry. The web UI has the same on its "Files" page.

## Web UI

Optional and off by default. The web UI shows the same as the TUI and is meant
for people who do not work in a terminal.

```console
mcs-backup web                    # switches it on if needed, opens the browser
mcs-backup web enable --for 2h    # on for two hours
mcs-backup web disable
```

`MCS_BACKUP_WEB_ADDR=127.0.0.1:8484` has it on from the start. The login is
always required, also on localhost: `mcs-backup web` fetches a **one-time link**
that is valid for two minutes and logs the browser in with a session cookie, so
no permanent secret ends up in a URL or a history. In a pod or over SSH the
link is printed together with the `kubectl port-forward` command that makes the
port reachable. Behind an ingress you can set `MCS_BACKUP_WEB_TOKEN` for
HTTP Basic Auth instead.

The web UI speaks plain HTTP. Outside of localhost put it behind a proxy that
terminates TLS. Cross-origin requests are refused, pages are served with a strict
Content-Security-Policy, and nothing is loaded from the internet.

## For AI agents

`mcs-backup mcp` serves the [Model Context Protocol][mcp] on stdin/stdout so
that an agent can look at the backups and, if you allow it, operate them:

```console
kubectl exec -i <pod> -c mcs-backup -- mcs-backup mcp                      # look only
kubectl exec -i <pod> -c mcs-backup -- mcs-backup mcp --allow-write        # also start, tag, schedule
kubectl exec -i <pod> -c mcs-backup -- mcs-backup mcp --allow-destructive  # also restore over live data
```

Tools above the chosen level do not exist for the agent; secrets are never
returned. See [agents](docs/agents.md).

## Monitoring

Prometheus metrics with the prefix `mcsbackup_` are on `:9000/metrics`.
[Ready-made alerts, a ServiceMonitor and a Grafana dashboard](docs/examples/monitoring/)
are included. Logs are JSON on stdout (`MCS_BACKUP_LOG_FORMAT=text` for people).
For chat, desktop or e-mail notifications, `NOTIFY_COMMAND` runs a command of
yours for the events you select, see [hooks](docs/hooks.md#notifications).

## Documentation

* [Configuration](docs/configuration.md): every setting with its default
* [Hooks, pipes and notifications](docs/hooks.md)
* [Linux and macOS hosts](docs/platforms.md)
* [Append-only repositories](docs/append-only.md)
* [AI agents and scripting](docs/agents.md)
* [Migrating from 1.x](docs/migration-1x.md)
* [Examples](docs/examples/): Kubernetes, monitoring, systemd, launchd, notifications, restore tests

## Requirements and limits

* restic 0.17 or newer (the image has 0.19.1).
* Linux and macOS, amd64 and arm64. The container image is Linux only. Windows is
  not supported.
* restic cannot store an empty snapshot; `ALLOW_EMPTY_SNAPSHOT` only keeps such a
  run from failing.
* The macOS binaries are neither signed nor notarized, see [platforms](docs/platforms.md#macos).

## Development

```console
task build            # or: go build ./cmd/mcs-backup
task test             # unit tests
task lint             # vet, go fix check, golangci-lint v2
RESTIC_BIN=$(command -v restic) REST_SERVER_BIN=$(command -v rest-server) task integration
```

The integration tests run the real binary against real restic repositories
(local and, with `REST_SERVER_BIN`, an append-only rest-server). Go 1.27 or newer
is required.

Tests against an S3 server (`MCS_TEST_S3_ENDPOINT`, `MCS_TEST_S3_ACCESS_KEY`,
`MCS_TEST_S3_SECRET_KEY`, optional `MCS_TEST_S3_BUCKET`) exercise the TUI and the web UI
with real data. `MCS_TEST_S3_DEMO=1 go test -tags integration -run TestS3Demo ./internal/integration/`
prepares a repository and a `local/s3-demo/mcs-backup` wrapper to try both by hand.

[restic]: https://github.com/restic/restic
[mcp]: https://modelcontextprotocol.io
