package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iamhere/api/internal/auth"
	"github.com/iamhere/api/internal/db"
	"github.com/iamhere/api/internal/qr"
)

// Integration tests run against a real PostGIS server. Point TEST_PG_ADMIN_URL
// at an admin connection; each test gets its own throwaway database.
func adminURL() string {
	if v := os.Getenv("TEST_PG_ADMIN_URL"); v != "" {
		return v
	}
	return "postgres://postgres:dev@localhost:55432/postgres?sslmode=disable"
}

type env struct {
	svc  *Service
	pool *pgxpool.Pool
	mu   sync.Mutex
	now  time.Time
}

func (e *env) clock() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }
func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminURL())
	if err == nil {
		err = admin.Ping(ctx)
	}
	if err != nil {
		if os.Getenv("CI") != "" { // a missing database must fail CI, never skip silently
			t.Fatalf("test postgres unavailable in CI: %v", err)
		}
		t.Skipf("no test postgres available: %v", err)
	}
	name := "iah_t_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(adminURL())
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 40
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close()
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	e := &env{pool: pool, now: time.Date(2030, 1, 1, 10, 0, 3, 0, time.UTC)}
	e.svc = New(pool, Options{QRGraceSlots: 2, CheckinGrace: 5 * time.Second, MaxFixAge: 30 * time.Second}) // production default for strict mode
	e.svc.Now = e.clock
	return e
}

