package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/iamhere/api/internal/qr"
	"github.com/iamhere/api/internal/vision"
)

// ---- a fake vision service ------------------------------------------------------
//
// The real service (apps/vision) is tested on its own. Here we only need its
// contract, with verdicts the test controls: every reference photo is labelled
// with the room it shows, and a probe is the text "room=<label>;code=<text>".

type fakeVision struct {
	client *vision.Client
	srv    *httptest.Server
	mu     sync.Mutex
	labels map[string]string // reference sha256 -> room label
	cached map[string]bool   // what the service "has in its cache"
	probes map[string]string // probe id -> room, fetchable once as a learned view
	seq    int
	// learned views sent back to the service (after a restart)
	warmedFeatures int
}

func newFakeVision(t *testing.T) *fakeVision {
	f := &fakeVision{labels: map[string]string{}, cached: map[string]bool{}, probes: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	f.client = vision.New(f.srv.URL)
	return f
}

// label registers what room a reference image shows.
func (f *fakeVision) label(img []byte, room string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := vision.SHA256(img)
	f.labels[hex.EncodeToString(h[:])] = room
}

func (f *fakeVision) serve(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Image, Probe, Features string
		ProbeID                string `json:"probe_id"`
		Refs                   []struct{ SHA256 string }
	}
	json.NewDecoder(r.Body).Decode(&in)
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(status int, v any) { w.WriteHeader(status); json.NewEncoder(w).Encode(v) }
	switch r.URL.Path {
	case "/v1/features": // a learned view: the probe's room, as "features"
		room, ok := f.probes[in.ProbeID]
		if !ok {
			reply(404, map[string]any{"error": "not_found"})
			return
		}
		delete(f.probes, in.ProbeID)
		blob := []byte("features-of:" + room + ":" + in.ProbeID)
		h := vision.SHA256(blob)
		f.labels[hex.EncodeToString(h[:])] = room
		f.cached[hex.EncodeToString(h[:])] = true // like the service: ready as a reference at once
		reply(200, map[string]string{"features": base64.StdEncoding.EncodeToString(blob)})
	case "/v1/reference":
		if in.Features != "" {
			blob, _ := base64.StdEncoding.DecodeString(in.Features)
			h := vision.SHA256(blob)
			f.cached[hex.EncodeToString(h[:])] = true
			f.warmedFeatures++
			reply(200, map[string]int{"keypoints": 1500})
			return
		}
		img, _ := base64.StdEncoding.DecodeString(in.Image)
		h := vision.SHA256(img)
		f.cached[hex.EncodeToString(h[:])] = true // like the service: cached even when criticised
		if f.labels[hex.EncodeToString(h[:])] == "blurry" {
			reply(422, map[string]any{"error": "photo_unusable", "codes": []string{"blurry"}, "message": "The photo is blurry."})
			return
		}
		reply(200, map[string]int{"width": 320, "height": 240, "keypoints": 1500})
	case "/v1/verify":
		var missing []string
		for _, ref := range in.Refs {
			if !f.cached[ref.SHA256] {
				missing = append(missing, ref.SHA256)
			}
		}
		if len(missing) > 0 {
			reply(409, map[string]any{"error": "missing_refs", "missing": missing})
			return
		}
		p, _ := base64.StdEncoding.DecodeString(in.Probe)
		fields := map[string]string{}
		for _, kv := range strings.Split(string(p), ";") {
			k, v, _ := strings.Cut(kv, "=")
			fields[k] = v
		}
		if fields["room"] == "dark" {
			reply(422, map[string]any{"error": "photo_unusable", "codes": []string{"too_dark"}, "message": "The photo is too dark."})
			return
		}
		res := vision.Result{Codes: []string{}, Threshold: 40, Model: "fake"}
		if c := fields["code"]; c != "" {
			res.Codes = append(res.Codes, c)
		}
		score := 150 // 375% of the threshold: clearly the room, not a near-copy
		if n, err := strconv.Atoi(fields["score"]); err == nil {
			score = n
		}
		for _, ref := range in.Refs {
			if f.labels[ref.SHA256] == fields["room"] {
				res.Matched, res.Score = true, score
			}
		}
		f.seq++
		res.ProbeID = fmt.Sprintf("probe-%d", f.seq)
		f.probes[res.ProbeID] = fields["room"]
		reply(200, res)
	}
}

