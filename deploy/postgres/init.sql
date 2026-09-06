-- Idempotent local Postgres bootstrap. Runs once on an empty data volume via
-- docker-entrypoint-initdb.d; `make seed` re-applies it against a live db.
-- Prod schemas/roles are managed by migrations, not this file.

CREATE SCHEMA IF NOT EXISTS air;

-- A least-privilege application role the broker can hand out in place of the
-- superuser. Password is a local-dev placeholder only.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'air_app') THEN
        CREATE ROLE air_app LOGIN PASSWORD 'localdevpass';
    END IF;
END
$$;

GRANT USAGE ON SCHEMA air TO air_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA air
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO air_app;

-- Smoke table so consumers have something to read against on first boot.
CREATE TABLE IF NOT EXISTS air.healthcheck (
    id         serial PRIMARY KEY,
    checked_at timestamptz NOT NULL DEFAULT now()
);
