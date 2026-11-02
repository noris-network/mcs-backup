#!/bin/sh
# NOTIFY_COMMAND that posts to a web service. Set NOTIFY_WEBHOOK_URL next to it.
#
# ntfy / Gotify style (plain text body, title in a header):
set -eu
: "${NOTIFY_WEBHOOK_URL:?set NOTIFY_WEBHOOK_URL}"

priority=default
case $MCS_BACKUP_EVENT in
*.failed | backup.suspicious | repository.down) priority=high ;;
esac

exec curl -fsS -m 20 \
	-H "Title: mcs-backup $MCS_BACKUP_HOST: $MCS_BACKUP_EVENT" \
	-H "Priority: $priority" \
	-d "${MCS_BACKUP_MESSAGE}${MCS_BACKUP_ERROR:+ ($MCS_BACKUP_ERROR)}" \
	"$NOTIFY_WEBHOOK_URL"

# Other services:
#
# Forward the event JSON unchanged (Teams/Slack workflows, own endpoints):
#   exec curl -fsS -m 20 -H 'Content-Type: application/json' --data-binary @- "$NOTIFY_WEBHOOK_URL"
#
# healthchecks.io: ping on success, signal a failure otherwise:
#   case $MCS_BACKUP_EVENT in
#   backup.succeeded) exec curl -fsS -m 20 "$HC_URL" ;;
#   backup.failed | backup.suspicious) exec curl -fsS -m 20 "$HC_URL/fail" ;;
#   esac
#   (with NOTIFY_ON=failure,warning,success)