// probe is an attendee photo of `room` in which `code` is readable.
func probe(room, code string) []byte {
	return []byte("room=" + room + ";code=https://attend.example.org/#/c/" + code)
}

// roomPhoto returns a real JPEG (the API checks the format) labelled as `room`.
func (e *env) roomPhoto(room string) []byte {
	r := rand.New(rand.NewSource(int64(len(e.vis.labels) + 1)))
	img := image.NewGray(image.Rect(0, 0, 320, 240))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.Intn(256))
	}
	img.SetGray(0, 0, color.Gray{uint8(len(room))})
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	e.vis.label(b.Bytes(), room)
	return b.Bytes()
}

// strictVenueSession: a place at the test anchor with a saved photo of
// "room-A", and a strict session there.
func (e *env) strictVenueSession(t *testing.T, u User, o Org) (Session, Venue) {
	t.Helper()
	ctx := context.Background()
	v, err := e.svc.CreateVenue(ctx, u, o.ID, "Room 101", anchor.Lat, anchor.Lon, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddVenueReference(ctx, u, o.ID, v.ID, e.roomPhoto("room-A")); err != nil {
		t.Fatal(err)
	}
	s, err := e.svc.CreateSession(ctx, u, o.ID, CreateSessionInput{Title: "Strict", VenueID: &v.ID, Mode: "strict"})
	if err != nil {
		t.Fatal(err)
	}
	return s, v
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestNormalModeIsQROnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "normal")

	s := e.session(t, prof, o, nil)
	tok := e.token(t, prof, o, s)
	r, err := e.svc.CheckIn(ctx, e.user(t, "A"), CheckInInput{Token: tok})
	if err != nil || r.Status != "verified" || r.Mode != "standard" || r.Reasons[0] != "qr_only" {
		t.Fatalf("%+v %v", r, err)
	}
	// a wildly wrong position or a photo a client may send is ignored
	far := &GPSInput{Lat: -33, Lon: 151, AccuracyM: 5}
	r, err = e.svc.CheckIn(ctx, e.user(t, "B"), CheckInInput{Token: tok, GPS: far, Photo: probe("elsewhere", "")})
	if err != nil || r.Status != "verified" {
		t.Fatalf("%+v %v", r, err)
	}
	var withEvidence int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM attendance_records WHERE evidence ? 'gps' OR evidence ? 'visual'`).Scan(&withEvidence)
	if withEvidence != 0 {
		t.Fatalf("normal mode stored location/photo evidence on %d records", withEvidence)
	}
	sc, err := e.svc.Scan(ctx, e.user(t, "C"), tok)
	if err != nil || sc.NeedsGPS || sc.NeedsPhoto || sc.Mode != "standard" || sc.Ticket == "" {
		t.Fatalf("%+v %v", sc, err)
	}
}

func TestStrictModeFullFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "strict")
	s, _ := e.strictVenueSession(t, prof, o)
	if s.VerificationMode != "strict" || s.QRRotationS != 5 || s.LocationSource != "venue" {
		t.Fatalf("%+v", s)
	}
	tok := e.token(t, prof, o, s)

	stu := e.user(t, "student")
	sc, err := e.svc.Scan(ctx, stu, tok)
	if err != nil || !sc.NeedsGPS || !sc.NeedsPhoto || sc.Mode != "strict" {
		t.Fatalf("%+v %v", sc, err)
	}
	// the photo of the screen is taken a few seconds later, showing that moment's code
	e.advance(6 * time.Second)
	r, err := e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: probe("room-A", e.token(t, prof, o, s))})
	if err != nil || r.Status != "verified" {
		t.Fatalf("full strict check-in: %+v %v", r, err)
	}
	var ev string
	e.pool.QueryRow(ctx, `SELECT evidence::text FROM attendance_records WHERE user_id=$1`, stu.ID).Scan(&ev)
	if !strings.Contains(ev, `"room_matched": true`) || !strings.Contains(ev, `"current_code_in_photo": true`) || strings.Contains(ev, "36.19") {
		t.Fatalf("evidence must hold the photo verdict but never raw coordinates: %s", ev)
	}

	fresh := func(name string) (User, string, string) {
		u := e.user(t, name)
		code := e.token(t, prof, o, s)
		sc, err := e.svc.Scan(ctx, u, code)
		if err != nil {
			t.Fatal(err)
		}
		return u, sc.Ticket, code
	}
	other := e.strictSession(t, prof, o, anchor)
	otherCode := e.token(t, prof, o, other)
	oldCode := e.token(t, prof, o, s)
	e.advance(25 * time.Second) // oldCode is now 5 rotations old

	cases := []struct {
		name   string
		gps    *GPSInput
		photo  func(code string) []byte
		want   string
		reason string
	}{
		{"wrong room", near(), func(c string) []byte { return probe("room-B", c) }, "needs_review", "visual_check_required"},
		{"no photo", near(), func(string) []byte { return nil }, "needs_review", "visual_check_required"},
		{"photo without the code", near(), func(string) []byte { return []byte("room=room-A;code=") }, "needs_review", "photo_without_current_code"},
		{"photo of an old code (replayed photo)", near(), func(string) []byte { return probe("room-A", oldCode) }, "needs_review", "photo_without_current_code"},
		{"code of another session", near(), func(string) []byte { return probe("room-A", otherCode) }, "needs_review", "photo_without_current_code"},
		{"no gps", nil, func(c string) []byte { return probe("room-A", c) }, "needs_review", "gps_unavailable"},
		{"far away", &GPSInput{Lat: 36.30, Lon: 44.01, AccuracyM: 8}, func(c string) []byte { return probe("room-A", c) }, "rejected", "outside_radius"},
	}
	for _, c := range cases {
		u, tk, code := fresh(c.name)
		r, err := e.svc.CheckIn(ctx, u, CheckInInput{Ticket: tk, GPS: c.gps, Photo: c.photo(code)})
		if err != nil || r.Status != c.want || !contains(r.Reasons, c.reason) {
			t.Errorf("%s: %+v %v", c.name, r, err)
		}
	}

	// a dark photo is a retryable 400, not a verdict; the retry still works
	u, tk, code := fresh("dark photo")
	_, err = e.svc.CheckIn(ctx, u, CheckInInput{Ticket: tk, GPS: near(), Photo: []byte("room=dark")})
	wantCode(t, err, "photo_unusable")
	r, err = e.svc.CheckIn(ctx, u, CheckInInput{Ticket: tk, GPS: near(), Photo: probe("room-A", code)})
	if err != nil || r.Status != "verified" {
		t.Fatalf("retry after bad photo: %+v %v", r, err)
	}
	// already checked in: told at scan time
	sc, err = e.svc.Scan(ctx, stu, e.token(t, prof, o, s))
	if err != nil || !sc.AlreadyRecorded || sc.Status != "verified" {
		t.Fatalf("%+v %v", sc, err)
	}
}

// Strict mode without a saved place: the organizer's location plus today's photo.
func TestTodaysPhotoWithoutSavedPlace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "today")
	s := e.strictSession(t, prof, o, anchor)
	stu := e.user(t, "stu")

	sc, _ := e.svc.Scan(ctx, stu, e.token(t, prof, o, s))
	if !sc.NeedsGPS || sc.NeedsPhoto {
		t.Fatalf("no photo exists yet, so none may be asked for: %+v", sc)
	}
	if _, err := e.svc.AddSessionReference(ctx, prof, o.ID, s.ID, e.roomPhoto("hall-7")); err != nil {
		t.Fatal(err)
	}
	code := e.token(t, prof, o, s)
	sc, _ = e.svc.Scan(ctx, stu, code)
	if !sc.NeedsPhoto {
		t.Fatalf("today's photo must enable the photo check: %+v", sc)
	}
	r, err := e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: probe("hall-7", code)})
	if err != nil || r.Status != "verified" {
		t.Fatalf("%+v %v", r, err)
	}
	refs, err := e.svc.ListSessionReferences(ctx, prof, o.ID, s.ID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("%v %v", refs, err)
	}
}

func TestSessionPhotoRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, organizer, organizer2, reviewer := e.user(t, "owner"), e.user(t, "org1"), e.user(t, "org2"), e.user(t, "rev")
	o := e.org(t, owner, "rules")
	e.svc.SetMember(ctx, owner, o.ID, organizer.ID, RoleOrganizer)
	e.svc.SetMember(ctx, owner, o.ID, organizer2.ID, RoleOrganizer)
	e.svc.SetMember(ctx, owner, o.ID, reviewer.ID, RoleReviewer)
	s := e.strictSession(t, organizer, o, anchor)

	if _, err := e.svc.AddSessionReference(ctx, organizer2, o.ID, s.ID, e.roomPhoto("x")); err != ErrForbidden {
		t.Fatalf("someone else's session: %v", err)
	}
	if _, err := e.svc.AddSessionReference(ctx, reviewer, o.ID, s.ID, e.roomPhoto("x")); err != ErrForbidden {
		t.Fatalf("reviewer: %v", err)
	}
	if _, err := e.svc.AddSessionReference(ctx, owner, o.ID, s.ID, e.roomPhoto("x")); err != nil {
		t.Fatalf("owner may help: %v", err)
	}
	_, err := e.svc.AddSessionReference(ctx, organizer, o.ID, s.ID, []byte("not a photo"))
	wantCode(t, err, "photo_invalid")
	blurry := e.roomPhoto("blurry")
	_, err = e.svc.AddSessionReference(ctx, organizer, o.ID, s.ID, blurry)
	wantCode(t, err, "photo_unusable")
	var first ReferencePhoto
	for i := 0; i < maxReferencesPerSession-1; i++ {
		ref, err := e.svc.AddSessionReference(ctx, organizer, o.ID, s.ID, e.roomPhoto("x"))
		if err != nil {
			t.Fatal(err)
		}
		first = ref
	}
	_, err = e.svc.AddSessionReference(ctx, organizer, o.ID, s.ID, e.roomPhoto("x"))
	wantCode(t, err, "reference_limit")

	// thumbnails: organizers yes, reviewers no, other organizations never
	if img, err := e.svc.ReferenceImage(ctx, organizer, o.ID, first.ID); err != nil || len(img) == 0 {
		t.Fatalf("%d %v", len(img), err)
	}
	if _, err := e.svc.ReferenceImage(ctx, reviewer, o.ID, first.ID); err != ErrForbidden {
		t.Fatalf("reviewer image: %v", err)
	}
	stranger := e.user(t, "stranger")
	so := e.org(t, stranger, "elsewhere")
	if _, err := e.svc.ReferenceImage(ctx, stranger, so.ID, first.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant image: %v", err)
	}

	if err := e.svc.DeleteSessionReference(ctx, organizer, o.ID, s.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	// ended sessions are frozen
	e.svc.CloseSession(ctx, organizer, o.ID, s.ID)
	_, err = e.svc.AddSessionReference(ctx, organizer, o.ID, s.ID, e.roomPhoto("x"))
	wantCode(t, err, "session_ended")

	// today's photos are purged a month after the session; saved places' photos stay
	v, _ := e.svc.CreateVenue(ctx, owner, o.ID, "Hall", anchor.Lat, anchor.Lon, 30)
	e.svc.AddVenueReference(ctx, owner, o.ID, v.ID, e.roomPhoto("hall"))
	e.advance(sessionPhotoRetention + 2*time.Hour)
	if n, err := e.svc.PurgeSessionPhotos(ctx); err != nil || n != maxReferencesPerSession-1 {
		t.Fatalf("purged %d %v", n, err)
	}
	var left int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM reference_photos`).Scan(&left)
	if left != 1 {
		t.Fatalf("%d photos left, want only the place's", left)
	}
}

