package app

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iamhere/api/internal/attendance"
	"github.com/iamhere/api/internal/qr"
	"github.com/iamhere/api/internal/vision"
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

// CheckInInput carries either a freshly scanned QR token or a ticket from Scan.
// Photo is the raw JPEG/PNG bytes of the live camera frame (strict mode only).
type CheckInInput struct {
	Token  string
	Ticket string
	GPS    *GPSInput
	Photo  []byte
	// PhotoHint is where the browser saw the QR code in Photo (four corners).
	// Only a hint: the code is read again on the server.
	PhotoHint *[4][2]float64
}

type CheckInResult struct {
	Status           string     `json:"status"` // verified | needs_review | rejected (or the stored status if already recorded)
	Mode             string     `json:"mode"`
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

var errBadQR = newErr(400, "invalid_qr", "This QR code is not valid. Scan the code shown by the organizer.")

// checkinSession is everything a check-in needs to know about its session.
type checkinSession struct {
	id, orgID                uuid.UUID
	orgName, title, status   string
	mode                     attendance.Mode
	endsAt                   time.Time
	secret                   []byte
	radius, maxAcc, rotation int
	hasAnchor                bool
	anchorAcc                float64
	venueID                  *uuid.UUID
	dist                     *float64 // metres from the anchor to the reported position, if both exist
}

func (s *Service) loadCheckinSession(ctx context.Context, sid uuid.UUID, lat, lon *float64) (*checkinSession, error) {
	var c checkinSession
	var mode string
	err := s.pool.QueryRow(ctx, `SELECT s.org_id, o.name, s.title, s.status::text, s.ends_at, s.qr_secret,
		s.verification_mode::text, s.gps_radius_m, s.max_gps_accuracy_m, s.qr_rotation_s,
		s.anchor IS NOT NULL, COALESCE(s.anchor_accuracy_m,0)::float8, s.venue_id,
		CASE WHEN s.anchor IS NOT NULL AND $2::float8 IS NOT NULL
		     THEN ST_Distance(s.anchor, ST_SetSRID(ST_MakePoint($3::float8,$2::float8),4326)::geography) END
		FROM attendance_sessions s JOIN organizations o ON o.id=s.org_id WHERE s.id=$1`, sid, lat, lon).
		Scan(&c.orgID, &c.orgName, &c.title, &c.status, &c.endsAt, &c.secret, &mode, &c.radius, &c.maxAcc, &c.rotation,
			&c.hasAnchor, &c.anchorAcc, &c.venueID, &c.dist)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errBadQR // same answer as a forged token: no session enumeration
	}
	if err != nil {
		return nil, err
	}
	c.id, c.mode = sid, attendance.Mode(mode)
	return &c, nil
}

// qrGrace is how many past rotations a scanned code may be. Checked modes are
// tighter: a relayed screenshot has to be forwarded within one rotation.
func (s *Service) qrGrace(m attendance.Mode) uint64 {
	if m.Checked() && s.opt.QRGraceSlots > 1 {
		return 1
	}
	return s.opt.QRGraceSlots
}

func (s *Service) verifyQR(c *checkinSession, token string, now time.Time) error {
	if err := qr.Verify(c.secret, c.id, token, now, time.Duration(c.rotation)*time.Second, s.qrGrace(c.mode)); err != nil {
		if errors.Is(err, qr.ErrExpired) {
			return newErr(410, "qr_expired", "That code just changed. Scan the code currently on the screen.")
		}
		return errBadQR
	}
	return nil
}

// stillOpen is the server-authoritative end check. The small grace covers a
// request that started just before the end; it is not client-controlled.
func (s *Service) stillOpen(c *checkinSession, now time.Time) error {
	if c.status != "active" || now.After(c.endsAt.Add(s.opt.CheckinGrace)) {
		return newErr(410, "session_ended", "This attendance session has ended.")
	}
	return nil
}

// ScanResult tells the client what is still needed to finish a check-in.
type ScanResult struct {
	Ticket           string `json:"ticket"`
	ExpiresInS       int    `json:"expires_in_s"`
	Mode             string `json:"mode"`
	NeedsGPS         bool   `json:"needs_gps"`
	NeedsPhoto       bool   `json:"needs_photo"`
	AlreadyRecorded  bool   `json:"already_recorded"`
	Status           string `json:"status,omitempty"` // existing record's status when already recorded
	SessionTitle     string `json:"session_title"`
	OrganizationName string `json:"organization_name"`
}