func (e *env) user(t *testing.T, name string) User {
	t.Helper()
	u, err := e.svc.EnsureUser(context.Background(), auth.Identity{Issuer: "dev", Subject: uuid.NewString(), Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *env) org(t *testing.T, owner User, slug string) Org {
	t.Helper()
	o, err := e.svc.CreateOrg(context.Background(), owner, "Org "+slug, slug, "IQ", "Asia/Baghdad")
	if err != nil {
		t.Fatal(err)
	}
	return o
}

var anchor = &Anchor{Lat: 36.19, Lon: 44.01, AccuracyM: 5}

func (e *env) session(t *testing.T, u User, o Org, a *Anchor) Session {
	t.Helper()
	s, err := e.svc.CreateSession(context.Background(), u, o.ID, CreateSessionInput{Title: "Software Engineering Lecture", Anchor: a})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// strictSession starts a strict-mode session (no venue: GPS from the anchor only).
func (e *env) strictSession(t *testing.T, u User, o Org, a *Anchor) Session {
	t.Helper()
	s, err := e.svc.CreateSession(context.Background(), u, o.ID, CreateSessionInput{Title: "Strict lecture", Anchor: a, Mode: "strict"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) token(t *testing.T, u User, o Org, s Session) string {
	t.Helper()
	q, err := e.svc.SessionQR(context.Background(), u, o.ID, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	return q.Token
}

func near() *GPSInput { return &GPSInput{Lat: 36.19001, Lon: 44.01001, AccuracyM: 8} }

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	ae, ok := err.(*Error)
	if !ok || ae.Code != code {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

func TestHappyPathAndDefaults(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "Prof. أحمد")
	o := e.org(t, prof, "uni")
	s := e.session(t, prof, o, anchor)

	if d := s.EndsAt.Sub(s.StartsAt); d != 30*time.Minute {
		t.Fatalf("default duration %v", d)
	}
	if s.GPSRadiusM != 20 || s.QRRotationS != 5 || s.Status != "active" || s.VerificationMode != "standard" {
		t.Fatalf("defaults: %+v", s)
	}
	stu := e.user(t, "ئاوات کوردی")
	res, err := e.svc.CheckIn(ctx, stu, CheckInInput{Token: e.token(t, prof, o, s), GPS: near()})
	if err != nil || res.Status != "verified" || res.AlreadyRecorded {
		t.Fatalf("%+v %v", res, err)
	}
	// same account again: idempotent, no second record
	res2, err := e.svc.CheckIn(ctx, stu, CheckInInput{Token: e.token(t, prof, o, s), GPS: near()})
	if err != nil || !res2.AlreadyRecorded || *res2.RecordID != *res.RecordID {
		t.Fatalf("%+v %v", res2, err)
	}
	got, _ := e.svc.GetSession(ctx, prof, o.ID, s.ID)
	if got.Verified != 1 {
		t.Fatalf("count %d", got.Verified)
	}
}

func TestGPSOutcomes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "gps")
	s, _ := e.strictVenueSession(t, prof, o)
	tok := e.token(t, prof, o, s)
	photo := testPhoto(1, 20, 6) // the right place; isolates the GPS outcome

	cases := []struct {
		name string
		gps  *GPSInput
		want string
	}{
		{"far away rejected", &GPSInput{Lat: 36.30, Lon: 44.01, AccuracyM: 10}, "rejected"},
		{"no gps goes to review", nil, "needs_review"},
		{"stale fix goes to review", &GPSInput{Lat: 36.19001, Lon: 44.01001, AccuracyM: 8, FixAgeMs: 600000}, "needs_review"},
		{"poor accuracy not rejected", &GPSInput{Lat: 36.19001, Lon: 44.01001, AccuracyM: 300}, "needs_review"},
	}
	for _, c := range cases {
		u := e.user(t, c.name)
		r, err := e.svc.CheckIn(ctx, u, CheckInInput{Token: tok, GPS: c.gps, Photo: photo})
		if err != nil || r.Status != c.want {
			t.Errorf("%s: %+v %v", c.name, r, err)
		}
	}
	// a rejected attempt leaves an audit row but no attendance record
	var recs, atts int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM attendance_records`).Scan(&recs)
	e.pool.QueryRow(ctx, `SELECT count(*) FROM verification_attempts`).Scan(&atts)
	if recs != 3 || atts != 4 {
		t.Fatalf("records=%d attempts=%d", recs, atts)
	}
	// a later good attempt upgrades needs_review to verified
	u := e.user(t, "upgrade")
	e.svc.CheckIn(ctx, u, CheckInInput{Token: tok, Photo: photo})
	r, _ := e.svc.CheckIn(ctx, u, CheckInInput{Token: tok, GPS: near(), Photo: photo})
	if r.Status != "verified" {
		t.Fatalf("upgrade: %+v", r)
	}
}

func TestNoAnchorIsHonest(t *testing.T) {
	e := newEnv(t)
	prof := e.user(t, "P")
	o := e.org(t, prof, "noanchor")
	s := e.strictSession(t, prof, o, nil)
	r, err := e.svc.CheckIn(context.Background(), e.user(t, "S"), CheckInInput{Token: e.token(t, prof, o, s), GPS: near()})
	if err != nil || r.Status != "needs_review" || r.Reasons[0] != "no_session_anchor" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestQRAndSessionLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "life")
	s := e.session(t, prof, o, anchor)
	tok := e.token(t, prof, o, s)
	stu := e.user(t, "S")

	// forged / garbage tokens
	_, err := e.svc.CheckIn(ctx, stu, CheckInInput{Token: "garbage"})
	wantCode(t, err, "invalid_qr")
	_, err = e.svc.CheckIn(ctx, stu, CheckInInput{Token: qr.Sign([]byte("wrong-secret-wrong-secret-wrong!!"), s.ID, e.clock(), 10*time.Second)})
	wantCode(t, err, "invalid_qr")
	_, err = e.svc.CheckIn(ctx, stu, CheckInInput{Token: qr.Sign([]byte("x"), uuid.New(), e.clock(), 10*time.Second)})
	wantCode(t, err, "invalid_qr")

	// a token 3 rotation slots old is outside the 2-slot grace
	e.advance(35 * time.Second)
	_, err = e.svc.CheckIn(ctx, stu, CheckInInput{Token: tok, GPS: near()})
	wantCode(t, err, "qr_expired")
	// a fresh one works
	if r, err := e.svc.CheckIn(ctx, stu, CheckInInput{Token: e.token(t, prof, o, s), GPS: near()}); err != nil || r.Status != "verified" {
		t.Fatalf("%+v %v", r, err)
	}

	// closing ends check-ins and the QR feed; closing twice is fine
	tok2 := e.token(t, prof, o, s)
	if _, err := e.svc.CloseSession(ctx, prof, o.ID, s.ID); err != nil {
		t.Fatal(err)
	}
	if c, err := e.svc.CloseSession(ctx, prof, o.ID, s.ID); err != nil || c.Status != "closed" {
		t.Fatalf("%+v %v", c, err)
	}
	_, err = e.svc.CheckIn(ctx, e.user(t, "late"), CheckInInput{Token: tok2, GPS: near()})
	wantCode(t, err, "session_ended")
	_, err = e.svc.SessionQR(ctx, prof, o.ID, s.ID)
	wantCode(t, err, "session_ended")

	// time-based expiry, server clock only
	s2 := e.session(t, prof, o, anchor)
	tok3 := e.token(t, prof, o, s2)
	e.advance(31 * time.Minute)
	_, err = e.svc.CheckIn(ctx, e.user(t, "later"), CheckInInput{Token: tok3, GPS: near()})
	if ae, ok := err.(*Error); !ok || (ae.Code != "session_ended" && ae.Code != "qr_expired") {
		t.Fatalf("expected ended/expired, got %v", err)
	}
	got, _ := e.svc.GetSession(ctx, prof, o.ID, s2.ID)
	if got.Status != "expired" {
		t.Fatalf("status %s", got.Status)
	}
}

func TestConcurrentCheckins(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "load")
	s := e.session(t, prof, o, anchor)
	tok := e.token(t, prof, o, s)

	// 1) the same account hammering one session: exactly one record
	dup := e.user(t, "dup")
	var wg sync.WaitGroup
	var inserted int32
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.svc.CheckIn(ctx, dup, CheckInInput{Token: tok, GPS: near()})
			if err != nil {
				t.Error(err)
				return
			}
			if !r.AlreadyRecorded {
				atomic.AddInt32(&inserted, 1)
			}
		}()
	}
	wg.Wait()
	var n int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM attendance_records WHERE user_id=$1`, dup.ID).Scan(&n)
	if n != 1 || inserted != 1 {
		t.Fatalf("records=%d inserted=%d", n, inserted)
	}

	// 2) 300 different attendees on the same shared QR
	users := make([]User, 300)
	for i := range users {
		users[i] = e.user(t, fmt.Sprintf("student %d", i))
	}
	for _, u := range users {
		wg.Add(1)
		go func(u User) {
			defer wg.Done()
			if r, err := e.svc.CheckIn(ctx, u, CheckInInput{Token: tok, GPS: near()}); err != nil || r.Status != "verified" {
				t.Errorf("%+v %v", r, err)
			}
		}(u)
	}
	wg.Wait()
	got, _ := e.svc.GetSession(ctx, prof, o.ID, s.ID)
	if got.Verified != 301 {
		t.Fatalf("verified=%d want 301", got.Verified)
	}
}

func TestCreateSessionIdempotentUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "idem")
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := e.svc.CreateSession(ctx, prof, o.ID, CreateSessionInput{Title: "Lecture", IdempotencyKey: "k1"})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- s.ID
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[uuid.UUID]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("distinct sessions: %d", len(seen))
	}
}