func TestPhotoCheckOutagesNeverPass(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "outage")
	s, v := e.strictVenueSession(t, prof, o)

	e.vis.srv.Close() // the vision service goes down
	u := e.user(t, "u")
	code := e.token(t, prof, o, s)
	sc, _ := e.svc.Scan(ctx, u, code)
	r, err := e.svc.CheckIn(ctx, u, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: probe("room-A", code)})
	if err != nil || r.Status != "needs_review" || !contains(r.Reasons, "visual_check_unavailable") {
		t.Fatalf("outage must send people to review, not fail or pass: %+v %v", r, err)
	}
	_, err = e.svc.AddVenueReference(ctx, prof, o.ID, v.ID, e.roomPhoto("room-A"))
	wantCode(t, err, "photo_check_busy")

	// a server without the service: strict asks for no photo, and says why
	e.svc.vis = nil
	sc, _ = e.svc.Scan(ctx, e.user(t, "w"), e.token(t, prof, o, s))
	if sc.NeedsPhoto {
		t.Fatalf("%+v", sc)
	}
	_, err = e.svc.AddVenueReference(ctx, prof, o.ID, v.ID, e.roomPhoto("room-A"))
	wantCode(t, err, "photo_check_unavailable")
}

func TestPlaceTestFromASeat(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "seat")
	_, v := e.strictVenueSession(t, prof, o)
	res, err := e.svc.TestVenuePhoto(ctx, prof, o.ID, v.ID, probe("room-A", ""))
	if err != nil || !res.Matched || res.Threshold != 40 || res.References != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	res, _ = e.svc.TestVenuePhoto(ctx, prof, o.ID, v.ID, probe("room-B", ""))
	if res.Matched {
		t.Fatalf("%+v", res)
	}
	empty, _ := e.svc.CreateVenue(ctx, prof, o.ID, "Empty", anchor.Lat, anchor.Lon, 30)
	_, err = e.svc.TestVenuePhoto(ctx, prof, o.ID, empty.ID, probe("room-A", ""))
	wantCode(t, err, "no_references")
}

