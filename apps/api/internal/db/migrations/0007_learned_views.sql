-- Rooms learned from use: when an attendee's strict check-in is verified beyond
-- doubt (current code in the photo, GPS inside, room clearly recognised), the
-- photo's room features are kept as an extra reference for the place, so its
-- recognition follows new seats, light and furniture. Never the photo itself:
-- keypoint positions and descriptors only, with the screen masked out.
CREATE TABLE learned_views (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL,
    venue_id   uuid NOT NULL,
    session_id uuid,                          -- the session it was learned in (each teaches a few views)
    features   bytea NOT NULL CHECK (length(features) BETWEEN 17 AND 700000),
    sha256     bytea NOT NULL CHECK (length(sha256) = 32),
    score      int NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    FOREIGN KEY (org_id, venue_id) REFERENCES venues (org_id, id) ON DELETE CASCADE
);
CREATE INDEX learned_views_venue_idx ON learned_views (org_id, venue_id, created_at DESC);
CREATE INDEX learned_views_expiry_idx ON learned_views (expires_at);
