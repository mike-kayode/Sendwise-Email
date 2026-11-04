#!/bin/sh
# Runs once when the Postgres data directory is initialized. Each service owns
# its own database and runtime role; the legacy monolith keeps the "email" DB.
set -eu

psql --host "${PGHOST:-/var/run/postgresql}" \
  --dbname "${POSTGRES_DB:-postgres}" \
  -v ON_ERROR_STOP=1 \
  -v identity_user="$IDENTITY_DB_USER" \
  -v identity_password="$IDENTITY_DB_PASSWORD" \
  -v workspace_user="$WORKSPACE_DB_USER" \
  -v workspace_password="$WORKSPACE_DB_PASSWORD" \
  --username "$POSTGRES_USER" <<-'EOSQL'
    SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'identity_user', :'identity_password')
    WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'identity_user')\gexec
    ALTER ROLE :"identity_user" LOGIN PASSWORD :'identity_password';

    SELECT format('CREATE DATABASE %I OWNER %I', 'identity', :'identity_user')
    WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'identity')\gexec
    ALTER DATABASE identity OWNER TO :"identity_user";

    SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'workspace_user', :'workspace_password')
    WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'workspace_user')\gexec
    ALTER ROLE :"workspace_user" LOGIN PASSWORD :'workspace_password';

    SELECT format('CREATE DATABASE %I OWNER %I', 'workspace', :'workspace_user')
    WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'workspace')\gexec
    ALTER DATABASE workspace OWNER TO :"workspace_user";
EOSQL
