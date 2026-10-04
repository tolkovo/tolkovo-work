#!/bin/sh
# Runs once on an empty data volume. Creates the LOGIN roles the server uses.
# The group roles are created here so the logins can join them; migrations
# (00001_tenancy.sql, 00002_jobs_outbox.sql) skip roles that already exist.
# Neither role owns tables, so RLS applies to both (docs/adr/0003).
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<SQL
CREATE ROLE tolkovo_app NOLOGIN;
CREATE ROLE ${APP_DB_USER} LOGIN PASSWORD '${APP_DB_PASSWORD}' IN ROLE tolkovo_app;
CREATE ROLE tolkovo_system NOLOGIN;
CREATE ROLE ${SYSTEM_DB_USER} LOGIN PASSWORD '${SYSTEM_DB_PASSWORD}' IN ROLE tolkovo_system;
SQL
