package httpapi

import (
	"net/url"
	"strings"
	"testing"

	"github.com/iamhere/api/internal/app"
)

// liveCode is the code currently on an organizer's screen.
func liveCode(c client, org app.Org, s app.Session) string {
	var q app.QRInfo
	c.json("GET", "/v1/orgs/"+org.ID.String()+"/sessions/"+s.ID.String()+"/qr", nil, 200, &q)
	return q.Token
}

// A person without an account scans, signs up, and is checked in without scanning again.
func TestSignUpKeepsTheScan(t *testing.T) {
	ts := newServer(t)
	anon := client{t, ts.URL, ""}
	owner := client{t, ts.URL, "dev:claim-owner:Lecturer"}
	var org app.Org
	owner.json("POST", "/v1/orgs", map[string]any{"name": "Claim Org", "slug": "claim-org"}, 201, &org)
	var sess app.Session
	owner.json("POST", "/v1/orgs/"+org.ID.String()+"/sessions", map[string]any{"title": "Algebra"}, 201, &sess)

	// only a real, current code can be held
	code, b := anon.do("POST", "/v1/checkins/claim", map[string]any{"token": "nonsense"})
	if code != 400 || !strings.Contains(string(b), "invalid_qr") {
		t.Fatalf("bad code: %d %s", code, b)
	}
	var cl app.ClaimResult
	anon.json("POST", "/v1/checkins/claim", map[string]any{"token": liveCode(owner, org, sess)}, 200, &cl)
	if cl.Claim == "" || cl.SessionTitle != "Algebra" || cl.OrganizationName != "Claim Org" || cl.ExpiresInS != 300 {
		t.Fatalf("%+v", cl)
	}
	// a claim is not a code: it cannot check anyone in directly
	someone := client{t, ts.URL, "dev:claim-someone:Someone"}
	if code, _ := someone.do("POST", "/v1/checkins", map[string]any{"token": cl.Claim}); code != 400 {
		t.Fatalf("claim used as a code: %d", code)
	}

	// sign up with the claim: the check-in continues with a ticket, no second scan
	var su app.SignedIn
	anon.json("POST", "/v1/auth/signup", map[string]any{"name": "Lana", "email": "lana@example.org", "password": "blue-river-seven", "claim": cl.Claim}, 201, &su)
	if su.CheckIn == nil || su.CheckIn.Ticket == "" || su.CheckIn.SessionTitle != "Algebra" || su.CheckIn.NeedsGPS || su.CheckIn.NeedsPhoto {
		t.Fatalf("%+v", su)
	}
	lana := client{t, ts.URL, su.Token}
	var res app.CheckInResult
	lana.json("POST", "/v1/checkins", map[string]any{"ticket": su.CheckIn.Ticket}, 200, &res)
	if res.Status != "verified" {
		t.Fatalf("%+v", res)
	}

	// a tampered claim still creates the account, and says why the check-in did not continue
	var su2 app.SignedIn
	anon.json("POST", "/v1/auth/signup", map[string]any{"name": "Omar", "email": "omar@example.org", "password": "blue-river-seven", "claim": cl.Claim[:len(cl.Claim)-2] + "AA"}, 201, &su2)
	if su2.Token == "" || su2.CheckIn != nil || su2.CheckInError == nil || su2.CheckInError.Code != "invalid_qr" {
		t.Fatalf("%+v", su2)
	}
	// once the session is closed, a held scan no longer counts
	owner.json("POST", "/v1/orgs/"+org.ID.String()+"/sessions/"+sess.ID.String()+"/close", nil, 200, nil)
	var su3 app.SignedIn
	anon.json("POST", "/v1/auth/signup", map[string]any{"name": "Zainab", "email": "zainab@example.org", "password": "blue-river-seven", "claim": cl.Claim}, 201, &su3)
	if su3.CheckIn != nil || su3.CheckInError == nil || su3.CheckInError.Code != "session_ended" {
		t.Fatalf("%+v", su3)
	}
}

// Start on the home screen needs no organization: one is found or made.
func TestWorkspace(t *testing.T) {
	ts := newServer(t)
	newbie := client{t, ts.URL, "dev:ws-new:Sara Ahmed"}
	var w1, w2 app.Org
	newbie.json("POST", "/v1/workspace", map[string]any{"timezone": "Asia/Baghdad"}, 200, &w1)
	if w1.Name != "Sara Ahmed" || w1.Role != app.RoleOwner || !strings.HasPrefix(w1.Slug, "p-") || w1.Timezone != "Asia/Baghdad" {
		t.Fatalf("%+v", w1)
	}
	newbie.json("POST", "/v1/workspace", map[string]any{"timezone": "Not/AZone"}, 200, &w2)
	if w2.ID != w1.ID {
		t.Fatalf("a second Start made a second workspace: %+v %+v", w1, w2)
	}
	var s app.Session
	newbie.json("POST", "/v1/orgs/"+w1.ID.String()+"/sessions", map[string]any{"title": "Lecture"}, 201, &s)

	// someone who already organizes for a team starts there
	owner := client{t, ts.URL, "dev:ws-owner:Dean"}
	var team app.Org
	owner.json("POST", "/v1/orgs", map[string]any{"name": "Faculty", "slug": "ws-faculty"}, 201, &team)
	for _, c := range []struct {
		sub, role string
		same      bool
	}{{"ws-organizer", "organizer", true}, {"ws-reviewer", "reviewer", false}} {
		var inv app.Invite
		owner.json("POST", "/v1/orgs/"+team.ID.String()+"/invites", map[string]any{"role": c.role}, 201, &inv)
		m := client{t, ts.URL, "dev:" + c.sub + ":Member"}
		m.json("POST", "/v1/invites/accept", map[string]any{"token": inv.Token}, 200, nil)
		var w app.Org
		m.json("POST", "/v1/workspace", map[string]any{}, 200, &w)
		if (w.ID == team.ID) != c.same {
			t.Fatalf("%s: %+v", c.role, w)
		}
	}
}

