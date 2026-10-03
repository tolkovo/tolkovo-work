#!/bin/sh
# Runs once on an empty data volume. Creates the LOGIN role the server uses.
# Membership in tolkovo_app is granted after `tolkovo migrate up` creates that
# role (see 00001_tenancy.sql); the app role never owns tables, so RLS applies.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
CREATE ROLE tolkovo_app NOLOGIN;
CREATE ROLE ${APP_DB_USER} LOGIN PASSWORD '${APP_DB_PASSWORD}' IN ROLE tolkovo_app;
SQL
