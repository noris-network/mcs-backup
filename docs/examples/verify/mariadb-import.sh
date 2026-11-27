#!/bin/sh
# VERIFY_SCRIPT for the pipe mode: load the dump into a scratch server.
#
# VERIFY_DB_HOST must be a disposable MariaDB server and NEVER the production
# database: a dump of all databases creates and fills the databases of the
# original by name. The script drops what it created again. Such a dump also
# holds the mysql system database: the scratch server takes over the users and
# passwords of the original once it reloads its privileges (at the latest on a
# restart), so VERIFY_DB_USER and VERIFY_DB_PASSWORD must then be an account of
# the original.
set -eu
: "${VERIFY_DB_HOST:?set VERIFY_DB_HOST to a scratch server}"
: "${VERIFY_DB_USER:=root}"
export MYSQL_PWD=${VERIFY_DB_PASSWORD:-}

sql() { mariadb -h "$VERIFY_DB_HOST" -u "$VERIFY_DB_USER" "$@"; }

before=$(sql -N -e 'SHOW DATABASES' | sort)
sql # the dump arrives on stdin

# something must have been loaded
after=$(sql -N -e 'SHOW DATABASES' | sort)
created=$(printf '%s\n%s\n' "$before" "$after" | sort | uniq -u)
[ -n "$created" ] || {
	echo "the dump created no database" >&2
	exit 1
}

# clean up what the dump created
printf '%s\n' "$created" | while read -r db; do
	sql -e "DROP DATABASE \`$db\`"
done
