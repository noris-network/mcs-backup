#!/bin/sh
# Smoke test of the container image, as the docs promise it: it runs with any
# UID and a read-only root file system, writing only to /tmp; the service
# creates the repository and makes a backup; restore-if-empty brings the data
# back into an empty volume. The MariaDB example image is built on top of it.
#
#   scripts/image-smoke.sh IMAGE
#
# DOCKER names the command (default docker, for example DOCKER="sudo -n docker").
set -eu
image=${1:?usage: scripts/image-smoke.sh IMAGE}
DOCKER=${DOCKER:-docker}
cd "$(dirname "$0")/.."

name=mcs-smoke-$$
uid=12345
cleanup() {
	$DOCKER rm -f "$name" >/dev/null 2>&1 || true
	$DOCKER volume rm "$name-repo" "$name-data" "$name-empty" >/dev/null 2>&1 || true
	$DOCKER rmi "$name-mariadb" >/dev/null 2>&1 || true
}
trap cleanup EXIT
fail() {
	echo "FAIL: $*" >&2
	$DOCKER logs "$name" 2>&1 | tail -20 >&2 || true
	exit 1
}

echo "== versions"
$DOCKER run --rm "$image" mcs-backup version
$DOCKER run --rm "$image" restic version

echo "== volumes for UID $uid"
for v in repo data empty; do $DOCKER volume create "$name-$v" >/dev/null; done
$DOCKER run --rm -v "$name-repo:/repo" -v "$name-data:/data" -v "$name-empty:/empty" "$image" \
	sh -c "mkdir -p /data/app && echo smoke >/data/app/file.txt && chown -R $uid:$uid /repo /data /empty"

# as in the Kubernetes example: any UID, read-only root, /tmp writable
run_as_user() {
	$DOCKER run --read-only --user "$uid:$uid" --tmpfs /tmp:rw,mode=1777 \
		-e RESTIC_REPOSITORY=/repo -e RESTIC_PASSWORD=smoke-test-password -e BACKUP_ROOT=/data "$@"
}

echo "== config check"
run_as_user --rm -v "$name-repo:/repo" -v "$name-data:/data" "$image" mcs-backup config check >/dev/null

echo "== serve and backup"
run_as_user -d --name "$name" -v "$name-repo:/repo" -v "$name-data:/data" "$image" >/dev/null
ready=no
for _ in $(seq 60); do
	if $DOCKER exec "$name" mcs-backup status --json 2>/dev/null | grep -Eq '"ready": *true'; then
		ready=yes
		break
	fi
	sleep 1
done
[ "$ready" = yes ] || fail "the service did not get ready"
$DOCKER exec "$name" mcs-backup backup --note smoke || fail "the backup failed"
$DOCKER exec "$name" mcs-backup snapshots --json | grep -q '"smoke"' || fail "the snapshot is missing"

echo "== stop with SIGTERM"
$DOCKER stop "$name" >/dev/null
code=$($DOCKER inspect -f '{{.State.ExitCode}}' "$name")
[ "$code" = 0 ] || fail "serve ended with exit code $code after SIGTERM"

echo "== restore-if-empty into an empty volume"
run_as_user --rm -v "$name-repo:/repo" -v "$name-empty:/data" "$image" mcs-backup restore-if-empty
got=$($DOCKER run --rm -v "$name-empty:/data" "$image" cat /data/app/file.txt)
[ "$got" = smoke ] || fail "restore-if-empty restored \"$got\""

echo "== MariaDB example image"
sed "s#^FROM .*#FROM $image#" docs/examples/kubernetes/mariadb-client.Dockerfile |
	$DOCKER build -q -t "$name-mariadb" - >/dev/null
$DOCKER run --rm "$name-mariadb" mariadb-dump --version
$DOCKER run --rm "$name-mariadb" mariadb --version

echo "image smoke test passed"
