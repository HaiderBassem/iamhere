package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iamhere/api/internal/app"
)

func TestInvitesAndVenuesOverHTTP(t *testing.T) {
	ts := newServer(t)
	owner := client{t, ts.URL, "dev:own-1:Owner"}
	guest := client{t, ts.URL, "dev:guest-1:Guest"}
	stranger := client{t, ts.URL, "dev:str-1:Stranger"}

	var org app.Org
	owner.json("POST", "/v1/orgs", map[string]any{"name": "Acme", "slug": "acme"}, 201, &org)
	base := "/v1/orgs/" + org.ID.String()

	// venue + title-only session binds to it
	var venue app.Venue
	owner.json("POST", base+"/venues", map[string]any{"name": "Main hall", "lat": 36.19, "lon": 44.01, "radius_m": 40}, 201, &venue)
	owner.json("POST", base+"/venues", map[string]any{"name": "X", "lat": 99, "lon": 0}, 400, nil)
	var sess app.Session
	owner.json("POST", base+"/sessions", map[string]any{"title": "Title only"}, 201, &sess)
	if sess.LocationSource != "sole_venue" || sess.GPSRadiusM != 40 {
		t.Fatalf("%+v", sess)
	}
	owner.json("POST", base+"/sessions", map[string]any{"title": "Bad venue", "venue_id": "00000000-0000-0000-0000-000000000001"}, 400, nil)
	var vs struct{ Items []app.Venue }
	owner.json("GET", base+"/venues", nil, 200, &vs)
	if len(vs.Items) != 1 || vs.Items[0].Lat != 36.19 || vs.Items[0].Lon != 44.01 {
		t.Fatalf("%+v", vs)
	}

	// invite -> accept -> role in effect
	var inv app.Invite
	owner.json("POST", base+"/invites", map[string]any{"role": "organizer", "expires_in_hours": 24}, 201, &inv)
	if inv.Token == "" {
		t.Fatal("no token returned at creation")
	}
	guest.json("GET", base+"/sessions", nil, 404, nil) // not a member yet
	guest.json("POST", base+"/invites", map[string]any{"role": "reporter"}, 404, nil)
	var joined app.Org
	guest.json("POST", "/v1/invites/accept", map[string]any{"token": inv.Token}, 200, &joined)
	if joined.ID != org.ID || joined.Role != app.RoleOrganizer {
		t.Fatalf("%+v", joined)
	}
	guest.json("GET", base+"/sessions", nil, 200, nil)
	guest.json("POST", base+"/venues", map[string]any{"name": "Nope", "lat": 1, "lon": 1}, 403, nil) // organizer cannot manage venues
	stranger.json("POST", "/v1/invites/accept", map[string]any{"token": inv.Token}, 404, nil)        // single use
	stranger.json("POST", "/v1/invites/accept", map[string]any{"token": "iah_inv_nope"}, 404, nil)
	owner.json("POST", base+"/invites", map[string]any{"role": "owner"}, 400, nil)
	owner.json("POST", base+"/invites", map[string]any{"role": "reviewer", "expires_in_hours": 99999}, 400, nil)

	// listing hides secrets; revocation works; venue deletion keeps old sessions
	var open struct{ Items []app.Invite }
	owner.json("POST", base+"/invites", map[string]any{"role": "reporter"}, 201, &inv)
	owner.json("GET", base+"/invites", nil, 200, &open)
	if len(open.Items) != 1 || open.Items[0].Token != "" {
		t.Fatalf("%+v", open)
	}
	owner.json("DELETE", base+"/invites/"+inv.ID.String(), nil, 204, nil)
	owner.json("DELETE", base+"/venues/"+venue.ID.String(), nil, 204, nil)
	owner.json("DELETE", base+"/venues/"+venue.ID.String(), nil, 404, nil)
	owner.json("GET", base+"/sessions/"+sess.ID.String(), nil, 200, nil)
}

func TestInviteAcceptRateLimited(t *testing.T) {
	ts := newServer(t)
	u := client{t, ts.URL, "dev:guesser-1:Guesser"}
	var limited bool
	for i := 0; i < 30 && !limited; i++ {
		code, _ := u.do("POST", "/v1/invites/accept", map[string]any{"token": "iah_inv_guess"})
		limited = code == 429
	}
	if !limited {
		t.Fatal("invite redemption is not rate limited")
	}
}

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	h := cors([]string{"https://app.example.org"})(next)
	do := func(method, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/v1/me", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := do("OPTIONS", "https://app.example.org"); rec.Code != 204 ||
		rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.org" ||
		rec.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}
	if rec := do("GET", "https://app.example.org"); rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.org" {
		t.Fatalf("simple: %d %v", rec.Code, rec.Header())
	}
	if rec := do("OPTIONS", "https://evil.example"); rec.Code != 403 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign preflight: %d %v", rec.Code, rec.Header())
	}
	if rec := do("GET", "https://evil.example"); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("foreign origin was allowed")
	}
	if rec := do("GET", ""); rec.Code != 200 || rec.Header().Get("Vary") != "" {
		t.Fatalf("same-origin: %d %v", rec.Code, rec.Header())
	}
	// disabled when no origins are configured
	off := cors(nil)(next)
	req := httptest.NewRequest("OPTIONS", "/v1/me", nil)
	req.Header.Set("Origin", "https://app.example.org")
	rec := httptest.NewRecorder()
	off.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("cors enabled without configuration")
	}
}

// A handler that aborts mid-stream (truncated CSV) must reach the client as a
// broken response, not as a cleanly terminated one.
func TestRecovererPreservesAbort(t *testing.T) {
	s := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ts := httptest.NewServer(s.recoverer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boom" {
			panic("unexpected")
		}
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	})))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/abort")
	if err == nil {
		_, err = io.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("aborted response looked complete")
	}
	resp, err = http.Get(ts.URL + "/boom")
	if err != nil || resp.StatusCode != 500 {
		t.Fatalf("ordinary panic must be a 500: %v %v", resp, err)
	}
	resp.Body.Close()
}
