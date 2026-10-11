package app

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iamhere/api/internal/attendance"
	"github.com/iamhere/api/internal/qr"
)

// GPSInput is what the attendee's browser reports. Everything here is
// client-supplied and treated as untrusted evidence, never proof.
type GPSInput struct {
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	AccuracyM float64 `json:"accuracy_m"`
	// FixAgeMs is computed on the client as (Date.now() - position.timestamp),
	// which is robust to a skewed device clock, unlike an absolute timestamp.
	FixAgeMs int64 `json:"fix_age_ms"`
}

type CheckInInput struct {
	Token string
	GPS   *GPSInput
}

type CheckInResult struct {
	Status           string     `json:"status"` // verified | needs_review | rejected (or the stored status if already recorded)
	Reasons          []string   `json:"reasons"`
	RecordID         *uuid.UUID `json:"record_id,omitempty"`
	AlreadyRecorded  bool       `json:"already_recorded"`
	SessionTitle     string     `json:"session_title"`
	OrganizationName string     `json:"organization_name"`
}

func (g *GPSInput) validate() error {
	if !validCoord(g.Lat, g.Lon) || math.IsNaN(g.AccuracyM) || g.AccuracyM < 0 || g.AccuracyM > 100000 ||
		g.FixAgeMs < -60000 || g.FixAgeMs > 7*24*3600*1000 {
		return invalid("Invalid location reading.")
	}
	return nil
}

// CheckIn verifies one attendee's attempt. Every attendee has an independent
// attempt against the same shared QR; the (session,user) unique constraint and a
// single upsert statement guarantee one effective record under concurrency.
func (s *Service) CheckIn(ctx context.Context, u User, in CheckInInput) (CheckInResult, error) {
	now := s.Now()
	badQR := newErr(400, "invalid_qr", "This QR code is not valid. Scan the code shown by the organizer.")

	sid, err := qr.SessionID(in.Token)
	if err != nil {
		return CheckInResult{}, badQR
	}
	var lat, lon *float64
	if in.GPS != nil {
		if err := in.GPS.validate(); err != nil {
			return CheckInResult{}, err
		}
		lat, lon = &in.GPS.Lat, &in.GPS.Lon
	}

	var (
		orgID                    uuid.UUID
		orgName, title, status   string
		mode                     string
		endsAt                   time.Time
		secret                   []byte
		radius, maxAcc, rotation int
		hasAnchor                bool
		anchorAcc                float64
		dist                     *float64
	)
	err = s.pool.QueryRow(ctx, `SELECT s.org_id, o.name, s.title, s.status::text, s.ends_at, s.qr_secret,
		s.verification_mode::text, s.gps_radius_m, s.max_gps_accuracy_m, s.qr_rotation_s,
		s.anchor IS NOT NULL, COALESCE(s.anchor_accuracy_m,0)::float8,
		CASE WHEN s.anchor IS NOT NULL AND $2::float8 IS NOT NULL
		     THEN ST_Distance(s.anchor, ST_SetSRID(ST_MakePoint($3::float8,$2::float8),4326)::geography) END
		FROM attendance_sessions s JOIN organizations o ON o.id=s.org_id WHERE s.id=$1`, sid, lat, lon).
		Scan(&orgID, &orgName, &title, &status, &endsAt, &secret, &mode, &radius, &maxAcc, &rotation, &hasAnchor, &anchorAcc, &dist)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckInResult{}, badQR // same answer as a forged token: no session enumeration
	}
	if err != nil {
		return CheckInResult{}, err
	}

	if err := qr.Verify(secret, sid, in.Token, now, time.Duration(rotation)*time.Second, s.opt.QRGraceSlots); err != nil {
		if errors.Is(err, qr.ErrExpired) {
			return CheckInResult{}, newErr(410, "qr_expired", "This QR code has expired. Scan the code currently on the screen.")
		}
		return CheckInResult{}, badQR
	}
	// Server-authoritative end. The small grace covers a request that started
	// scanning just before expiry; it is not a client-controlled value.
	if status != "active" || now.After(endsAt.Add(s.opt.CheckinGrace)) {
		return CheckInResult{}, newErr(410, "session_ended", "This attendance session has ended.")
	}

	gps := attendance.GPSReading{}
	if in.GPS != nil && dist != nil {
		gps = attendance.GPSReading{
			Present:   true,
			DistanceM: *dist,
			// the organizer anchor is itself uncertain; widen so we never reject on its error
			AccuracyM: in.GPS.AccuracyM + anchorAcc,
			AgeS:      math.Max(0, float64(in.GPS.FixAgeMs)/1000),
		}
	}
	dec := attendance.Decide(attendance.Policy{
		Mode:         attendance.Mode(mode),
		RadiusM:      float64(radius),
		MaxAccuracyM: float64(maxAcc),
		MaxFixAgeS:   s.opt.MaxFixAge.Seconds(),
		HasAnchor:    hasAnchor,
		// VisualAvailable stays false until the visual module exists: in visual
		// and strict modes every attempt is honestly routed to review.
	}, gps, false)

	// Privacy: store the derived distance and accuracy, never raw coordinates.
	ev := map[string]any{"mode": mode, "qr": "valid", "reasons": dec.Reasons}
	if gps.Present {
		ev["gps"] = map[string]any{"distance_m": round1(gps.DistanceM), "accuracy_m": round1(gps.AccuracyM), "fix_age_s": round1(gps.AgeS)}
	}
	evJSON, _ := json.Marshal(ev)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CheckInResult{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `INSERT INTO verification_attempts(org_id, session_id, user_id, outcome, reasons, evidence)
		VALUES($1,$2,$3,$4,$5,$6)`, orgID, sid, u.ID, string(dec.Status), dec.Reasons, evJSON); err != nil {
		return CheckInResult{}, err
	}
	res := CheckInResult{Status: string(dec.Status), Reasons: dec.Reasons, SessionTitle: title, OrganizationName: orgName}
	if dec.Status == attendance.StatusRejected {
		return res, tx.Commit(ctx)
	}

	// A verified retry may upgrade a needs_review record; nothing else is ever
	// overwritten, so organizer decisions (rejected/revoked) stand.
	var id uuid.UUID
	var stored string
	var inserted bool
	err = tx.QueryRow(ctx, `INSERT INTO attendance_records
		(org_id, session_id, user_id, status, method, full_name_snapshot, email_snapshot, phone_snapshot, evidence, checked_in_at, updated_at)
		VALUES($1,$2,$3,$4::attendance_status,'qr',$5,$6,$7,$8,$9,$9)
		ON CONFLICT (session_id, user_id) DO UPDATE SET status='verified', evidence=EXCLUDED.evidence, updated_at=EXCLUDED.updated_at
		  WHERE attendance_records.status='needs_review' AND EXCLUDED.status='verified'
		RETURNING id, status::text, (xmax = 0)`,
		orgID, sid, u.ID, string(dec.Status), u.FullName, u.Email, u.Phone, evJSON, now).Scan(&id, &stored, &inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id, status::text FROM attendance_records WHERE session_id=$1 AND user_id=$2`, sid, u.ID).Scan(&id, &stored)
		inserted = false
	}
	if err != nil {
		return CheckInResult{}, err
	}
	res.RecordID, res.Status, res.AlreadyRecorded = &id, stored, !inserted
	return res, tx.Commit(ctx)
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
