package app

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---- attendance listing ---------------------------------------------------

type AttendanceRow struct {
	ID           uuid.UUID `json:"id"`
	UserID       uuid.UUID `json:"user_id"`
	FullName     string    `json:"full_name"`
	Email        *string   `json:"email,omitempty"`
	Phone        *string   `json:"phone,omitempty"`
	Status       string    `json:"status"`
	Method       string    `json:"method"`
	CheckedInAt  time.Time `json:"checked_in_at"`
	ManualReason *string   `json:"manual_reason,omitempty"`
	// Reasons are the stable codes behind the status (e.g. gps_unavailable,
	// visual_check_required), so a reviewer can see why a check-in was flagged.
	Reasons []string `json:"reasons"`
}

type AttendanceCursor struct {
	At time.Time
	ID uuid.UUID
}

// ListAttendance pages a session's records oldest-first. Contact details are
// returned only to roles allowed to see them.
func (s *Service) ListAttendance(ctx context.Context, u User, orgID, sid uuid.UUID, limit int, after *AttendanceCursor) ([]AttendanceRow, error) {
	role, err := s.require(ctx, u, orgID, PermViewAttendance)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, user_id, full_name_snapshot, email_snapshot, phone_snapshot, status::text, method::text, checked_in_at, manual_reason,
		ARRAY(SELECT jsonb_array_elements_text(CASE WHEN jsonb_typeof(evidence->'reasons')='array' THEN evidence->'reasons' ELSE '[]'::jsonb END))
		FROM attendance_records WHERE org_id=$1 AND session_id=$2 `
	args := []any{orgID, sid, limit}
	if after != nil {
		q += `AND (checked_in_at, id) > ($4, $5) `
		args = append(args, after.At, after.ID)
	}
	rows, err := s.pool.Query(ctx, q+`ORDER BY checked_in_at, id LIMIT $3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contacts := role.Can(PermViewContacts)
	out := []AttendanceRow{}
	for rows.Next() {
		var r AttendanceRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.FullName, &r.Email, &r.Phone, &r.Status, &r.Method, &r.CheckedInAt, &r.ManualReason, &r.Reasons); err != nil {
			return nil, err
		}
		if !contacts {
			r.Email, r.Phone = nil, nil
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type LiveCounts struct {
	Verified    int    `json:"verified"`
	NeedsReview int    `json:"needs_review"`
	Status      string `json:"status"`
}

// Counts is the cheap query behind the live organizer view.
func (s *Service) Counts(ctx context.Context, u User, orgID, sid uuid.UUID) (LiveCounts, error) {
	sess, err := s.GetSession(ctx, u, orgID, sid)
	if err != nil {
		return LiveCounts{}, err
	}
	return LiveCounts{Verified: sess.Verified, NeedsReview: sess.NeedsReview, Status: sess.Status}, nil
}

// ---- review & manual ------------------------------------------------------

// allowed review transitions; anything else is a conflict.
var reviewTransitions = map[string][]string{
	"needs_review": {"verified", "rejected"},
	"verified":     {"revoked"},
	"rejected":     {"verified"},
	"revoked":      {"verified"},
}

func (s *Service) ReviewRecord(ctx context.Context, u User, orgID, recordID uuid.UUID, to, reason string) error {
	if _, err := s.require(ctx, u, orgID, PermReview); err != nil {
		return err
	}
	reason, err := validText(reason, 3, 500, "Reason")
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var from string
	err = tx.QueryRow(ctx, `SELECT status::text FROM attendance_records WHERE id=$1 AND org_id=$2 FOR UPDATE`, recordID, orgID).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	ok := false
	for _, t := range reviewTransitions[from] {
		ok = ok || t == to
	}
	if !ok {
		return newErr(409, "invalid_transition", "That change is not allowed from the current status.")
	}
	if _, err := tx.Exec(ctx, `UPDATE attendance_records SET status=$2::attendance_status, updated_at=$3 WHERE id=$1`, recordID, to, s.Now()); err != nil {
		return err
	}
	if err := audit(ctx, tx, &orgID, u.ID, "attendance.review", recordID.String(), map[string]any{"from": from, "to": to, "reason": reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ManualAttendance is the organizer-only fallback for attendees who cannot scan.
// It is stored as method=manual (never passed off as QR-verified) with the
// acting organizer and a mandatory reason, and is audited.
func (s *Service) ManualAttendance(ctx context.Context, u User, orgID, sid, target uuid.UUID, reason string) (uuid.UUID, error) {
	if _, err := s.require(ctx, u, orgID, PermManual); err != nil {
		return uuid.Nil, err
	}
	reason, err := validText(reason, 3, 500, "Reason")
	if err != nil {
		return uuid.Nil, err
	}
	var status string
	err = s.pool.QueryRow(ctx, `SELECT status::text FROM attendance_sessions WHERE id=$1 AND org_id=$2`, sid, orgID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if status == "cancelled" {
		return uuid.Nil, newErr(409, "session_cancelled", "This session was cancelled.")
	}
	tu, err := s.UserByID(ctx, target)
	if err != nil {
		return uuid.Nil, invalid("That user does not exist.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO attendance_records
		(org_id, session_id, user_id, status, method, full_name_snapshot, email_snapshot, phone_snapshot, evidence, manual_reason, manual_by, checked_in_at, updated_at)
		VALUES($1,$2,$3,'verified','manual',$4,$5,$6,'{"manual":true}',$7,$8,$9,$9) RETURNING id`,
		orgID, sid, target, tu.FullName, tu.Email, tu.Phone, reason, u.ID, s.Now()).Scan(&id)
	if isUnique(err) {
		return uuid.Nil, newErr(409, "already_recorded", "This person already has an attendance record for this session.")
	}
	if err != nil {
		return uuid.Nil, err
	}
	if err := audit(ctx, tx, &orgID, u.ID, "attendance.manual", id.String(), map[string]any{"user": target, "reason": reason}); err != nil {
		return uuid.Nil, err
	}
	return id, tx.Commit(ctx)
}

// ---- personal history -----------------------------------------------------

type HistoryRow struct {
	SessionTitle     string    `json:"session_title"`
	OrganizationName string    `json:"organization_name"`
	Status           string    `json:"status"`
	CheckedInAt      time.Time `json:"checked_in_at"`
}

func (s *Service) MyHistory(ctx context.Context, u User, limit int) ([]HistoryRow, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT se.title, o.name, r.status::text, r.checked_in_at
		FROM attendance_records r JOIN attendance_sessions se ON se.id=r.session_id JOIN organizations o ON o.id=r.org_id
		WHERE r.user_id=$1 ORDER BY r.checked_in_at DESC LIMIT $2`, u.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryRow{}
	for rows.Next() {
		var h HistoryRow
		if err := rows.Scan(&h.SessionTitle, &h.OrganizationName, &h.Status, &h.CheckedInAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ---- CSV export -----------------------------------------------------------

// csvSafe defuses spreadsheet formula injection: a cell that starts with a
// formula trigger gets a leading apostrophe so Excel/Sheets treat it as text.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r', '\n':
		return "'" + s
	}
	return s
}

// ExportCSV streams verified and needs_review records as UTF-8 with a BOM so
// spreadsheet software detects the encoding (Arabic/Kurdish names stay intact).
// Only roles with export permission may call it.
func (s *Service) ExportCSV(ctx context.Context, u User, orgID, sid uuid.UUID, w io.Writer) error {
	if _, err := s.require(ctx, u, orgID, PermExport); err != nil {
		return err
	}
	var title, tz string
	err := s.pool.QueryRow(ctx, `SELECT se.title, o.timezone FROM attendance_sessions se JOIN organizations o ON o.id=se.org_id
		WHERE se.id=$1 AND se.org_id=$2`, sid, orgID).Scan(&title, &tz)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	rows, err := s.pool.Query(ctx, `SELECT full_name_snapshot, email_snapshot, phone_snapshot, status::text, method::text, checked_in_at
		FROM attendance_records WHERE org_id=$1 AND session_id=$2 AND status IN ('verified','needs_review')
		ORDER BY checked_in_at, id`, orgID, sid)
	if err != nil {
		return err
	}
	defer rows.Close()

	if _, err := io.WriteString(w, "\xEF\xBB\xBF"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Full Name", "Email", "Phone", "Status", "Method", "Checked In (" + tz + ")"})
	for rows.Next() {
		var name, status, method string
		var email, phone *string
		var at time.Time
		if err := rows.Scan(&name, &email, &phone, &status, &method, &at); err != nil {
			return err
		}
		_ = cw.Write([]string{csvSafe(name), csvSafe(deref(email)), csvSafe(deref(phone)),
			strings.ReplaceAll(status, "_", " "), method, at.In(loc).Format("2006-01-02 15:04:05 -07:00")})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	_ = audit(ctx, s.pool, &orgID, u.ID, "export.csv", sid.String(), nil)
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
