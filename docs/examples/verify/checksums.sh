#!/bin/sh
# VERIFY_SCRIPT for the file mode with VERIFY_MODE=full: the restored copy has
# to match the checksums that the pre-backup hook wrote into the backup.
#
# PRE_BACKUP_SCRIPT (runs in BACKUP_ROOT):
#   find . -type f ! -name checksums.txt -exec sha256sum {} + > checksums.txt
#
# With the default sample mode only some files are restored, so the list
# would not match; restic itself already compares every restored file with
# its stored checksum (--verify).
set -eu
cd "$MCS_BACKUP_VERIFY_DIR"
[ -f checksums.txt ] || {
	echo "checksums.txt is not in the snapshot" >&2
	exit 1
}
sha256sum -c --quiet checksums.txt
