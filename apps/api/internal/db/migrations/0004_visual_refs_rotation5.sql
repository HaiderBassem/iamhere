-- QR codes now rotate every 5 seconds by default (the allowed range is unchanged).
-- Organizations still on the previous default of 10 move to the new one;
-- deliberately customised values are left alone. Running sessions keep the
-- rotation they were created with (it is a per-session snapshot).
ALTER TABLE organization_settings ALTER COLUMN qr_rotation_s SET DEFAULT 5;
UPDATE organization_settings SET qr_rotation_s = 5 WHERE qr_rotation_s = 10;

-- Visual presence check: perceptual fingerprints of reference photos of a venue.
-- Photos themselves are never stored, only the 64-byte fingerprint.
CREATE TABLE venue_fingerprints (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL,
    venue_id    uuid NOT NULL,
    fingerprint bytea NOT NULL CHECK (length(fingerprint) = 64),
    created_by  uuid NOT NULL REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (org_id, venue_id) REFERENCES venues (org_id, id) ON DELETE CASCADE
);
CREATE INDEX venue_fingerprints_venue_idx ON venue_fingerprints (org_id, venue_id);
