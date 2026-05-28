#!/usr/bin/env bash

env

: "${MYSQL_HOST:?MYSQL_HOST is required}"
: "${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD is required}"
: "${MYSQL_USER:?MYSQL_USER is required}"
: "${MYSQL_PASSWORD:?MYSQL_PASSWORD is required}"
: "${MYSQL_DATABASE:?MYSQL_DATABASE is required}"

echo "Waiting for MySQL at ${MYSQL_HOST}..."
until mysqladmin ping -h "$MYSQL_HOST" -uroot -p"$MYSQL_ROOT_PASSWORD" --silent; do
    sleep 2
done

echo "Running catalogue database setup..."
mysql -h "$MYSQL_HOST" -uroot -p"$MYSQL_ROOT_PASSWORD" < /db/schema.sql
mysql -h "$MYSQL_HOST" -uroot -p"$MYSQL_ROOT_PASSWORD" < /db/app-user.sql
mysql -h "$MYSQL_HOST" -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE" < /db/master-data.sql
echo "Catalogue database setup complete"