func TestTenantIsolationAndRoles(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.user(t, "ownerA"), e.user(t, "ownerB")
	oa, ob := e.org(t, a, "aaa"), e.org(t, b, "bbb")
	sa := e.session(t, a, oa, anchor)

	// B cannot see, list, export, QR, close or settle anything in A: all look "not found"
	if _, err := e.svc.GetSession(ctx, b, oa.ID, sa.ID); err != ErrNotFound {
		t.Fatalf("get: %v", err)
	}
	if _, err := e.svc.ListAttendance(ctx, b, oa.ID, sa.ID, 10, nil); err != ErrNotFound {
		t.Fatalf("list: %v", err)
	}
	if _, err := e.svc.SessionQR(ctx, b, oa.ID, sa.ID); err != ErrNotFound {
		t.Fatalf("qr: %v", err)
	}
	if err := e.svc.ExportCSV(ctx, b, oa.ID, sa.ID, &bytes.Buffer{}); err != ErrNotFound {
		t.Fatalf("csv: %v", err)
	}
	// B's own org with A's session id must not leak it either
	if _, err := e.svc.GetSession(ctx, b, ob.ID, sa.ID); err != ErrNotFound {
		t.Fatalf("cross: %v", err)
	}
	if _, err := e.svc.CloseSession(ctx, b, ob.ID, sa.ID); err != ErrNotFound {
		t.Fatalf("cross close: %v", err)
	}

	// roles
	org, rev, rep := e.user(t, "organizer"), e.user(t, "reviewer"), e.user(t, "reporter")
	for u, r := range map[*User]Role{&org: RoleOrganizer, &rev: RoleReviewer, &rep: RoleReporter} {
		if err := e.svc.SetMember(ctx, a, oa.ID, u.ID, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.CreateSession(ctx, rev, oa.ID, CreateSessionInput{Title: "x"}); err != ErrForbidden {
		t.Fatalf("reviewer create: %v", err)
	}
	if _, err := e.svc.CreateSession(ctx, rep, oa.ID, CreateSessionInput{Title: "x"}); err != ErrForbidden {
		t.Fatalf("reporter create: %v", err)
	}
	if err := e.svc.SetMember(ctx, org, oa.ID, rev.ID, RoleAdmin); err != ErrForbidden {
		t.Fatalf("organizer manage: %v", err)
	}
	if _, err := e.svc.UpdateSettings(ctx, org, oa.ID, Settings{}); err != ErrForbidden {
		t.Fatalf("organizer settings: %v", err)
	}
	// organizer can't close someone else's session; owner can
	if _, err := e.svc.CloseSession(ctx, org, oa.ID, sa.ID); err != ErrForbidden {
		t.Fatalf("organizer close other's: %v", err)
	}
	// contacts hidden from reviewer, shown to reporter
	stu := e.user(t, "stu")
	e.pool.Exec(ctx, `UPDATE users SET email='s@example.com' WHERE id=$1`, stu.ID)
	stu.Email = ptr("s@example.com")
	if _, err := e.svc.CheckIn(ctx, stu, CheckInInput{Token: e.token(t, a, oa, sa), GPS: near()}); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.svc.ListAttendance(ctx, rev, oa.ID, sa.ID, 10, nil)
	if len(rows) != 1 || rows[0].Email != nil {
		t.Fatalf("reviewer saw contact: %+v", rows)
	}
	rows, _ = e.svc.ListAttendance(ctx, rep, oa.ID, sa.ID, 10, nil)
	if rows[0].Email == nil {
		t.Fatal("reporter should see contacts")
	}
	// removed member loses access immediately
	if err := e.svc.RemoveMember(ctx, a, oa.ID, org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetSession(ctx, org, oa.ID, sa.ID); err != ErrNotFound {
		t.Fatalf("removed member: %v", err)
	}
}

func ptr(s string) *string { return &s }

func TestOwnershipRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, admin, other := e.user(t, "o"), e.user(t, "a"), e.user(t, "x")
	o := e.org(t, owner, "own")
	e.svc.SetMember(ctx, owner, o.ID, admin.ID, RoleAdmin)

	wantCode(t, e.svc.SetMember(ctx, owner, o.ID, owner.ID, RoleAdmin), "last_owner")
	wantCode(t, e.svc.RemoveMember(ctx, owner, o.ID, owner.ID), "last_owner")
	if err := e.svc.SetMember(ctx, admin, o.ID, other.ID, RoleOwner); err != ErrForbidden {
		t.Fatalf("admin granting owner: %v", err)
	}
	if err := e.svc.RemoveMember(ctx, admin, o.ID, owner.ID); err != ErrForbidden {
		t.Fatalf("admin removing owner: %v", err)
	}
	wantCode(t, e.svc.DeleteAccount(ctx, owner), "sole_owner")
	wantCode(t, func() error { _, err := e.svc.CreateOrg(ctx, owner, "Dup", "own", "", ""); return err }(), "slug_taken")
}

func TestReviewManualAndAudit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "rev")
	s := e.strictSession(t, prof, o, anchor)
	stu := e.user(t, "S")
	r, _ := e.svc.CheckIn(ctx, stu, CheckInInput{Token: e.token(t, prof, o, s)}) // strict, no gps -> needs_review

	if err := e.svc.ReviewRecord(ctx, prof, o.ID, *r.RecordID, "verified", "ab"); err == nil {
		t.Fatal("short reason accepted")
	}
	if err := e.svc.ReviewRecord(ctx, prof, o.ID, *r.RecordID, "revoked", "valid reason"); err == nil {
		t.Fatal("needs_review -> revoked must be refused")
	}
	if err := e.svc.ReviewRecord(ctx, prof, o.ID, *r.RecordID, "verified", "seen in room"); err != nil {
		t.Fatal(err)
	}
	// manual: reason required, flagged as manual, duplicate refused
	broken := e.user(t, "broken camera")
	if _, err := e.svc.ManualAttendance(ctx, prof, o.ID, s.ID, broken.ID, ""); err == nil {
		t.Fatal("manual without reason")
	}
	if _, err := e.svc.ManualAttendance(ctx, prof, o.ID, s.ID, broken.ID, "camera broken"); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.ManualAttendance(ctx, prof, o.ID, s.ID, broken.ID, "again")
	wantCode(t, err, "already_recorded")
	var manual int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM attendance_records WHERE method='manual' AND manual_by=$1`, prof.ID).Scan(&manual)
	if manual != 1 {
		t.Fatalf("manual rows %d", manual)
	}
	var audits int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE org_id=$1 AND action IN ('attendance.review','attendance.manual')`, o.ID).Scan(&audits)
	if audits != 2 {
		t.Fatalf("audits %d", audits)
	}
}

