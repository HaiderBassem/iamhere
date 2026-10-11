package app

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

// ---- attendance report per person ------------------------------------------
//
// The registrar's question is not "who came today" but "who missed too many".
// A report counts, for each person who checked in at least once in a period,
// how many of the organization's sessions in that period they attended. The
// title filter stands in for courses: "Software Engineering" every Sunday is
// one title.

type ReportRow struct {
	UserID   uuid.UUID `json:"user_id"`
	FullName string    `json:"full_name"`
	Email    *string   `json:"email,omitempty"`
	Attended int       `json:"attended"`  // verified check-ins (including added by hand)
	ToReview int       `json:"to_review"` // still waiting for the organizer
	Rate     float64   `json:"rate"`      // attended / sessions, 0..1
	LastSeen time.Time `json:"last_seen"`
}

type Report struct {
	From      string      `json:"from"` // YYYY-MM-DD, in the organization's timezone
	To        string      `json:"to"`
	Title     string      `json:"title,omitempty"`
	Timezone  string      `json:"timezone"`
	Sessions  int         `json:"sessions"`
	Titles    []string    `json:"titles"` // session titles in the period, for the filter
	People    []ReportRow `json:"people"`
	Truncated bool        `json:"truncated"`
}

type ReportQuery struct {
	From, To string // YYYY-MM-DD; empty = the last 30 days
	Title    string // empty = every session
}

const (
	maxReportDays   = 366
	maxReportPeople = 5000
)

// period resolves the query's dates in the organization's timezone into a
// half-open [start, end) range of instants.
func (q ReportQuery) period(loc *time.Location, now time.Time) (from, to time.Time, err error) {
	today := now.In(loc)
	day := func(s string, def time.Time) (time.Time, error) {
		if s == "" {
			return time.Date(def.Year(), def.Month(), def.Day(), 0, 0, 0, 0, loc), nil
		}
		t, err := time.ParseInLocation("2006-01-02", s, loc)
		if err != nil {
			return time.Time{}, invalid("Dates must look like 2026-10-11.")
		}
		return t, nil
	}
	if to, err = day(q.To, today); err != nil {
		return
	}
	if from, err = day(q.From, to.AddDate(0, 0, -29)); err != nil {
		return
	}
	if from.After(to) {
		return from, to, invalid("The start date is after the end date.")
	}
	if to.Sub(from) > maxReportDays*24*time.Hour {
		return from, to, invalid(fmt.Sprintf("A report covers at most %d days.", maxReportDays))
	}
	return from, to.AddDate(0, 0, 1), nil
}

func (s *Service) OrgReport(ctx context.Context, u User, orgID uuid.UUID, q ReportQuery) (Report, error) {
	role, err := s.require(ctx, u, orgID, PermViewAttendance)
	if err != nil {
		return Report{}, err
	}
	return s.report(ctx, orgID, q, role.Can(PermViewContacts))
}

func (s *Service) report(ctx context.Context, orgID uuid.UUID, q ReportQuery, contacts bool) (Report, error) {
	title := ""
	if q.Title != "" {
		t, err := validText(q.Title, 1, 200, "Title")
		if err != nil {
			return Report{}, err
		}
		title = t
	}
	var tz string
	if err := s.pool.QueryRow(ctx, `SELECT timezone FROM organizations WHERE id=$1`, orgID).Scan(&tz); err != nil {
		return Report{}, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	from, to, err := q.period(loc, s.Now())
	if err != nil {
		return Report{}, err
	}
	rep := Report{From: from.Format("2006-01-02"), To: to.AddDate(0, 0, -1).Format("2006-01-02"), Title: title, Timezone: tz,
		Titles: []string{}, People: []ReportRow{}}

	const inPeriod = `org_id=$1 AND status <> 'cancelled' AND starts_at >= $2 AND starts_at < $3`
	rows, err := s.pool.Query(ctx, `SELECT title FROM attendance_sessions WHERE `+inPeriod+`
		GROUP BY title ORDER BY max(starts_at) DESC LIMIT 200`, orgID, from, to)
	if err != nil {
		return Report{}, err
	}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return Report{}, err
		}
		rep.Titles = append(rep.Titles, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Report{}, err
	}

	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM attendance_sessions WHERE `+inPeriod+` AND ($4 = '' OR title = $4)`,
		orgID, from, to, title).Scan(&rep.Sessions); err != nil {
		return Report{}, err
	}
	rows, err = s.pool.Query(ctx, `WITH ss AS (SELECT id FROM attendance_sessions WHERE `+inPeriod+` AND ($4 = '' OR title = $4))
		SELECT r.user_id, u.full_name, u.email,
		       count(*) FILTER (WHERE r.status='verified'), count(*) FILTER (WHERE r.status='needs_review'), max(r.checked_in_at)
		FROM attendance_records r JOIN ss ON ss.id=r.session_id JOIN users u ON u.id=r.user_id
		WHERE r.status IN ('verified','needs_review')
		GROUP BY r.user_id, u.full_name, u.email
		ORDER BY u.full_name, r.user_id LIMIT $5`, orgID, from, to, title, maxReportPeople+1)
	if err != nil {
		return Report{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var p ReportRow
		if err := rows.Scan(&p.UserID, &p.FullName, &p.Email, &p.Attended, &p.ToReview, &p.LastSeen); err != nil {
			return Report{}, err
		}
		if len(rep.People) == maxReportPeople {
			rep.Truncated = true
			break
		}
		if !contacts {
			p.Email = nil
		}
		if rep.Sessions > 0 {
			p.Rate = float64(p.Attended) / float64(rep.Sessions)
		}
		rep.People = append(rep.People, p)
	}
	return rep, rows.Err()
}

// ReportCSV writes the report as a spreadsheet (UTF-8 with a BOM, formula-safe).
func (s *Service) ReportCSV(ctx context.Context, u User, orgID uuid.UUID, q ReportQuery, w io.Writer) error {
	if _, err := s.require(ctx, u, orgID, PermExport); err != nil {
		return err
	}
	rep, err := s.report(ctx, orgID, q, true)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(rep.Timezone)
	if err != nil {
		loc = time.UTC
	}
	if _, err := io.WriteString(w, "\xEF\xBB\xBF"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Full Name", "Email", "Attended", "Sessions", "Rate (%)", "To Review", "Last Check-in (" + rep.Timezone + ")"})
	for _, p := range rep.People {
		_ = cw.Write([]string{csvSafe(p.FullName), csvSafe(deref(p.Email)), fmt.Sprint(p.Attended), fmt.Sprint(rep.Sessions),
			fmt.Sprintf("%.0f", p.Rate*100), fmt.Sprint(p.ToReview), p.LastSeen.In(loc).Format("2006-01-02 15:04")})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	_ = audit(ctx, s.pool, &orgID, u.ID, "export.report", orgID.String(), map[string]any{"from": rep.From, "to": rep.To, "title": rep.Title})
	return nil
}