// The registrar's view: attendance per person over a period, and adding someone by email.
func TestReportAndAddByEmail(t *testing.T) {
	ts := newServer(t)
	anon := client{t, ts.URL, ""}
	owner := client{t, ts.URL, "dev:rep-owner:Lecturer"}
	var org app.Org
	owner.json("POST", "/v1/orgs", map[string]any{"name": "Report Org", "slug": "report-org"}, 201, &org)
	people := map[string]client{}
	for _, n := range []string{"amal", "bilal"} {
		var su app.SignedIn
		anon.json("POST", "/v1/auth/signup", map[string]any{"name": strings.ToUpper(n[:1]) + n[1:], "email": n + "@example.org", "password": "blue-river-seven"}, 201, &su)
		people[n] = client{t, ts.URL, su.Token}
	}
	base := "/v1/orgs/" + org.ID.String()
	var sessions []app.Session
	for _, title := range []string{"Algebra", "Algebra", "Physics"} {
		var s app.Session
		owner.json("POST", base+"/sessions", map[string]any{"title": title}, 201, &s)
		sessions = append(sessions, s)
		people["amal"].json("POST", "/v1/checkins", map[string]any{"token": liveCode(owner, org, s)}, 200, nil)
	}
	people["bilal"].json("POST", "/v1/checkins", map[string]any{"token": liveCode(owner, org, sessions[0])}, 200, nil)

	// Bilal's phone died in Physics: the organizer adds him by email
	physics := base + "/sessions/" + sessions[2].ID.String() + "/attendance/manual"
	owner.json("POST", physics, map[string]any{"email": " Bilal@Example.org", "reason": "Phone not working"}, 201, nil)
	owner.json("POST", physics, map[string]any{"email": "bilal@example.org", "reason": "Phone not working"}, 409, nil)
	code, b := owner.do("POST", physics, map[string]any{"email": "nobody@example.org", "reason": "Phone not working"})
	if code != 404 || !strings.Contains(string(b), "no_account") {
		t.Fatalf("unknown email: %d %s", code, b)
	}
	// only organizers may look people up
	if code, _ := people["amal"].do("POST", physics, map[string]any{"email": "bilal@example.org", "reason": "Friend"}); code != 404 {
		t.Fatalf("attendee added someone: %d", code)
	}

	var rep app.Report
	owner.json("GET", base+"/report", nil, 200, &rep)
	if rep.Sessions != 3 || len(rep.People) != 2 || len(rep.Titles) != 2 {
		t.Fatalf("%+v", rep)
	}
	byName := map[string]app.ReportRow{}
	for _, p := range rep.People {
		byName[p.FullName] = p
	}
	if a := byName["Amal"]; a.Attended != 3 || a.Rate != 1 || a.Email == nil {
		t.Fatalf("%+v", a)
	}
	if b := byName["Bilal"]; b.Attended != 2 { // one scan + one added by hand
		t.Fatalf("%+v", b)
	}
	owner.json("GET", base+"/report?title="+url.QueryEscape("Algebra"), nil, 200, &rep)
	if rep.Sessions != 2 || rep.Title != "Algebra" {
		t.Fatalf("%+v", rep)
	}
	for _, p := range rep.People {
		if p.FullName == "Bilal" && p.Rate != 0.5 {
			t.Fatalf("%+v", p)
		}
	}
	// a period in the past has no sessions
	owner.json("GET", base+"/report?from=2020-01-01&to=2020-01-31", nil, 200, &rep)
	if rep.Sessions != 0 || len(rep.People) != 0 {
		t.Fatalf("%+v", rep)
	}
	for _, q := range []string{"?from=yesterday", "?from=2026-02-01&to=2026-01-01", "?from=2020-01-01&to=2026-01-01"} {
		if code, _ := owner.do("GET", base+"/report"+q, nil); code != 400 {
			t.Errorf("%s: %d", q, code)
		}
	}
	code, b = owner.do("GET", base+"/report.csv", nil)
	if code != 200 || !strings.HasPrefix(string(b), "\xEF\xBB\xBFFull Name,Email,Attended,Sessions") || !strings.Contains(string(b), "Amal,amal@example.org,3,3,100,0,") {
		t.Fatalf("csv: %d %s", code, b)
	}
	// people outside the organization see nothing
	if code, _ := people["amal"].do("GET", base+"/report", nil); code != 404 {
		t.Fatalf("outsider report: %d", code)
	}
}
