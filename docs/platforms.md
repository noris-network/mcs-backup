# Running on Linux and macOS hosts

mcs-backup runs as well on a server or a workstation as in a container. The
binary is available for Linux and macOS, on amd64 and arm64; Windows is not
supported. restic is **not** part of the archives: install version 0.17 or newer
(`apt install restic`, `brew install restic`, or from the
[releases][restic-releases]) and make sure it is in the `PATH` of the service
(or set `MCS_BACKUP_RESTIC`).

Download the archive for your platform from the releases page, verify it against
`checksums.txt`, and put `mcs-backup` into `/usr/local/bin`.

## Linux

Two unit files are in [examples/systemd/](examples/systemd/):

* [`mcs-backup.service`](examples/systemd/mcs-backup.service) for a server. It
  runs as root so that it can read everything, with a hardened sandbox, a state
  directory for maintenance windows, a cache directory and the socket under
  `/run/mcs-backup/`.
* [`mcs-backup.user.service`](examples/systemd/mcs-backup.user.service) for a
  workstation: runs in your user session, can show desktop notifications.

The CLI finds the service through `MCS_BACKUP_SOCKET` (or `--socket`); export the
same value in your shell as in the unit.

To restore a lost data directory before an application starts, run
`mcs-backup restore-if-empty` in an `ExecStartPre=` of the application's unit.

## macOS

Two property lists are in [examples/launchd/](examples/launchd/):

* a **LaunchAgent** for a Mac that somebody uses: runs while the user is logged
  in and can show notifications with `osascript`,
* a **LaunchDaemon** for a Mac that runs unattended. It cannot show desktop
  notifications; use a webhook.

Things that are specific to macOS:

* **Full Disk Access.** macOS protects folders such as Desktop, Documents,
  Downloads and Mail. restic needs access to them or the backup ends with
  `Operation not permitted` for those paths: *System Settings → Privacy &
  Security → Full Disk Access →* add the `restic` binary (`command -v restic`).
  After a Homebrew update of restic the entry may have to be added again.
* **Gatekeeper.** The binaries are not signed or notarized. A file downloaded with
  a browser carries a quarantine flag and will not start; remove it once:
  `xattr -d com.apple.quarantine /usr/local/bin/mcs-backup`. Downloads with
  `curl` or `brew` are not affected.
* **Sleep.** A Mac that sleeps misses its backup time. With `BACKUP_CATCH_UP`
  (default on) one backup is made after wake-up if the last snapshot is older
  than the last scheduled time.
* **The socket path** is limited to about 100 characters, and `$TMPDIR` differs
  between users and daemons. The examples set `MCS_BACKUP_SOCKET` explicitly.
* **TUI.** `mcs-backup tui` never writes to a clipboard, which many terminals (the
  console of a browser, for example) do not allow. Where something is meant to be
  copied, the restic environment on the Access tab (`y`) and the web UI link,
  it is shown as plain text without a frame, to be selected with the mouse.

## Notifications on a host

Set `NOTIFY_COMMAND` to one of the scripts in [examples/notify/](examples/notify/):
`notify-send.sh` (Linux desktop), `notify-osascript.sh` (macOS) or
`notify-webhook.sh` (ntfy, Gotify, anything that takes an HTTP POST).
See [hooks.md](hooks.md#notifications) for the events.

## Restore test

The weekly restore test (`VERIFY_SCHEDULE`) uses `VERIFY_DIR`, by default the
temporary directory, as scratch space. It must not be inside `BACKUP_ROOT`. If
that directory is a small RAM disk (`/tmp` as tmpfs), point `VERIFY_DIR` to a
disk or reduce `VERIFY_SAMPLE_BYTES`; the test is skipped with a warning when
the space is not enough. Ready-made `VERIFY_SCRIPT`s are in
[examples/verify/](examples/verify/).

[restic-releases]: https://github.com/restic/restic/releases
