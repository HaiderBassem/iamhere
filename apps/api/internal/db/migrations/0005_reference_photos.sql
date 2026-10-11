-- Reference photos for strict mode's photo check, replacing the brightness
-- fingerprints of 0004 (those cannot recognise a room from another seat, and
-- cannot be converted: places need their photos taken again).
--
-- A reference belongs either to a saved place (reused by every session there)
-- or to one session ("today's photo", taken when the session starts). The image
-- is stored so references can be re-processed when the matcher improves and so
-- organizers can see what they enrolled. These are photos of rooms taken by the
-- organization itself; attendees' photos are never stored.
DROP TABLE venue_fingerprints;

CREATE TABLE reference_photos (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    venue_id    uuid,
    session_id  uuid,
    image       bytea NOT NULL CHECK (length(image) BETWEEN 1 AND 2000000),
    sha256      bytea NOT NULL CHECK (length(sha256) = 32),
    width       int NOT NULL CHECK (width > 0),
    height      int NOT NULL CHECK (height > 0),
    created_by  uuid NOT NULL REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK ((venue_id IS NULL) <> (session_id IS NULL)),
    FOREIGN KEY (org_id, venue_id) REFERENCES venues (org_id, id) ON DELETE CASCADE,
    FOREIGN KEY (org_id, session_id) REFERENCES attendance_sessions (org_id, id) ON DELETE CASCADE
);
CREATE INDEX reference_photos_venue_idx ON reference_photos (org_id, venue_id, created_at) WHERE venue_id IS NOT NULL;
CREATE INDEX reference_photos_session_idx ON reference_photos (org_id, session_id, created_at) WHERE session_id IS NOT NULL;
