#!/bin/sh
# NOTIFY_COMMAND that posts to a web service. Set NOTIFY_WEBHOOK_URL next to it;
# its value is masked in the log like a password.
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
# healthchecks.io: ping when a snapshot was saved, signal a failure otherwise.
# The first success after a failure is backup.recovered, not backup.succeeded;
# a backup.warning saved its snapshot too (files restic could not read, a
# retention that waits for approval, a failed copy, ...):
#   case $MCS_BACKUP_EVENT in
#   backup.succeeded | backup.recovered | backup.warning) exec curl -fsS -m 20 "$NOTIFY_WEBHOOK_URL" ;;
#   backup.failed | backup.suspicious) exec curl -fsS -m 20 "$NOTIFY_WEBHOOK_URL/fail" ;;
#   esac
#   (with NOTIFY_WEBHOOK_URL=https://hc-ping.com/<uuid>, NOTIFY_ON=all)
