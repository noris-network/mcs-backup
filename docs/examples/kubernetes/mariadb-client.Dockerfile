# mcs-backup with the MariaDB client for dump and load scripts.
FROM registry.example.com/mcs-backup:2.0.0
RUN apk add --no-cache mariadb-client