// Scan validates a freshly scanned QR code and returns a ticket plus the list of
// remaining steps. The QR is only checked here, so with a 5-second rotation the
// person can still take their time over the location and camera steps.
func (s *Service) Scan(ctx context.Context, u User, token string) (ScanResult, error) {
	now := s.Now()
	sid, err := qr.SessionID(token)
	if err != nil {
		return ScanResult{}, errBadQR
	}
	c, err := s.loadCheckinSession(ctx, sid, nil, nil)
	if err != nil {
		return ScanResult{}, err
	}
	if err := s.verifyQR(c, token, now); err != nil {
		return ScanResult{}, err
	}
	if err := s.stillOpen(c, now); err != nil {
		return ScanResult{}, err
	}
	res := ScanResult{
		Ticket: qr.SignTicket(c.secret, sid, u.ID, now), ExpiresInS: int(qr.TicketTTL.Seconds()),
		Mode: string(c.mode), SessionTitle: c.title, OrganizationName: c.orgName,
	}
	if c.mode.Checked() {
		// Only ask for steps that can change the outcome: GPS needs a session
		// anchor, the photo needs reference photos of the place.
		res.NeedsGPS = c.hasAnchor
		refs, err := s.sessionRefs(ctx, c.orgID, sid, c.venueID)
		if err != nil {
			return ScanResult{}, err
		}
		res.NeedsPhoto = len(refs) > 0 && s.vis != nil
	}
	var status string
	err = s.pool.QueryRow(ctx, `SELECT status::text FROM attendance_records WHERE session_id=$1 AND user_id=$2`, sid, u.ID).Scan(&status)
	if err == nil {
		res.AlreadyRecorded, res.Status = true, status
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ScanResult{}, err
	}
	return res, nil
}