func TestCSVExport(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "csv")
	s := e.session(t, prof, o, anchor)
	tok := e.token(t, prof, o, s)
	for _, name := range []string{"=HYPERLINK(\"http://evil\")", "محمد علي", "ئاوات", "O'Brien, Jr."} {
		if _, err := e.svc.CheckIn(ctx, e.user(t, name), CheckInInput{Token: tok, GPS: near()}); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := e.svc.ExportCSV(ctx, prof, o.ID, s.ID, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\xEF\xBB\xBF") {
		t.Fatal("missing UTF-8 BOM")
	}
	for _, want := range []string{"محمد علي", "ئاوات", `"O'Brien, Jr."`, "'=HYPERLINK", "+03:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("csv missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\n=HYPERLINK") || strings.Contains(out, ",=HYPERLINK") || strings.Contains(out, "\"=HYPERLINK") {
		t.Fatal("formula not neutralised")
	}
}

func TestProfileAndAccountDeletion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "del")
	s := e.session(t, prof, o, anchor)
	stu := e.user(t, "Leaving Student")

	phone := "+964 750 123 4567"
	u, err := e.svc.UpdateProfile(ctx, stu, nil, &phone)
	if err != nil || u.Phone == nil || *u.Phone != "+9647501234567" {
		t.Fatalf("%v %v", u, err)
	}
	bad := "0750123"
	if _, err := e.svc.UpdateProfile(ctx, u, nil, &bad); err == nil {
		t.Fatal("national-format phone accepted without country")
	}
	name := "bad\u202Ename"
	if _, err := e.svc.UpdateProfile(ctx, u, &name, nil); err == nil {
		t.Fatal("bidi override accepted")
	}
	if _, err := e.svc.CheckIn(ctx, u, CheckInInput{Token: e.token(t, prof, o, s), GPS: near()}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteAccount(ctx, u); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.svc.ListAttendance(ctx, prof, o.ID, s.ID, 10, nil)
	if len(rows) != 1 || rows[0].FullName != "Deleted user" || rows[0].Phone != nil {
		t.Fatalf("personal data survived: %+v", rows)
	}
	if _, err := e.svc.UserByID(ctx, u.ID); err != ErrNotFound {
		t.Fatalf("deleted user still resolvable: %v", err)
	}
}

