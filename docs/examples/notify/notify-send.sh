#!/bin/sh
# NOTIFY_COMMAND for a Linux desktop. Run mcs-backup as a systemd user service
# (see ../systemd/mcs-backup.user.service) so that it can reach the session bus.
#
#   NOTIFY_COMMAND=/usr/local/share/mcs-backup/notify-send.sh
#
# The event arrives in MCS_BACKUP_* variables (and as JSON on stdin).
urgency=normal
case $MCS_BACKUP_EVENT in
*.failed | backup.suspicious | repository.down) urgency=critical ;;
esac
exec notify-send --app-name=mcs-backup --urgency="$urgency" \
	"mcs-backup: $MCS_BACKUP_EVENT" \
	"${MCS_BACKUP_MESSAGE}${MCS_BACKUP_ERROR:+ ($MCS_BACKUP_ERROR)}"