// CheckIn verifies one attendee's attempt. Every attendee has an independent
// attempt against the same shared QR; the (session,user) unique constraint and a
// single upsert statement guarantee one effective record under concurrency.
//
//	standard: a fresh QR (or a ticket from one) is the whole check.
//	strict:   QR + GPS against the session anchor + a live photo against the
//	          venue's reference photos. Anything uncertain goes to review.
func (s *Service) CheckIn(ctx context.Context, u User, in CheckInInput) (CheckInResult, error) {
	now := s.Now()

	var sid uuid.UUID
	var err error
	if in.Ticket != "" {
		sid, err = qr.TicketSessionID(in.Ticket)
	} else {
		sid, err = qr.SessionID(in.Token)
	}
	if err != nil {
		return CheckInResult{}, errBadQR
	}
	var lat, lon *float64
	if in.GPS != nil {
		if err := in.GPS.validate(); err != nil {
			return CheckInResult{}, err
		}
		lat, lon = &in.GPS.Lat, &in.GPS.Lon
	}
	c, err := s.loadCheckinSession(ctx, sid, lat, lon)
	if err != nil {
		return CheckInResult{}, err
	}
	if in.Ticket != "" {
		if err := qr.VerifyTicket(c.secret, sid, u.ID, in.Ticket, now); err != nil {
			if errors.Is(err, qr.ErrExpired) {
				return CheckInResult{}, newErr(410, "ticket_expired", "That took too long. Scan the code on the screen again.")
			}
			return CheckInResult{}, errBadQR
		}
	} else if err := s.verifyQR(c, in.Token, now); err != nil {
		return CheckInResult{}, err
	}
	if err := s.stillOpen(c, now); err != nil {
		return CheckInResult{}, err
	}

	// Evidence is only gathered, and only stored, for modes that check it.
	// Normal mode ignores whatever location or photo a client may still send.
	var gps attendance.GPSReading
	var visualMatched, visualAvailable bool
	var visualReason string // a more specific reason than "photo did not match"
	var vres vision.Result  // kept to learn the room from a verified check-in
	var codeOK bool
	ev := map[string]any{"mode": string(c.mode), "qr": "valid"}
	if c.mode.Checked() {
		if in.GPS != nil && c.dist != nil {
			gps = attendance.GPSReading{
				Present:   true,
				DistanceM: *c.dist,
				// the organizer anchor is itself uncertain; widen so we never reject on its error
				AccuracyM: in.GPS.AccuracyM + c.anchorAcc,
				AgeS:      math.Max(0, float64(in.GPS.FixAgeMs)/1000),
			}
			// Privacy: store the derived distance and accuracy, never raw coordinates.
			ev["gps"] = map[string]any{"distance_m": round1(gps.DistanceM), "accuracy_m": round1(gps.AccuracyM), "fix_age_s": round1(gps.AgeS)}
		}
		refs, err := s.sessionRefs(ctx, c.orgID, sid, c.venueID)
		if err != nil {
			return CheckInResult{}, err
		}
		visualAvailable = len(refs) > 0 && s.vis != nil
		if visualAvailable && len(in.Photo) > 0 {
			if len(in.Photo) > vision.MaxPhotoBytes {
				return CheckInResult{}, newErr(400, "photo_too_large", "That photo is too large.")
			}
			vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			vr, err := s.vis.Verify(vctx, in.Photo, in.PhotoHint, refs)
			cancel()
			var pe *vision.PhotoError
			switch {
			case errors.As(err, &pe):
				return CheckInResult{}, newErr(400, "photo_unusable", pe.Message) // retryable: retake the photo
			case err != nil:
				// never a pass: the person goes to review instead of being blocked
				visualReason = attendance.ReasonVisualUnavailable
				ev["visual"] = map[string]any{"error": "unavailable"}
			default:
				vres = vr
				codeOK = s.codeInPhoto(c, vr.Codes, now)
				visualMatched = vr.Matched && codeOK
				if !codeOK {
					visualReason = attendance.ReasonPhotoNoCode
				}
				// Privacy: the photo is discarded; only the verdict is kept.
				ev["visual"] = map[string]any{"score": vr.Score, "room_matched": vr.Matched, "current_code_in_photo": codeOK, "model": vr.Model}
			}
		}
	}

	dec := attendance.Decide(attendance.Policy{
		Mode:            c.mode,
		RadiusM:         float64(c.radius),
		MaxAccuracyM:    float64(c.maxAcc),
		MaxFixAgeS:      s.opt.MaxFixAge.Seconds(),
		HasAnchor:       c.hasAnchor,
		VisualAvailable: visualAvailable,
	}, gps, visualMatched)
	if visualReason != "" {
		for i, r := range dec.Reasons {
			if r == attendance.ReasonVisualRequired {
				dec.Reasons[i] = visualReason
			}
		}
	}
	ev["reasons"] = dec.Reasons
	evJSON, _ := json.Marshal(ev)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CheckInResult{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `INSERT INTO verification_attempts(org_id, session_id, user_id, outcome, reasons, evidence)
		VALUES($1,$2,$3,$4,$5,$6)`, c.orgID, sid, u.ID, string(dec.Status), dec.Reasons, evJSON); err != nil {
		return CheckInResult{}, err
	}
	res := CheckInResult{Status: string(dec.Status), Mode: string(c.mode), Reasons: dec.Reasons, SessionTitle: c.title, OrganizationName: c.orgName}
	if dec.Status == attendance.StatusRejected {
		return res, tx.Commit(ctx)
	}

	// While a record is still in review, a new attempt replaces it: a verified
	// one upgrades it, a needs_review one refreshes the evidence so the reviewer
	// sees the latest reasons. Nothing else is ever overwritten, so verified
	// records and organizer decisions (rejected/revoked) stand.
	var id uuid.UUID
	var stored string
	var inserted bool
	err = tx.QueryRow(ctx, `INSERT INTO attendance_records
		(org_id, session_id, user_id, status, method, full_name_snapshot, email_snapshot, phone_snapshot, evidence, checked_in_at, updated_at)
		VALUES($1,$2,$3,$4::attendance_status,'qr',$5,$6,$7,$8,$9,$9)
		ON CONFLICT (session_id, user_id) DO UPDATE SET status=EXCLUDED.status, evidence=EXCLUDED.evidence, updated_at=EXCLUDED.updated_at
		  WHERE attendance_records.status='needs_review'
		RETURNING id, status::text, (xmax = 0)`,
		c.orgID, sid, u.ID, string(dec.Status), u.FullName, u.Email, u.Phone, evJSON, now).Scan(&id, &stored, &inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id, status::text FROM attendance_records WHERE session_id=$1 AND user_id=$2`, sid, u.ID).Scan(&id, &stored)
		inserted = false
	}
	if err != nil {
		return CheckInResult{}, err
	}
	res.RecordID, res.Status, res.AlreadyRecorded = &id, stored, !inserted
	if err := tx.Commit(ctx); err != nil {
		return CheckInResult{}, err
	}
	if shouldLearn(dec.Status == attendance.StatusVerified && stored == string(attendance.StatusVerified), c.venueID, vres, codeOK) {
		go s.learn(c.orgID, *c.venueID, sid, vres)
	}
	return res, nil
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// photoCodeGraceSlots is how old the code inside a strict-mode photo may be:
// the photo is taken the moment the code is seen, then uploaded, so allow a
// few rotations for slow connections (5 s rotation: 15-20 s).
const photoCodeGraceSlots = 3

// codeInPhoto reports whether one of the codes read in the photo is a recent
// check-in code of this session. That proves the camera was pointed at the
// live screen moments ago, not at an old photo of the room.
func (s *Service) codeInPhoto(c *checkinSession, codes []string, now time.Time) bool {
	for _, text := range codes {
		tok := text
		if i := strings.LastIndex(text, "#/c/"); i >= 0 {
			tok = text[i+len("#/c/"):]
		}
		if id, err := qr.SessionID(tok); err != nil || id != c.id {
			continue
		}
		if qr.Verify(c.secret, c.id, tok, now, time.Duration(c.rotation)*time.Second, photoCodeGraceSlots) == nil {
			return true
		}
	}
	return false
}
