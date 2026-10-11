package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/iamhere/api/internal/app"
	"github.com/iamhere/api/internal/vision"
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
	if rec := do("GET", ""); rec.Code != 200 || rec.Header().Get("Vary") != "Origin" || rec.Header().Get("Access-Control-Allow-Origin") != "" {
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

// photoDataURL renders a deterministic synthetic scene as a canvas-style data URL.
func photoDataURL(seed int64, brightness int) string {
	const w, h = 320, 240
	r := rand.New(rand.NewSource(seed))
	type blk struct{ x0, y0, x1, y1, l int }
	var blks []blk
	for i := 0; i < 14; i++ {
		x0, y0 := r.Intn(w*3/4), r.Intn(h*3/4)
		blks = append(blks, blk{x0, y0, x0 + 20 + r.Intn(w/3), y0 + 20 + r.Intn(h/3), r.Intn(256)})
	}
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			l := 80 + y*60/h
			for _, b := range blks {
				if x >= b.x0 && x < b.x1 && y >= b.y0 && y < b.y1 {
					l = b.l
				}
			}
			img.SetGray(x, y, color.Gray{uint8(max(0, min(255, l+brightness)))})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

// fakeVision stands in for the vision service: every room matches, and the
// photo "shows" whatever code text follows "code=" in the probe bytes.
type fakeVision struct {
	client *vision.Client
}

func newFakeVision(t *testing.T) *fakeVision {
	var mu sync.Mutex
	cached := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Image, Probe string
			Refs         []struct{ SHA256, Image string }
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		defer mu.Unlock()
		reply := func(status int, v any) { w.WriteHeader(status); _ = json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case "/v1/reference":
			img, _ := base64.StdEncoding.DecodeString(in.Image)
			h := vision.SHA256(img)
			cached[hex.EncodeToString(h[:])] = true
			reply(200, map[string]int{"width": 320, "height": 240, "keypoints": 1500})
		case "/v1/verify":
			var missing []string
			for _, ref := range in.Refs {
				if ref.Image != "" {
					cached[ref.SHA256] = true
				}
				if !cached[ref.SHA256] {
					missing = append(missing, ref.SHA256)
				}
			}
			if len(missing) > 0 {
				reply(409, map[string]any{"error": "missing_refs", "missing": missing})
				return
			}
			p, _ := base64.StdEncoding.DecodeString(in.Probe)
			res := vision.Result{Codes: []string{}, Matched: len(in.Refs) > 0, Score: 150, Threshold: 40, Model: "fake"}
			if _, code, ok := strings.Cut(string(p), "code="); ok {
				res.Codes = append(res.Codes, code)
			}
			reply(200, res)
		}
	}))
	t.Cleanup(srv.Close)
	return &fakeVision{client: vision.New(srv.URL)}
}

func probeDataURL(token string) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("code=https://attend.example.org/#/c/"+token))
}

