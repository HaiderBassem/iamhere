CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    idp_issuer    text NOT NULL,
    idp_subject   text NOT NULL,
    full_name     text NOT NULL CHECK (length(btrim(full_name)) BETWEEN 1 AND 200),
    email         text,
    phone_e164    text CHECK (phone_e164 IS NULL OR phone_e164 ~ '^\+[1-9][0-9]{6,14}$'),
    created_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,
    UNIQUE (idp_issuer, idp_subject)
);

CREATE TABLE organizations (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    name        text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    country     text CHECK (country IS NULL OR country ~ '^[A-Z]{2}$'),
    timezone    text NOT NULL DEFAULT 'UTC',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TYPE org_role AS ENUM ('owner','admin','organizer','reviewer','reporter');

CREATE TABLE organization_memberships (
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       org_role NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX ON organization_memberships (user_id);

CREATE TYPE verification_mode AS ENUM ('standard','visual','strict');

CREATE TABLE organization_settings (
    org_id               uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    verification_mode    verification_mode NOT NULL DEFAULT 'standard',
    default_duration_s   int NOT NULL DEFAULT 1800 CHECK (default_duration_s BETWEEN 60 AND 86400),
    gps_radius_m         int NOT NULL DEFAULT 20   CHECK (gps_radius_m BETWEEN 5 AND 5000),
    qr_rotation_s        int NOT NULL DEFAULT 10   CHECK (qr_rotation_s BETWEEN 5 AND 120),
    max_gps_accuracy_m   int NOT NULL DEFAULT 100  CHECK (max_gps_accuracy_m BETWEEN 5 AND 2000)
);

CREATE TYPE session_status AS ENUM ('created','active','expired','closed','cancelled');

CREATE TABLE attendance_sessions (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by         uuid NOT NULL REFERENCES users(id),
    title              text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 200),
    status             session_status NOT NULL DEFAULT 'active',
    starts_at          timestamptz NOT NULL DEFAULT now(),
    ends_at            timestamptz NOT NULL,
    closed_at          timestamptz,
    -- policy snapshot: a mid-session settings change is explicit, never accidental
    verification_mode  verification_mode NOT NULL,
    gps_radius_m       int NOT NULL,
    max_gps_accuracy_m int NOT NULL,
    qr_rotation_s      int NOT NULL,
    qr_secret          bytea NOT NULL,
    -- organizer anchor: organizer device position when the session started
    anchor             geography(Point,4326),
    anchor_accuracy_m  real,
    idempotency_key    text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),
    UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX attendance_sessions_idem ON attendance_sessions (org_id, created_by, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX ON attendance_sessions (org_id, starts_at DESC);

CREATE TYPE attendance_status AS ENUM ('pending','verified','needs_review','rejected','revoked');
CREATE TYPE attendance_method AS ENUM ('qr','manual');

CREATE TABLE attendance_records (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             uuid NOT NULL,
    session_id         uuid NOT NULL,
    user_id            uuid NOT NULL REFERENCES users(id),
    status             attendance_status NOT NULL,
    method             attendance_method NOT NULL DEFAULT 'qr',
    -- historical snapshot: reports must not change when a profile is edited later
    full_name_snapshot text NOT NULL,
    email_snapshot     text,
    phone_snapshot     text,
    evidence           jsonb NOT NULL DEFAULT '{}',
    manual_reason      text,
    manual_by          uuid REFERENCES users(id),
    checked_in_at      timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (org_id, session_id) REFERENCES attendance_sessions (org_id, id) ON DELETE CASCADE,
    UNIQUE (session_id, user_id),
    CHECK (method <> 'manual' OR (manual_reason IS NOT NULL AND manual_by IS NOT NULL))
);
CREATE INDEX ON attendance_records (org_id, session_id, checked_in_at);
CREATE INDEX ON attendance_records (user_id, checked_in_at DESC);

CREATE TABLE verification_attempts (
    id          bigserial PRIMARY KEY,
    org_id      uuid NOT NULL,
    session_id  uuid NOT NULL,
    user_id     uuid NOT NULL,
    outcome     text NOT NULL,
    reasons     text[] NOT NULL DEFAULT '{}',
    evidence    jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON verification_attempts (session_id, created_at);
CREATE INDEX ON verification_attempts (user_id, created_at);

CREATE TABLE audit_logs (
    id         bigserial PRIMARY KEY,
    org_id     uuid,
    actor_id   uuid,
    action     text NOT NULL,
    target     text,
    detail     jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON audit_logs (org_id, created_at DESC);