func TestTicketsAndRotation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "tickets")
	s := e.session(t, prof, o, nil)
	tok := e.token(t, prof, o, s)
	a, b := e.user(t, "a"), e.user(t, "b")

	sc, err := e.svc.Scan(ctx, a, tok)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CheckIn(ctx, b, CheckInInput{Ticket: sc.Ticket})
	wantCode(t, err, "invalid_qr")
	_, err = e.svc.CheckIn(ctx, a, CheckInInput{Ticket: "AAAA"})
	wantCode(t, err, "invalid_qr")
	e.advance(qr.TicketTTL + 5*time.Second)
	_, err = e.svc.CheckIn(ctx, a, CheckInInput{Ticket: sc.Ticket})
	wantCode(t, err, "ticket_expired")
	_, err = e.svc.Scan(ctx, a, tok)
	wantCode(t, err, "qr_expired")

	fresh := e.token(t, prof, o, s)
	sc, _ = e.svc.Scan(ctx, a, fresh)
	if _, err := e.svc.CloseSession(ctx, prof, o.ID, s.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.CheckIn(ctx, a, CheckInInput{Ticket: sc.Ticket})
	wantCode(t, err, "session_ended")
	_, err = e.svc.Scan(ctx, a, fresh)
	wantCode(t, err, "session_ended")
}