func TestStrictFlowOverHTTP(t *testing.T) {
	ts := newServer(t)
	anon := client{t, ts.URL, ""}
	owner := client{t, ts.URL, "dev:own-s:Owner"}
	stu := client{t, ts.URL, "dev:stu-s:Student"}

	// public config needs no sign-in (the web app reads it before login)
	var pc map[string]any
	anon.json("GET", "/v1/public-config", nil, 200, &pc)
	if pc["auth"] == nil {
		t.Fatalf("%v", pc)
	}

	var org app.Org
	owner.json("POST", "/v1/orgs", map[string]any{"name": "Strict Uni", "slug": "strict-uni"}, 201, &org)
	base := "/v1/orgs/" + org.ID.String()
	var venue app.Venue
	owner.json("POST", base+"/venues", map[string]any{"name": "Room 1", "lat": 36.19, "lon": 44.01, "radius_m": 30}, 201, &venue)
	vbase := base + "/venues/" + venue.ID.String()

	// save a place photo; garbage and non-images are refused
	var ref app.ReferencePhoto
	owner.json("POST", vbase+"/references", map[string]any{"photo": photoDataURL(1, 0)}, 201, &ref)
	owner.json("POST", vbase+"/references", map[string]any{"photo": "!!notbase64!!"}, 400, nil)
	owner.json("POST", vbase+"/references", map[string]any{"photo": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("plain text, not a picture at all...."))}, 400, nil)
	var vs struct{ Items []app.Venue }
	owner.json("GET", base+"/venues", nil, 200, &vs)
	if vs.Items[0].ReferenceCount != 1 {
		t.Fatalf("%+v", vs)
	}
	var refs struct{ Items []app.ReferencePhoto }
	owner.json("GET", vbase+"/references", nil, 200, &refs)
	if len(refs.Items) != 1 {
		t.Fatalf("%+v", refs)
	}
	stu.json("GET", vbase+"/references", nil, 404, nil) // not a member

	// the stored image is served back to the organizer only
	code, img := owner.do("GET", base+"/references/"+ref.ID.String()+"/image", nil)
	if code != 200 || len(img) < 100 {
		t.Fatalf("image: %d %d bytes", code, len(img))
	}
	if code, _ := stu.do("GET", base+"/references/"+ref.ID.String()+"/image", nil); code != 404 {
		t.Fatalf("student read a reference image: %d", code)
	}

	// "test from a seat"
	var pt app.PlaceTest
	owner.json("POST", vbase+"/test", map[string]any{"photo": photoDataURL(2, 0)}, 200, &pt)
	if !pt.Matched || pt.References != 1 {
		t.Fatalf("%+v", pt)
	}

	var sess app.Session
	owner.json("POST", base+"/sessions", map[string]any{"title": "Strict", "venue_id": venue.ID, "mode": "strict"}, 201, &sess)
	owner.json("POST", base+"/sessions", map[string]any{"title": "Bad", "mode": "paranoid"}, 400, nil)
	sbase := base + "/sessions/" + sess.ID.String()

	// today's photo for this session
	var today app.ReferencePhoto
	owner.json("POST", sbase+"/references", map[string]any{"photo": photoDataURL(3, 0)}, 201, &today)
	owner.json("GET", sbase+"/references", nil, 200, &refs)
	if len(refs.Items) != 1 {
		t.Fatalf("%+v", refs)
	}

	var q app.QRInfo
	owner.json("GET", sbase+"/qr", nil, 200, &q)
	var sc app.ScanResult
	stu.json("POST", "/v1/checkins/scan", map[string]any{"token": q.Token}, 200, &sc)
	if !sc.NeedsGPS || !sc.NeedsPhoto || sc.Ticket == "" {
		t.Fatalf("%+v", sc)
	}
	gps := map[string]any{"lat": 36.19001, "lon": 44.01001, "accuracy_m": 8, "fix_age_ms": 400}
	hint := [4][2]float64{{10, 10}, {90, 10}, {90, 90}, {10, 90}}
	var res app.CheckInResult
	stu.json("POST", "/v1/checkins", map[string]any{"ticket": sc.Ticket, "gps": gps, "photo": probeDataURL(q.Token), "photo_hint": hint}, 200, &res)
	if res.Status != "verified" || res.Mode != "strict" {
		t.Fatalf("%+v", res)
	}
	// unknown fields are still refused, and an oversized body is not buffered
	stu.json("POST", "/v1/checkins", map[string]any{"ticket": sc.Ticket, "bogus": 1}, 400, nil)
	stu.json("POST", "/v1/checkins", map[string]any{"ticket": sc.Ticket, "photo": strings.Repeat("A", 4<<20)}, 400, nil)
	stu.json("POST", "/v1/checkins/scan", map[string]any{"token": "nope"}, 400, nil)

	owner.json("DELETE", sbase+"/references/"+today.ID.String(), nil, 204, nil)
	owner.json("DELETE", vbase+"/references/"+ref.ID.String(), nil, 204, nil)
	owner.json("DELETE", vbase+"/references/"+ref.ID.String(), nil, 404, nil)
}

func TestWebAppHosting(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><title>app</title>"), 0o644)
	os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644)
	secret := filepath.Join(filepath.Dir(dir), "outside-secret.txt")
	os.WriteFile(secret, []byte("TOP SECRET"), 0o644)

	h := webApp(dir, cspFor("https://auth.example.org/realms/x"))
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if rec := get("/app.js"); rec.Code != 200 || rec.Body.String() != "console.log(1)" {
		t.Fatalf("static file: %d %q", rec.Code, rec.Body)
	}
	// client-side routes fall back to the app shell, with the security headers
	rec := get("/o/123/team")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>app</title>") {
		t.Fatalf("spa fallback: %d %q", rec.Code, rec.Body)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "https://auth.example.org", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("CSP too loose: %s", csp)
	}
	// unknown API paths are JSON 404s, never the app shell
	if rec := get("/v1/nope"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "not_found") {
		t.Fatalf("api 404: %d %q", rec.Code, rec.Body)
	}
	// path traversal cannot leave the web directory
	for _, p := range []string{"/../outside-secret.txt", "/..%2foutside-secret.txt", "/%2e%2e/outside-secret.txt", "/a/../../outside-secret.txt"} {
		if body := get(p).Body.String(); strings.Contains(body, "TOP SECRET") {
			t.Fatalf("traversal via %s leaked a file", p)
		}
	}
	// only reads
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest("POST", "/", nil))
	if post.Code != 404 {
		t.Fatalf("POST: %d", post.Code)
	}
}