func TestSettingsSnapshotPerSession(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "snap")
	s1 := e.session(t, prof, o, anchor)
	st, _ := e.svc.GetSettings(ctx, prof, o.ID)
	st.GPSRadiusM, st.VerificationMode = 100, "strict"
	if _, err := e.svc.UpdateSettings(ctx, prof, o.ID, st); err != nil {
		t.Fatal(err)
	}
	s2 := e.session(t, prof, o, anchor)
	g1, _ := e.svc.GetSession(ctx, prof, o.ID, s1.ID)
	if g1.GPSRadiusM != 20 || g1.VerificationMode != "standard" {
		t.Fatalf("running session changed: %+v", g1)
	}
	if s2.GPSRadiusM != 100 || s2.VerificationMode != "strict" {
		t.Fatalf("new session: %+v", s2)
	}
	// strict mode with no visual module yet: honest routing to review
	r, err := e.svc.CheckIn(ctx, e.user(t, "S"), CheckInInput{Token: e.token(t, prof, o, s2), GPS: near()})
	if err != nil || r.Status != "needs_review" {
		t.Fatalf("%+v %v", r, err)
	}
	st.GPSRadiusM = 1
	if _, err := e.svc.UpdateSettings(ctx, prof, o.ID, st); err == nil {
		t.Fatal("out-of-range radius accepted")
	}
}