// In both modes a code stops working as soon as the next one is on the screen.
func TestReplacedCodeFailsInEveryMode(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "window")
	normal := e.session(t, prof, o, nil)
	strict := e.strictSession(t, prof, o, anchor)
	nTok, sTok := e.token(t, prof, o, normal), e.token(t, prof, o, strict)

	// 5s rotation, clock starts at :03 -> the code is replaced at :05; at :06.5 it is gone
	e.advance(3500 * time.Millisecond)
	_, err := e.svc.Scan(ctx, e.user(t, "n"), nTok)
	wantCode(t, err, "qr_expired")
	_, err = e.svc.Scan(ctx, e.user(t, "s"), sTok)
	wantCode(t, err, "qr_expired")
}

func TestVenueReferencePhotos(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, organizer := e.user(t, "owner"), e.user(t, "organizer")
	o := e.org(t, owner, "refs")
	e.svc.SetMember(ctx, owner, o.ID, organizer.ID, RoleOrganizer)
	v, _ := e.svc.CreateVenue(ctx, owner, o.ID, "Hall", 36.19, 44.01, 30)

	if _, err := e.svc.AddVenueReference(ctx, organizer, o.ID, v.ID, e.roomPhoto("hall")); err != ErrForbidden {
		t.Fatalf("organizer enrolling a saved photo: %v", err)
	}
	var first ReferencePhoto
	for i := 0; i < maxReferencesPerVenue; i++ {
		ref, err := e.svc.AddVenueReference(ctx, owner, o.ID, v.ID, e.roomPhoto("hall"))
		if err != nil {
			t.Fatalf("reference %d: %v", i, err)
		}
		if i == 0 {
			first = ref
		}
	}
	_, err := e.svc.AddVenueReference(ctx, owner, o.ID, v.ID, e.roomPhoto("hall"))
	wantCode(t, err, "reference_limit")
	vs, _ := e.svc.ListVenues(ctx, organizer, o.ID)
	if len(vs) != 1 || vs[0].ReferenceCount != maxReferencesPerVenue {
		t.Fatalf("%+v", vs)
	}
	// organizers can see the saved photos (to pick a place), not change them
	if refs, err := e.svc.ListVenueReferences(ctx, organizer, o.ID, v.ID); err != nil || len(refs) != maxReferencesPerVenue {
		t.Fatalf("%d %v", len(refs), err)
	}
	if err := e.svc.DeleteVenueReference(ctx, organizer, o.ID, v.ID, first.ID); err != ErrForbidden {
		t.Fatalf("organizer delete: %v", err)
	}
	other := e.user(t, "other")
	oo := e.org(t, other, "elsewhere")
	if _, err := e.svc.ListVenueReferences(ctx, other, o.ID, v.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant list: %v", err)
	}
	if err := e.svc.DeleteVenueReference(ctx, other, oo.ID, v.ID, first.ID); err != ErrNotFound {
		t.Fatalf("cross-org delete via own org: %v", err)
	}
	if err := e.svc.DeleteVenueReference(ctx, owner, o.ID, v.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteVenueReference(ctx, owner, o.ID, v.ID, first.ID); err != ErrNotFound {
		t.Fatalf("double delete: %v", err)
	}
	if err := e.svc.DeleteVenue(ctx, owner, o.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM reference_photos`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d photos left behind", left)
	}
}

// A person still in review who tries again must leave the reviewer the latest
// evidence, and can still be upgraded by a good attempt.
func TestReviewRetryKeepsLatestEvidence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "retry")
	s, _ := e.strictVenueSession(t, prof, o)
	stu := e.user(t, "stu")
	reasonsOf := func() []string {
		rows, err := e.svc.ListAttendance(ctx, prof, o.ID, s.ID, 10, nil)
		if err != nil || len(rows) != 1 {
			t.Fatalf("%v %v", rows, err)
		}
		return rows[0].Reasons
	}
	code := e.token(t, prof, o, s)
	sc, _ := e.svc.Scan(ctx, stu, code)
	e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, Photo: probe("room-A", code)})
	if r := reasonsOf(); !contains(r, "gps_unavailable") {
		t.Fatalf("first attempt reasons: %v", r)
	}
	r, err := e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: probe("room-B", code)})
	if err != nil || r.Status != "needs_review" || !r.AlreadyRecorded {
		t.Fatalf("%+v %v", r, err)
	}
	if got := reasonsOf(); contains(got, "gps_unavailable") || !contains(got, "visual_check_required") {
		t.Fatalf("reviewer sees stale reasons: %v", got)
	}
	r, _ = e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: probe("room-A", code)})
	if r.Status != "verified" {
		t.Fatalf("upgrade: %+v", r)
	}
	e.svc.ReviewRecord(ctx, prof, o.ID, *r.RecordID, "revoked", "left early")
	r, _ = e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: probe("room-A", code)})
	if r.Status != "revoked" {
		t.Fatalf("organizer decision overwritten: %+v", r)
	}
}

func TestConcurrentStrictCheckins(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "stress")
	s, _ := e.strictVenueSession(t, prof, o)
	code := e.token(t, prof, o, s)

	const n = 60
	users := make([]User, n)
	for i := range users {
		users[i] = e.user(t, "u")
	}
	var wg sync.WaitGroup
	results := make([]string, n)
	for i, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sc, err := e.svc.Scan(ctx, u, code)
			if err != nil {
				results[i] = "scan:" + err.Error()
				return
			}
			r, err := e.svc.CheckIn(ctx, u, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: probe("room-A", code)})
			if err != nil {
				results[i] = "checkin:" + err.Error()
				return
			}
			results[i] = r.Status + " " + strings.Join(r.Reasons, ",")
		}()
	}
	wg.Wait()
	for i, r := range results {
		if !strings.HasPrefix(r, "verified ") {
			t.Fatalf("attendee %d: %s", i, r)
		}
	}
	got, _ := e.svc.GetSession(ctx, prof, o.ID, s.ID)
	if got.Verified != n {
		t.Fatalf("verified %d want %d", got.Verified, n)
	}
}

// probeScore is like probe, with the score the service gives the room match.
func probeScore(room, code string, score int) []byte {
	return []byte(fmt.Sprintf("room=%s;code=https://attend.example.org/#/c/%s;score=%d", room, code, score))
}

func TestRoomLearning(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "learn")
	s, v := e.strictVenueSession(t, prof, o)
	stats := func() PhotoStats {
		t.Helper()
		st, err := e.svc.VenuePhotoStats(ctx, prof, o.ID, v.ID)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	waitLearned := func(n int) {
		t.Helper()
		for i := 0; i < 150 && stats().Learned != n; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		if got := stats().Learned; got != n {
			t.Fatalf("learned views: %d, want %d", got, n)
		}
	}
	checkIn := func(name string, photo func(code string) []byte, gps *GPSInput) CheckInResult {
		t.Helper()
		code := e.token(t, prof, o, s)
		r, err := e.svc.CheckIn(ctx, e.user(t, name), CheckInInput{Token: code, GPS: gps, Photo: photo(code)})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	far := &GPSInput{Lat: anchor.Lat + 0.05, Lon: anchor.Lon, AccuracyM: 5}

	// verified beyond doubt, and a view not seen yet: the place learns it
	if r := checkIn("a", func(c string) []byte { return probe("room-A", c) }, near()); r.Status != "verified" {
		t.Fatalf("%+v", r)
	}
	waitLearned(1)

	// none of these teach anything
	for name, tc := range map[string]struct {
		photo  func(string) []byte
		gps    *GPSInput
		status string
	}{
		"near-copy of a known view": {func(c string) []byte { return probeScore("room-A", c, 200) }, near(), "verified"},
		"no current code":           {func(string) []byte { return probe("room-A", "") }, near(), "needs_review"},
		"far away":                  {func(c string) []byte { return probe("room-A", c) }, far, "rejected"},
		"another room":              {func(c string) []byte { return probe("room-B", c) }, near(), "needs_review"},
		"no location":               {func(c string) []byte { return probe("room-A", c) }, nil, "needs_review"},
	} {
		if r := checkIn(name, tc.photo, tc.gps); r.Status != tc.status {
			t.Fatalf("%s: %+v", name, r)
		}
	}
	time.Sleep(300 * time.Millisecond)
	st := stats()
	if st.Learned != 1 || st.Checks != 6 || st.Recognized != 5 || st.CodeRead != 5 || st.References != 1 {
		t.Fatalf("%+v", st)
	}

	// the learned view is compared too, and is sent back to the service after a restart
	refs, err := e.svc.sessionRefs(ctx, o.ID, s.ID, &v.ID)
	if err != nil || len(refs) != 2 || !refs[0].Features || refs[1].Features {
		t.Fatalf("learned views go before saved photos: %+v %v", refs, err)
	}
	e.vis.mu.Lock()
	e.vis.cached = map[string]bool{}
	e.vis.mu.Unlock()
	if r := checkIn("after-restart", func(c string) []byte { return probeScore("room-A", c, 300) }, near()); r.Status != "verified" {
		t.Fatalf("%+v", r)
	}
	if e.vis.warmedFeatures != 1 {
		t.Fatalf("learned view re-sent %d times", e.vis.warmedFeatures)
	}

	// a place keeps its newest 24 views
	for i := 0; i < 30; i++ {
		e.vis.mu.Lock()
		e.vis.probes[fmt.Sprintf("extra-%d", i)] = "room-A"
		e.vis.mu.Unlock()
		e.svc.learn(o.ID, v.ID, uuid.New(), vision.Result{ProbeID: fmt.Sprintf("extra-%d", i), Score: 100, Threshold: 40, Matched: true})
	}
	if st := stats(); st.Learned != maxLearnedPerPlace {
		t.Fatalf("cap: %+v", st)
	}

	// only managers can make a place forget; views expire after 30 days anyway
	org1 := e.user(t, "organizer")
	e.svc.SetMember(ctx, prof, o.ID, org1.ID, RoleOrganizer)
	if err := e.svc.ForgetLearned(ctx, org1, o.ID, v.ID); err != ErrForbidden {
		t.Fatalf("organizer: %v", err)
	}
	e.svc.Now = func() time.Time { return e.clock().Add(31 * 24 * time.Hour) }
	if err := e.svc.PurgeLearned(ctx); err != nil {
		t.Fatal(err)
	}
	e.svc.Now = e.clock
	if st := stats(); st.Learned != 0 {
		t.Fatalf("expiry: %+v", st)
	}
	e.svc.learn(o.ID, v.ID, uuid.New(), vision.Result{ProbeID: "gone", Score: 100, Threshold: 40, Matched: true}) // unknown probe: nothing happens
	e.vis.mu.Lock()
	e.vis.probes["again"] = "room-A"
	e.vis.mu.Unlock()
	e.svc.learn(o.ID, v.ID, uuid.New(), vision.Result{ProbeID: "again", Score: 100, Threshold: 40, Matched: true})
	if err := e.svc.ForgetLearned(ctx, prof, o.ID, v.ID); err != nil || stats().Learned != 0 {
		t.Fatalf("forget: %v %+v", err, stats())
	}
}
