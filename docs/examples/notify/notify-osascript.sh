#!/bin/sh
# NOTIFY_COMMAND for macOS, to be used with a LaunchAgent (see
# ../launchd/de.example.mcs-backup.agent.plist). A LaunchDaemon cannot show
# notifications.
#
# The values are handed to AppleScript as arguments and never become part of
# the script text, so a message with quotes cannot break or inject anything.
exec osascript \
	-e 'on run argv' \
	-e 'display notification (item 2 of argv) with title "mcs-backup" subtitle (item 1 of argv)' \
	-e 'end run' \
	"$MCS_BACKUP_EVENT" "${MCS_BACKUP_MESSAGE}${MCS_BACKUP_ERROR:+ ($MCS_BACKUP_ERROR)}"
