-- Registered venues: let a title-only session bind to a known physical place.
CREATE TABLE venues (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 200),
    center     geography(Point,4326) NOT NULL,
    radius_m   int NOT NULL DEFAULT 30 CHECK (radius_m BETWEEN 5 AND 1000),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, id)
);
CREATE INDEX venues_center_gix ON venues USING gist (center);
CREATE INDEX venues_org_idx ON venues (org_id);

ALTER TABLE attendance_sessions
    ADD COLUMN venue_id uuid,
    ADD COLUMN location_source text NOT NULL DEFAULT 'none'
        CHECK (location_source IN ('none','organizer','venue','sole_venue')),
    ADD FOREIGN KEY (org_id, venue_id) REFERENCES venues (org_id, id);

-- Single-use invitation links. Only a hash of the secret is stored.
CREATE TABLE org_invites (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    role       org_role NOT NULL CHECK (role <> 'owner'),
    token_hash bytea NOT NULL UNIQUE,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    used_by    uuid REFERENCES users(id)
);
CREATE INDEX org_invites_org_idx ON org_invites (org_id, created_at DESC);
