-- Deleting a venue must not be blocked by old sessions that used it. Sessions
-- keep their own anchor snapshot, so only the reference is cleared (not org_id).
ALTER TABLE attendance_sessions
    DROP CONSTRAINT attendance_sessions_org_id_venue_id_fkey,
    ADD CONSTRAINT attendance_sessions_org_id_venue_id_fkey
        FOREIGN KEY (org_id, venue_id) REFERENCES venues (org_id, id) ON DELETE SET NULL (venue_id);

-- Pending-invite listing and expiry sweeps.
CREATE INDEX org_invites_open_idx ON org_invites (org_id, expires_at) WHERE used_at IS NULL;
