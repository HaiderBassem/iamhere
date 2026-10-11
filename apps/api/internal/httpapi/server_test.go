package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iamhere/api/internal/app"
	"github.com/iamhere/api/internal/auth"
	"github.com/iamhere/api/internal/db"
)

func adminURL() string {
	if v := os.Getenv("TEST_PG_ADMIN_URL"); v != "" {
		return v
	}
	return "postgres://postgres:dev@localhost:55432/postgres?sslmode=disable"
}

func newServer(t *testing.T) *httptest.Server {
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
	name := "iah_h_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(adminURL())
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	svc := app.New(pool, app.Options{QRGraceSlots: 2, CheckinGrace: 5 * time.Second, MaxFixAge: time.Minute, Vision: newFakeVision(t).client})
	h := New(svc, auth.Dev{}, slog.New(slog.NewTextHandler(io.Discard, nil)), pool.Ping, Options{})
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		pool.Close()
		admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close()
	})
	return ts
}

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c client) do(method, path string, body any, hdr ...string) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (c client) json(method, path string, body any, want int, out any, hdr ...string) {
	c.t.Helper()
	code, b := c.do(method, path, body, hdr...)
	if code != want {
		c.t.Fatalf("%s %s: status %d want %d: %s", method, path, code, want, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			c.t.Fatalf("decode %s: %v", b, err)
		}
	}
}

func TestEndToEndJourney(t *testing.T) {
	ts := newServer(t)
	prof := client{t, ts.URL, "dev:prof-1:Professor أحمد"}
	stu := client{t, ts.URL, "dev:stu-1:ئاوات کوردی"}
	other := client{t, ts.URL, "dev:other-1:Other Owner"}
	anon := client{t, ts.URL, ""}

	// unauthenticated and malformed credentials
	if code, _ := anon.do("GET", "/v1/me", nil); code != 401 {
		t.Fatalf("anon: %d", code)
	}
	if code, _ := (client{t, ts.URL, "garbage"}).do("GET", "/v1/me", nil); code != 401 {
		t.Fatalf("garbage token: %d", code)
	}
	if code, _ := anon.do("GET", "/healthz", nil); code != 200 {
		t.Fatalf("healthz: %d", code)
	}
	if code, _ := anon.do("GET", "/readyz", nil); code != 200 {
		t.Fatalf("readyz: %d", code)
	}

	// organization + title-only session
	var org app.Org
	prof.json("POST", "/v1/orgs", map[string]any{"name": "University of Technology", "slug": "uot", "country": "IQ", "timezone": "Asia/Baghdad"}, 201, &org)
	base := "/v1/orgs/" + org.ID.String()

	var sess app.Session
	prof.json("POST", base+"/sessions", map[string]any{"title": "Software Engineering Lecture", "anchor": map[string]any{"lat": 36.19, "lon": 44.01, "accuracy_m": 5}}, 201, &sess, "Idempotency-Key", "abc")
	var again app.Session
	prof.json("POST", base+"/sessions", map[string]any{"title": "Software Engineering Lecture"}, 201, &again, "Idempotency-Key", "abc")
	if again.ID != sess.ID {
		t.Fatal("idempotency key did not return the same session")
	}

	var q app.QRInfo
	prof.json("GET", base+"/sessions/"+sess.ID.String()+"/qr", nil, 200, &q)
	if q.Token == "" || q.RotationS != 5 {
		t.Fatalf("%+v", q)
	}

	// live stream sends an initial snapshot
	req, _ := http.NewRequest("GET", ts.URL+base+"/sessions/"+sess.ID.String()+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+prof.token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %s", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	var sawCounts bool
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "event: counts") {
			sawCounts = true
			break
		}
	}
	if !sawCounts {
		t.Fatal("no initial counts event")
	}

	// attendee checks in; bad input is rejected strictly
	gps := map[string]any{"lat": 36.19001, "lon": 44.01001, "accuracy_m": 8, "fix_age_ms": 500}
	var res app.CheckInResult
	stu.json("POST", "/v1/checkins", map[string]any{"token": q.Token, "gps": gps}, 200, &res)
	if res.Status != "verified" || res.OrganizationName != "University of Technology" {
		t.Fatalf("%+v", res)
	}
	stu.json("POST", "/v1/checkins", map[string]any{"token": q.Token, "extra": 1}, 400, nil)
	stu.json("POST", "/v1/checkins", map[string]any{"token": "nope"}, 400, nil)
	stu.json("POST", "/v1/checkins", map[string]any{"token": q.Token, "gps": map[string]any{"lat": 999, "lon": 0, "accuracy_m": 1}}, 400, nil)

	// tenant isolation over HTTP: another org's owner sees 404, never 403 or data
	var other1 app.Org
	other.json("POST", "/v1/orgs", map[string]any{"name": "Other", "slug": "other"}, 201, &other1)
	for _, p := range []string{"/sessions/" + sess.ID.String(), "/sessions/" + sess.ID.String() + "/attendance", "/sessions/" + sess.ID.String() + "/qr", "/sessions/" + sess.ID.String() + "/attendance.csv"} {
		if code, _ := other.do("GET", base+p, nil); code != 404 {
			t.Fatalf("cross-tenant %s: %d", p, code)
		}
	}
	// the attendee is not a member either
	if code, _ := stu.do("GET", base+"/sessions", nil); code != 404 {
		t.Fatalf("attendee listing sessions: %d", code)
	}
	if code, _ := prof.do("GET", base+"/sessions/not-a-uuid", nil); code != 404 {
		t.Fatalf("bad id: %d", code)
	}

	// organizer sees the attendee; CSV downloads with BOM and Unicode intact
	var list struct{ Items []app.AttendanceRow }
	prof.json("GET", base+"/sessions/"+sess.ID.String()+"/attendance", nil, 200, &list)
	if len(list.Items) != 1 || list.Items[0].FullName != "ئاوات کوردی" {
		t.Fatalf("%+v", list)
	}
	code, csvBody := prof.do("GET", base+"/sessions/"+sess.ID.String()+"/attendance.csv", nil)
	if code != 200 || !bytes.HasPrefix(csvBody, []byte("\xEF\xBB\xBF")) || !bytes.Contains(csvBody, []byte("ئاوات کوردی")) {
		t.Fatalf("csv: %d %q", code, csvBody)
	}

	// close, then late check-in is refused
	prof.json("POST", base+"/sessions/"+sess.ID.String()+"/close", nil, 200, nil)
	late := client{t, ts.URL, "dev:late-1:Late"}
	late.json("POST", "/v1/checkins", map[string]any{"token": q.Token, "gps": gps}, 410, nil)

	// profile + personal history + account deletion
	var me app.User
	stu.json("PATCH", "/v1/me", map[string]any{"phone": "+964 750 123 4567"}, 200, &me)
	if me.Phone == nil || *me.Phone != "+9647501234567" {
		t.Fatalf("%+v", me)
	}
	var hist struct{ Items []app.HistoryRow }
	stu.json("GET", "/v1/me/attendance", nil, 200, &hist)
	if len(hist.Items) != 1 || hist.Items[0].SessionTitle != "Software Engineering Lecture" {
		t.Fatalf("%+v", hist)
	}
	stu.json("DELETE", "/v1/me", nil, 204, nil)
}

func TestCheckinRateLimit(t *testing.T) {
	ts := newServer(t)
	u := client{t, ts.URL, "dev:spam-1:Spammer"}
	var got429 bool
	for i := 0; i < 30; i++ {
		code, _ := u.do("POST", "/v1/checkins", map[string]any{"token": "x"})
		if code == 429 {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("check-in endpoint is not rate limited")
	}
}
