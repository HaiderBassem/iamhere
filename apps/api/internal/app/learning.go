package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iamhere/api/internal/vision"
)

// Rooms learned from use (docs/VISION.md, "Learning each room"). A strict
// check-in that was verified beyond doubt contributes its photo's room
// features (never the photo) as an extra reference for the place. Views that
// look almost exactly like an existing reference add nothing and are skipped.
const (
	learnMinScore        = 150 // percent of the threshold: clearly the room...
	learnMaxScore        = 400 // ...but not a near-copy of a view already known
	maxLearnedPerPlace   = 24
	maxLearnedPerSession = 4 // a whole class arriving teaches a few views, not dozens of near-copies
	learnedTTL           = 30 * 24 * time.Hour
)

// shouldLearn decides whether a verified check-in teaches the place a new view.
func shouldLearn(verified bool, venueID *uuid.UUID, vr vision.Result, codeOK bool) bool {
	if !verified || venueID == nil || !vr.Matched || !codeOK || vr.ProbeID == "" || vr.Threshold <= 0 {
		return false
	}
	pct := vr.Score * 100 / vr.Threshold
	return pct >= learnMinScore && pct < learnMaxScore
}

// learn fetches the probe's features from the vision service and keeps them,
// keeping only the newest views per place and a few per session. It runs after
// the check-in is answered; a failure only means nothing was learned.
func (s *Service) learn(orgID, venueID, sessionID uuid.UUID, vr vision.Result) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	full := func(q dbq) bool {
		var n int
		err := q.QueryRow(ctx, `SELECT count(*) FROM learned_views WHERE org_id=$1 AND venue_id=$2 AND session_id=$3`, orgID, venueID, sessionID).Scan(&n)
		return err != nil || n >= maxLearnedPerSession
	}
	if full(s.pool) {
		return
	}
	feats, err := s.vis.ProbeFeatures(ctx, vr.ProbeID)
	if err != nil {
		return
	}
	sum := vision.SHA256(feats)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	// one learner per place at a time, so the per-session count holds
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM venues WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, venueID).Scan(&one); err != nil || full(tx) {
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO learned_views(org_id, venue_id, session_id, features, sha256, score, expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		orgID, venueID, sessionID, feats, sum[:], vr.Score, s.Now().Add(learnedTTL)); err != nil {
		s.logErr("learn a view", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM learned_views WHERE org_id=$1 AND venue_id=$2 AND id NOT IN (
		SELECT id FROM learned_views WHERE org_id=$1 AND venue_id=$2 ORDER BY created_at DESC LIMIT $3)`,
		orgID, venueID, maxLearnedPerPlace); err != nil {
		s.logErr("prune learned views", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.logErr("learn a view", err)
	}
}

func (s *Service) logErr(what string, err error) {
	if s.opt.Log != nil && err != nil {
		s.opt.Log.Error(what, "err", err.Error())
	}
}

// learnedRefs are a place's learned views, newest first.
func (s *Service) learnedRefs(ctx context.Context, orgID uuid.UUID, venueID *uuid.UUID) ([]vision.Ref, error) {
	if venueID == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id, sha256 FROM learned_views
		WHERE org_id=$1 AND venue_id=$2 AND expires_at > $3 ORDER BY created_at DESC LIMIT $4`,
		orgID, *venueID, s.Now(), maxLearnedPerPlace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []vision.Ref
	for rows.Next() {
		var id uuid.UUID
		var sum []byte
		if err := rows.Scan(&id, &sum); err != nil {
			return nil, err
		}
		r := vision.Ref{Features: true, Load: func(ctx context.Context) ([]byte, error) {
			var b []byte
			err := s.pool.QueryRow(ctx, `SELECT features FROM learned_views WHERE id=$1`, id).Scan(&b)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, vision.ErrRefGone // pruned or forgotten since the list was made
			}
			return b, err
		}}
		copy(r.SHA256[:], sum)
		out = append(out, r)
	}
	return out, rows.Err()
}

// PhotoStats is how the photo check has done at a place: the organizer's view
// of its accuracy in daily use.
type PhotoStats struct {
	Days       int `json:"days"`
	Checks     int `json:"checks"`     // attendee photos checked
	Recognized int `json:"recognized"` // ...in which the room was recognised
	CodeRead   int `json:"code_read"`  // ...in which the current code was read
	Learned    int `json:"learned"`    // learned views the place has now
	References int `json:"references"` // saved photos
}

func (s *Service) VenuePhotoStats(ctx context.Context, u User, orgID, venueID uuid.UUID) (PhotoStats, error) {
	if _, err := s.require(ctx, u, orgID, PermCreateSession); err != nil {
		return PhotoStats{}, err
	}
	st := PhotoStats{Days: 30}
	err := s.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE a.evidence->'visual' ? 'score'),
		count(*) FILTER (WHERE (a.evidence->'visual'->>'room_matched')::boolean),
		count(*) FILTER (WHERE (a.evidence->'visual'->>'current_code_in_photo')::boolean),
		(SELECT count(*) FROM learned_views l WHERE l.org_id=$1 AND l.venue_id=$2 AND l.expires_at > $3),
		(SELECT count(*) FROM reference_photos r WHERE r.org_id=$1 AND r.venue_id=$2)
		FROM verification_attempts a JOIN attendance_sessions se ON se.id=a.session_id
		WHERE a.org_id=$1 AND se.venue_id=$2 AND a.created_at > now() - make_interval(days => $4)`, // attempts carry the database's time
		orgID, venueID, s.Now(), st.Days).Scan(&st.Checks, &st.Recognized, &st.CodeRead, &st.Learned, &st.References)
	return st, err
}

// ForgetLearned drops what a place has learned (e.g. after the room was
// rearranged); recognition falls back to the saved photos.
func (s *Service) ForgetLearned(ctx context.Context, u User, orgID, venueID uuid.UUID) error {
	if _, err := s.require(ctx, u, orgID, PermManageOrg); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM learned_views WHERE org_id=$1 AND venue_id=$2`, orgID, venueID); err != nil {
		return err
	}
	if err := audit(ctx, tx, &orgID, u.ID, "venue.forget_learned", venueID.String(), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PurgeLearned deletes learned views past their 30 days.
func (s *Service) PurgeLearned(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM learned_views WHERE expires_at < $1`, s.Now())
	return err
}
