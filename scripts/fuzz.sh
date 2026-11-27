#!/bin/sh
# Runs every fuzz test of the module for a while.
#
#   scripts/fuzz.sh [time per test, default 30s]
#
# An input that fails is saved under testdata/fuzz of its package: commit it,
# so that the normal test run keeps checking it.
set -eu
fuzztime=${1:-30s}
cd "$(dirname "$0")/.."

status=0
for pkg in $(go list ./...); do
	for test in $(go test -list '^Fuzz' "$pkg" | grep '^Fuzz' || true); do
		echo "== $pkg $test"
		go test -run '^$' -fuzz "^$test\$" -fuzztime "$fuzztime" "$pkg" || status=1
	done
done
exit $status
