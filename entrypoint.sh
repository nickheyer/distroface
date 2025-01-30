#!/bin/sh
set -e

mkdir -p /data/db

if [ ! -f /data/db/registry.db ]; then
    echo "Initializing database..."
    sqlite3 /data/db/registry.db < /app/db/schema.sql
    sqlite3 /data/db/registry.db < /app/db/initdb.sql
    chmod 664 /data/db/registry.db
fi

exec /app/distroface "$@"