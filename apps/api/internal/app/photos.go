package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg" // register decoders for DecodeConfig
	_ "image/png"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iamhere/api/internal/vision"
)

// Reference photos for strict mode's photo check. A reference belongs either
// to a saved place (reused by every session held there) or to one session
// ("today's photo", taken when it starts, which matches today's light, layout
// and board best). A session is checked against both.

const (
	maxReferencesPerVenue   = 12
	maxReferencesPerSession = 4
	maxReferenceBytes       = 2_000_000
	// Session photos are only needed while check-ins can still be reviewed.
	sessionPhotoRetention = 30 * 24 * time.Hour
)

type ReferencePhoto struct {
	ID        uuid.UUID `json:"id"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	CreatedAt time.Time `json:"created_at"`
}

var errVisionOff = newErr(503, "photo_check_unavailable", "The photo check is not set up on this server.")

// visionErr maps vision service failures to client-safe errors.
func visionErr(err error) error {
	var pe *vision.PhotoError
	switch {
	case errors.As(err, &pe):
		return newErr(400, "photo_unusable", pe.Message)
	case errors.Is(err, vision.ErrNotConfigured):
		return errVisionOff
	case errors.Is(err, vision.ErrUnavailable):
		return newErr(503, "photo_check_busy", "The photo check is busy. Please try again in a moment.")
	}
	return err
}

// imageSize checks a reference upload is a JPEG or PNG of sane size without
// decoding its pixels (the vision service does the real checks).
func imageSize(b []byte) (int, int, error) {
	if len(b) == 0 || len(b) > maxReferenceBytes {
		return 0, 0, newErr(400, "photo_too_large", "That photo is too large.")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width < 320 || cfg.Height < 240 || cfg.Width*cfg.Height > 24_000_000 {
		return 0, 0, newErr(400, "photo_invalid", "That file is not a usable photo.")
	}
	return cfg.Width, cfg.Height, nil
}

// addReference validates the photo with the vision service, then stores it
// under the given scope with a per-scope cap enforced under a row lock.
func (s *Service) addReference(ctx context.Context, u User, orgID uuid.UUID, venueID, sessionID *uuid.UUID, photo []byte) (ReferencePhoto, error) {
	w, h, err := imageSize(photo)
	if err != nil {
		return ReferencePhoto{}, err
	}
	if _, err := s.vis.CheckReference(ctx, photo); err != nil {
		return ReferencePhoto{}, visionErr(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReferencePhoto{}, err
	}
	defer tx.Rollback(ctx)
	limit, lock, count := maxReferencesPerVenue,
		`SELECT 1 FROM venues WHERE id=$1 AND org_id=$2 FOR UPDATE`,
		`SELECT count(*) FROM reference_photos WHERE venue_id=$1 AND org_id=$2`
	scope := venueID
	if sessionID != nil {
		limit, scope = maxReferencesPerSession, sessionID
		lock = `SELECT 1 FROM attendance_sessions WHERE id=$1 AND org_id=$2 FOR UPDATE`
		count = `SELECT count(*) FROM reference_photos WHERE session_id=$1 AND org_id=$2`
	}
	var one, n int
	if err := tx.QueryRow(ctx, lock, *scope, orgID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReferencePhoto{}, ErrNotFound
		}
		return ReferencePhoto{}, err
	}
	if err := tx.QueryRow(ctx, count, *scope, orgID).Scan(&n); err != nil {
		return ReferencePhoto{}, err
	}
	if n >= limit {
		return ReferencePhoto{}, newErr(409, "reference_limit", "There are already enough photos here. Remove one first.")
	}
	sum := vision.SHA256(photo)
	ref := ReferencePhoto{Width: w, Height: h}
	if err := tx.QueryRow(ctx, `INSERT INTO reference_photos(org_id, venue_id, session_id, image, sha256, width, height, created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, created_at`,
		orgID, venueID, sessionID, photo, sum[:], w, h, u.ID).Scan(&ref.ID, &ref.CreatedAt); err != nil {
		return ReferencePhoto{}, err
	}
	if err := audit(ctx, tx, &orgID, u.ID, "reference.add", ref.ID.String(), map[string]any{"venue": venueID, "session": sessionID}); err != nil {
		return ReferencePhoto{}, err
	}
	return ref, tx.Commit(ctx)
}

func (s *Service) listReferences(ctx context.Context, orgID uuid.UUID, column string, scope uuid.UUID) ([]ReferencePhoto, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, width, height, created_at FROM reference_photos
		WHERE org_id=$1 AND `+column+`=$2 ORDER BY created_at, id`, orgID, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReferencePhoto{}
	for rows.Next() {
		var r ReferencePhoto
		if err := rows.Scan(&r.ID, &r.Width, &r.Height, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) deleteReference(ctx context.Context, u User, orgID uuid.UUID, column string, scope, refID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM reference_photos WHERE id=$1 AND org_id=$2 AND `+column+`=$3`, refID, orgID, scope)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := audit(ctx, tx, &orgID, u.ID, "reference.delete", refID.String(), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- saved places -------------------------------------------------------------

// AddVenueReference saves a photo of a place. Admins and owners decide what
// "this place" looks like.
func (s *Service) AddVenueReference(ctx context.Context, u User, orgID, venueID uuid.UUID, photo []byte) (ReferencePhoto, error) {
	if _, err := s.require(ctx, u, orgID, PermManageOrg); err != nil {
		return ReferencePhoto{}, err
	}
	return s.addReference(ctx, u, orgID, &venueID, nil, photo)
}

func (s *Service) ListVenueReferences(ctx context.Context, u User, orgID, venueID uuid.UUID) ([]ReferencePhoto, error) {
	if _, err := s.require(ctx, u, orgID, PermCreateSession); err != nil {
		return nil, err
	}
	return s.listReferences(ctx, orgID, "venue_id", venueID)
}

func (s *Service) DeleteVenueReference(ctx context.Context, u User, orgID, venueID, refID uuid.UUID) error {
	if _, err := s.require(ctx, u, orgID, PermManageOrg); err != nil {
		return err
	}
	return s.deleteReference(ctx, u, orgID, "venue_id", venueID, refID)
}

// ---- one session ("today's photo") ---------------------------------------------

// sessionEditable allows the session's creator, or an admin/owner, to manage its
// photos while it is running.
func (s *Service) sessionEditable(ctx context.Context, u User, orgID, sid uuid.UUID) error {
	role, err := s.require(ctx, u, orgID, PermCreateSession)
	if err != nil {
		return err
	}
	var creator uuid.UUID
	var status string
	var ends time.Time
	err = s.pool.QueryRow(ctx, `SELECT created_by, status::text, ends_at FROM attendance_sessions WHERE id=$1 AND org_id=$2`, sid, orgID).
		Scan(&creator, &status, &ends)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if creator != u.ID && !role.Can(PermManageOrg) {
		return ErrForbidden
	}
	if status != "active" || !s.Now().Before(ends) {
		return newErr(410, "session_ended", "This attendance session has ended.")
	}
	return nil
}

func (s *Service) AddSessionReference(ctx context.Context, u User, orgID, sid uuid.UUID, photo []byte) (ReferencePhoto, error) {
	if err := s.sessionEditable(ctx, u, orgID, sid); err != nil {
		return ReferencePhoto{}, err
	}
	return s.addReference(ctx, u, orgID, nil, &sid, photo)
}

func (s *Service) ListSessionReferences(ctx context.Context, u User, orgID, sid uuid.UUID) ([]ReferencePhoto, error) {
	if _, err := s.require(ctx, u, orgID, PermViewAttendance); err != nil {
		return nil, err
	}
	return s.listReferences(ctx, orgID, "session_id", sid)
}

func (s *Service) DeleteSessionReference(ctx context.Context, u User, orgID, sid, refID uuid.UUID) error {
	if err := s.sessionEditable(ctx, u, orgID, sid); err != nil {
		return err
	}
	return s.deleteReference(ctx, u, orgID, "session_id", sid, refID)
}

// ReferenceImage returns a reference photo's JPEG/PNG bytes (thumbnails in the
// app). Only people who can start sessions see the organization's room photos.
func (s *Service) ReferenceImage(ctx context.Context, u User, orgID, refID uuid.UUID) ([]byte, error) {
	if _, err := s.require(ctx, u, orgID, PermCreateSession); err != nil {
		return nil, err
	}
	var img []byte
	err := s.pool.QueryRow(ctx, `SELECT image FROM reference_photos WHERE id=$1 AND org_id=$2`, refID, orgID).Scan(&img)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return img, err
}

// ---- matching -------------------------------------------------------------------

// sessionRefs are the references a session's photos are compared with: today's
// photos first (closest to today's conditions), then the place's saved photos.
func (s *Service) sessionRefs(ctx context.Context, orgID, sid uuid.UUID, venueID *uuid.UUID) ([]vision.Ref, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, sha256 FROM reference_photos
		WHERE org_id=$1 AND (session_id=$2 OR ($3::uuid IS NOT NULL AND venue_id=$3))
		ORDER BY (session_id IS NULL), created_at DESC`, orgID, sid, venueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.collectRefs(rows)
}

func (s *Service) collectRefs(rows pgx.Rows) ([]vision.Ref, error) {
	var out []vision.Ref
	for rows.Next() {
		var id uuid.UUID
		var sum []byte
		if err := rows.Scan(&id, &sum); err != nil {
			return nil, err
		}
		r := vision.Ref{Load: func(ctx context.Context) ([]byte, error) {
			var img []byte
			return img, s.pool.QueryRow(ctx, `SELECT image FROM reference_photos WHERE id=$1`, id).Scan(&img)
		}}
		copy(r.SHA256[:], sum)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PlaceTest is the result of "test from a seat": how strongly a photo taken now
// matches a saved place, so organizers can see whether their photos cover the
// room before relying on them.
type PlaceTest struct {
	Matched    bool `json:"matched"`
	Score      int  `json:"score"`
	Threshold  int  `json:"threshold"`
	References int  `json:"references"`
}

func (s *Service) TestVenuePhoto(ctx context.Context, u User, orgID, venueID uuid.UUID, photo []byte) (PlaceTest, error) {
	if _, err := s.require(ctx, u, orgID, PermCreateSession); err != nil {
		return PlaceTest{}, err
	}
	if len(photo) == 0 || len(photo) > vision.MaxPhotoBytes {
		return PlaceTest{}, newErr(400, "photo_invalid", "That file is not a usable photo.")
	}
	rows, err := s.pool.Query(ctx, `SELECT id, sha256 FROM reference_photos WHERE org_id=$1 AND venue_id=$2 ORDER BY created_at DESC`, orgID, venueID)
	if err != nil {
		return PlaceTest{}, err
	}
	refs, err := s.collectRefs(rows)
	rows.Close()
	if err != nil {
		return PlaceTest{}, err
	}
	if len(refs) == 0 {
		return PlaceTest{}, newErr(409, "no_references", "Add a photo of this place first.")
	}
	res, err := s.vis.Verify(ctx, photo, nil, refs)
	if err != nil {
		return PlaceTest{}, visionErr(err)
	}
	return PlaceTest{Matched: res.Matched, Score: res.Score, Threshold: res.Threshold, References: len(refs)}, nil
}

// PurgeSessionPhotos deletes "today's photos" of sessions that ended long ago:
// they only matter while that session's check-ins can still be reviewed.
func (s *Service) PurgeSessionPhotos(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM reference_photos p USING attendance_sessions s
		WHERE p.session_id=s.id AND s.ends_at < $1`, s.Now().Add(-sessionPhotoRetention))
	return tag.RowsAffected(), err
}
