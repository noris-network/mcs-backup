#!/bin/sh
# VERIFY_SCRIPT for the pipe mode (the dump arrives on stdin, nothing is
# stored). mysqldump and mariadb-dump end a complete dump with this line; a
# truncated dump does not have it.
tail -n 1 | grep -q -- '-- Dump completed'
