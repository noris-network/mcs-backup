# mcs-backup with the MariaDB client for dump and load scripts.
FROM registry.example.com/mcs-backup:v2.0.0
RUN apk add --no-cache mariadb-client
