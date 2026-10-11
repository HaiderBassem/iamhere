package app

import (
	"context"
	"crypto/rand"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iamhere/api/internal/qr"
)

type Anchor struct {
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	AccuracyM float64 `json:"accuracy_m"`
}

func (a *Anchor) validate() error {
	if a == nil {
		return nil
	}
	if !validCoord(a.Lat, a.Lon) || a.AccuracyM < 0 || a.AccuracyM > 100000 || math.IsNaN(a.AccuracyM) {
		return invalid("Invalid location.")
	}
	return nil
}

func validCoord(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

type CreateSessionInput struct {
	Title          string
	IdempotencyKey string  // lets a retried "Create & Display QR" return the same session
	Anchor         *Anchor // organizer device position; optional
	DurationS      int     // advanced; 0 = organization default
}

type Session struct {
	ID               uuid.UUID  `json:"id"`
	OrgID            uuid.UUID  `json:"org_id"`
	CreatedBy        uuid.UUID  `json:"created_by"`
	Title            string     `json:"title"`
	Status           string     `json:"status"` // effective status, computed against server time
	StartsAt         time.Time  `json:"starts_at"`
	EndsAt           time.Time  `json:"ends_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	VerificationMode string     `json:"verification_mode"`
	GPSRadiusM       int        `json:"gps_radius_m"`
	QRRotationS      int        `json:"qr_rotation_s"`
	HasAnchor        bool       `json:"has_anchor"`
	Verified         int        `json:"verified_count"`
	NeedsReview      int        `json:"needs_review_count"`
}

const sessionSelect = `SELECT s.id, s.org_id, s.created_by, s.title, s.status::text, s.starts_at, s.ends_at, s.closed_at,
	s.verification_mode::text, s.gps_radius_m, s.qr_rotation_s, s.anchor IS NOT NULL,
	(SELECT count(*) FROM attendance_records r WHERE r.session_id=s.id AND r.status='verified'),
	(SELECT count(*) FROM attendance_records r WHERE r.session_id=s.id AND r.status='needs_review')
	FROM attendance_sessions s `

func (s *Service) scanSession(r pgx.Row) (Session, error) {
	var x Session
	err := r.Scan(&x.ID, &x.OrgID, &x.CreatedBy, &x.Title, &x.Status, &x.StartsAt, &x.EndsAt, &x.ClosedAt,
		&x.VerificationMode, &x.GPSRadiusM, &x.QRRotationS, &x.HasAnchor, &x.Verified, &x.NeedsReview)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	if err == nil && x.Status == "active" && !s.Now().Before(x.EndsAt) {
		x.Status = "expired"
	}
	return x, err
}

// CreateSession is the title-only organizer flow: everything else comes from
// organization policy. With an IdempotencyKey, repeated requests return the
// original session instead of creating duplicates.
func (s *Service) CreateSession(ctx context.Context, u User, orgID uuid.UUID, in CreateSessionInput) (Session, error) {
	if _, err := s.require(ctx, u, orgID, PermCreateSession); err != nil {
		return Session{}, err
	}
	title, err := validText(in.Title, 1, 200, "Title")
	if err != nil {
		return Session{}, err
	}
	if err := in.Anchor.validate(); err != nil {
		return Session{}, err
	}
	if in.DurationS != 0 && (in.DurationS < 60 || in.DurationS > 86400) {
		return Session{}, invalid("Duration must be between 1 minute and 24 hours.")
	}
	if len(in.IdempotencyKey) > 100 {
		return Session{}, invalid("Idempotency key is too long.")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Session{}, err
	}
	var key *string
	if in.IdempotencyKey != "" {
		key = &in.IdempotencyKey
	}
	var lat, lon, acc *float64
	if a := in.Anchor; a != nil {
		lat, lon, acc = &a.Lat, &a.Lon, &a.AccuracyM
	}
	now := s.Now()

	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `INSERT INTO attendance_sessions
		(org_id, created_by, title, starts_at, ends_at, verification_mode, gps_radius_m, max_gps_accuracy_m, qr_rotation_s,
		 qr_secret, anchor, anchor_accuracy_m, idempotency_key)
		SELECT $1, $2, $3, $4::timestamptz,
		       $4::timestamptz + make_interval(secs => COALESCE(NULLIF($5::int,0), st.default_duration_s)),
		       st.verification_mode, st.gps_radius_m, st.max_gps_accuracy_m, st.qr_rotation_s, $6,
		       CASE WHEN $7::float8 IS NULL THEN NULL ELSE ST_SetSRID(ST_MakePoint($8::float8,$7::float8),4326)::geography END,
		       $9::float8, $10
		FROM organization_settings st WHERE st.org_id=$1
		ON CONFLICT (org_id, created_by, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING id`,
		orgID, u.ID, title, now, in.DurationS, secret, lat, lon, acc, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) && key != nil {
		err = s.pool.QueryRow(ctx, `SELECT id FROM attendance_sessions WHERE org_id=$1 AND created_by=$2 AND idempotency_key=$3`,
			orgID, u.ID, *key).Scan(&id)
	}
	if err != nil {
		return Session{}, err
	}
	_ = audit(ctx, s.pool, &orgID, u.ID, "session.create", id.String(), nil)
	return s.scanSession(s.pool.QueryRow(ctx, sessionSelect+`WHERE s.id=$1`, id))
}

func (s *Service) GetSession(ctx context.Context, u User, orgID, sid uuid.UUID) (Session, error) {
	if _, err := s.require(ctx, u, orgID, PermViewAttendance); err != nil {
		return Session{}, err
	}
	return s.scanSession(s.pool.QueryRow(ctx, sessionSelect+`WHERE s.id=$1 AND s.org_id=$2`, sid, orgID))
}

type SessionCursor struct {
	StartsAt time.Time
	ID       uuid.UUID
}

// ListSessions pages newest-first with a keyset cursor (stable under inserts).
func (s *Service) ListSessions(ctx context.Context, u User, orgID uuid.UUID, limit int, after *SessionCursor) ([]Session, error) {
	if _, err := s.require(ctx, u, orgID, PermViewAttendance); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	q := sessionSelect + `WHERE s.org_id=$1 `
	args := []any{orgID, limit}
	if after != nil {
		q += `AND (s.starts_at, s.id) < ($3, $4) `
		args = append(args, after.StartsAt, after.ID)
	}
	rows, err := s.pool.Query(ctx, q+`ORDER BY s.starts_at DESC, s.id DESC LIMIT $2`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		x, err := s.scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type QRInfo struct {
	Token       string    `json:"token"`
	NextInMs    int64     `json:"next_rotation_in_ms"`
	RotationS   int       `json:"rotation_s"`
	Status      string    `json:"status"`
	EndsAt      time.Time `json:"ends_at"`
	ServerNowMs int64     `json:"server_now_ms"`
}

// SessionQR returns the token currently valid for the organizer's display.
func (s *Service) SessionQR(ctx context.Context, u User, orgID, sid uuid.UUID) (QRInfo, error) {
	if _, err := s.require(ctx, u, orgID, PermCreateSession); err != nil {
		return QRInfo{}, err
	}
	var secret []byte
	var rot int
	var status string
	var ends time.Time
	err := s.pool.QueryRow(ctx, `SELECT qr_secret, qr_rotation_s, status::text, ends_at FROM attendance_sessions WHERE id=$1 AND org_id=$2`,
		sid, orgID).Scan(&secret, &rot, &status, &ends)
	if errors.Is(err, pgx.ErrNoRows) {
		return QRInfo{}, ErrNotFound
	}
	if err != nil {
		return QRInfo{}, err
	}
	now := s.Now()
	if status != "active" || !now.Before(ends) {
		return QRInfo{}, newErr(410, "session_ended", "This attendance session has ended.")
	}
	d := time.Duration(rot) * time.Second
	next := time.Unix(int64(qr.Slot(now, d)+1)*int64(rot), 0)
	return QRInfo{
		Token: qr.Sign(secret, sid, now, d), NextInMs: next.Sub(now).Milliseconds(), RotationS: rot,
		Status: status, EndsAt: ends, ServerNowMs: now.UnixMilli(),
	}, nil
}

// CloseSession ends a session early. Organizers can close their own sessions;
// admins and owners can close any. Closing twice is a no-op.
func (s *Service) CloseSession(ctx context.Context, u User, orgID, sid uuid.UUID) (Session, error) {
	role, err := s.require(ctx, u, orgID, PermCreateSession)
	if err != nil {
		return Session{}, err
	}
	var creator uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT created_by FROM attendance_sessions WHERE id=$1 AND org_id=$2`, sid, orgID).Scan(&creator); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	if creator != u.ID && !role.Can(PermManageOrg) {
		return Session{}, ErrForbidden
	}
	tag, err := s.pool.Exec(ctx, `UPDATE attendance_sessions SET status='closed', closed_at=$3 WHERE id=$1 AND org_id=$2 AND status='active'`,
		sid, orgID, s.Now())
	if err != nil {
		return Session{}, err
	}
	if tag.RowsAffected() == 1 {
		_ = audit(ctx, s.pool, &orgID, u.ID, "session.close", sid.String(), nil)
	}
	return s.scanSession(s.pool.QueryRow(ctx, sessionSelect+`WHERE s.id=$1 AND s.org_id=$2`, sid, orgID))
}

// ExpireSessions persists the 'expired' state for sessions past their end.
// Reads already report expiry from ends_at, so this is bookkeeping only.
func (s *Service) ExpireSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE attendance_sessions SET status='expired' WHERE status='active' AND ends_at <= $1`,
		s.Now().Add(-s.opt.CheckinGrace))
	return tag.RowsAffected(), err
}
