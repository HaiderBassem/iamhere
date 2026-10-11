package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/iamhere/api/internal/qr"
)

// testPhoto draws a deterministic synthetic "room" (see the vision tests): the
// same seed is the same place, a different seed another place. brightness and
// noise simulate other light and a noisy sensor.
func testPhoto(seed int64, brightness, noise int) []byte {
	const w, h = 320, 240
	r := rand.New(rand.NewSource(seed))
	type blk struct{ x0, y0, x1, y1, l int }
	var blks []blk
	for i := 0; i < 14; i++ {
		x0, y0 := r.Intn(w*3/4), r.Intn(h*3/4)
		blks = append(blks, blk{x0, y0, x0 + 20 + r.Intn(w/3), y0 + 20 + r.Intn(h/3), r.Intn(256)})
	}
	base := r.Intn(120) + 40
	nr := rand.New(rand.NewSource(seed + 1000))
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			l := base + y*60/h
			for _, b := range blks {
				if x >= b.x0 && x < b.x1 && y >= b.y0 && y < b.y1 {
					l = b.l
				}
			}
			l += brightness
			if noise > 0 {
				l += nr.Intn(2*noise+1) - noise
			}
			img.SetGray(x, y, color.Gray{uint8(max(0, min(255, l)))})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

func blackPhoto() []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, 320, 240)), nil)
	return b.Bytes()
}

// strictVenueSession is the full strict setup: a venue at the test anchor with a
// reference photo (seed 1), and a strict session bound to it.
func (e *env) strictVenueSession(t *testing.T, u User, o Org) (Session, Venue) {
	t.Helper()
	ctx := context.Background()
	v, err := e.svc.CreateVenue(ctx, u, o.ID, "Room 101", anchor.Lat, anchor.Lon, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddVenueReference(ctx, u, o.ID, v.ID, testPhoto(1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	s, err := e.svc.CreateSession(ctx, u, o.ID, CreateSessionInput{Title: "Strict", VenueID: &v.ID, Mode: "strict"})
	if err != nil {
		t.Fatal(err)
	}
	return s, v
}

func TestNormalModeIsQROnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "normal")

	// no location at all, nothing configured: a fresh QR is all it takes
	s := e.session(t, prof, o, nil)
	tok := e.token(t, prof, o, s)
	r, err := e.svc.CheckIn(ctx, e.user(t, "A"), CheckInInput{Token: tok})
	if err != nil || r.Status != "verified" || r.Mode != "standard" || r.Reasons[0] != "qr_only" {
		t.Fatalf("%+v %v", r, err)
	}
	// even a wildly wrong position or a photo a client may send is ignored
	far := &GPSInput{Lat: -33, Lon: 151, AccuracyM: 5}
	r, err = e.svc.CheckIn(ctx, e.user(t, "B"), CheckInInput{Token: tok, GPS: far, Photo: blackPhoto()})
	if err != nil || r.Status != "verified" {
		t.Fatalf("%+v %v", r, err)
	}
	// and nothing about location is stored for normal mode
	var withGPS int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM attendance_records WHERE evidence ? 'gps' OR evidence ? 'visual'`).Scan(&withGPS)
	if withGPS != 0 {
		t.Fatalf("normal mode stored location/photo evidence on %d records", withGPS)
	}
	// the scan step asks for nothing extra
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
	if err != nil || !sc.NeedsGPS || !sc.NeedsPhoto || sc.Mode != "strict" || sc.AlreadyRecorded {
		t.Fatalf("%+v %v", sc, err)
	}
	// the QR rotates while the student is still taking the photo: the ticket carries on
	e.advance(40 * time.Second)

	good := testPhoto(1, 25, 8) // same room, different light
	r, err := e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: good})
	if err != nil || r.Status != "verified" || r.Mode != "strict" {
		t.Fatalf("full strict check-in: %+v %v", r, err)
	}
	var ev string
	e.pool.QueryRow(ctx, `SELECT evidence::text FROM attendance_records WHERE user_id=$1`, stu.ID).Scan(&ev)
	if !bytes.Contains([]byte(ev), []byte(`"visual"`)) || bytes.Contains([]byte(ev), []byte("36.19")) {
		t.Fatalf("evidence must hold the photo verdict but never raw coordinates: %s", ev)
	}

	// each weak signal sends the person to review; none is accepted silently
	fresh := func(name string) (User, string) {
		u := e.user(t, name)
		sc, err := e.svc.Scan(ctx, u, e.token(t, prof, o, s))
		if err != nil {
			t.Fatal(err)
		}
		return u, sc.Ticket
	}
	cases := []struct {
		name   string
		gps    *GPSInput
		photo  []byte
		want   string
		reason string
	}{
		{"wrong room", near(), testPhoto(7, 0, 0), "needs_review", "visual_check_required"},
		{"no photo", near(), nil, "needs_review", "visual_check_required"},
		{"no gps", nil, good, "needs_review", "gps_unavailable"},
		{"far away", &GPSInput{Lat: 36.30, Lon: 44.01, AccuracyM: 8}, good, "rejected", "outside_radius"},
		{"old gps fix", &GPSInput{Lat: 36.19001, Lon: 44.01001, AccuracyM: 8, FixAgeMs: 45000}, good, "needs_review", "gps_stale"},
	}
	for _, c := range cases {
		u, tk := fresh(c.name)
		r, err := e.svc.CheckIn(ctx, u, CheckInInput{Ticket: tk, GPS: c.gps, Photo: c.photo})
		if err != nil || r.Status != c.want || !contains(r.Reasons, c.reason) {
			t.Errorf("%s: %+v %v", c.name, r, err)
		}
	}

	// a blank/covered photo is a retryable 400, not a verdict
	u, tk := fresh("covered lens")
	_, err = e.svc.CheckIn(ctx, u, CheckInInput{Ticket: tk, GPS: near(), Photo: blackPhoto()})
	wantCode(t, err, "photo_unusable")
	_, err = e.svc.CheckIn(ctx, u, CheckInInput{Ticket: tk, GPS: near(), Photo: []byte("not an image at all, just text bytes here....")})
	wantCode(t, err, "photo_invalid")
	// ...and the retry with the same ticket still works
	r, err = e.svc.CheckIn(ctx, u, CheckInInput{Ticket: tk, GPS: near(), Photo: good})
	if err != nil || r.Status != "verified" {
		t.Fatalf("retry after bad photo: %+v %v", r, err)
	}

	// already-checked-in people are told at scan time and are not charged extra steps
	sc, err = e.svc.Scan(ctx, stu, e.token(t, prof, o, s))
	if err != nil || !sc.AlreadyRecorded || sc.Status != "verified" {
		t.Fatalf("%+v %v", sc, err)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func TestStrictWithoutReferencePhotosGoesToReview(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "norefs")
	v, _ := e.svc.CreateVenue(ctx, prof, o.ID, "Hall", anchor.Lat, anchor.Lon, 30)
	s, err := e.svc.CreateSession(ctx, prof, o.ID, CreateSessionInput{Title: "x", VenueID: &v.ID, Mode: "strict"})
	if err != nil {
		t.Fatal(err)
	}
	tok := e.token(t, prof, o, s)
	stu := e.user(t, "stu")
	sc, _ := e.svc.Scan(ctx, stu, tok)
	if !sc.NeedsGPS || sc.NeedsPhoto {
		t.Fatalf("photo must not be requested when nothing can be compared: %+v", sc)
	}
	r, _ := e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near()})
	if r.Status != "needs_review" || !contains(r.Reasons, "visual_not_configured") {
		t.Fatalf("honest outcome expected: %+v", r)
	}
}

func TestTicketsAndRotation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "tickets")
	s := e.session(t, prof, o, nil) // normal mode
	tok := e.token(t, prof, o, s)
	a, b := e.user(t, "a"), e.user(t, "b")

	sc, err := e.svc.Scan(ctx, a, tok)
	if err != nil {
		t.Fatal(err)
	}
	// a ticket belongs to the person who scanned
	_, err = e.svc.CheckIn(ctx, b, CheckInInput{Ticket: sc.Ticket})
	wantCode(t, err, "invalid_qr")
	// garbage in the ticket slot is refused like a bad code
	_, err = e.svc.CheckIn(ctx, a, CheckInInput{Ticket: "AAAA"})
	wantCode(t, err, "invalid_qr")
	// it expires
	e.advance(qr.TicketTTL + 5*time.Second)
	_, err = e.svc.CheckIn(ctx, a, CheckInInput{Ticket: sc.Ticket})
	wantCode(t, err, "ticket_expired")
	// and it cannot be minted from an old QR
	_, err = e.svc.Scan(ctx, a, tok)
	wantCode(t, err, "qr_expired")

	// a closed session issues nothing and accepts nothing
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

func TestStrictQRWindowIsTighterThanNormal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "window")
	normal := e.session(t, prof, o, nil)
	strict := e.strictSession(t, prof, o, anchor)
	nTok, sTok := e.token(t, prof, o, normal), e.token(t, prof, o, strict)

	// 5s rotation, clock starts at :03 -> at :11 the code is exactly two rotations old
	e.advance(8 * time.Second)
	if _, err := e.svc.Scan(ctx, e.user(t, "n"), nTok); err != nil {
		t.Fatalf("normal mode allows two rotations of slack: %v", err)
	}
	_, err := e.svc.Scan(ctx, e.user(t, "s"), sTok)
	wantCode(t, err, "qr_expired")
}

func TestVenueReferencePhotos(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, organizer := e.user(t, "owner"), e.user(t, "organizer")
	o := e.org(t, owner, "refs")
	e.svc.SetMember(ctx, owner, o.ID, organizer.ID, RoleOrganizer)
	v, _ := e.svc.CreateVenue(ctx, owner, o.ID, "Hall", 36.19, 44.01, 30)

	if _, err := e.svc.AddVenueReference(ctx, organizer, o.ID, v.ID, testPhoto(1, 0, 0)); err != ErrForbidden {
		t.Fatalf("organizer enrolling a reference: %v", err)
	}
	_, err := e.svc.AddVenueReference(ctx, owner, o.ID, v.ID, blackPhoto())
	wantCode(t, err, "photo_unusable")
	_, err = e.svc.AddVenueReference(ctx, owner, o.ID, v.ID, []byte("nope"))
	if err == nil {
		t.Fatal("garbage accepted as a reference photo")
	}

	var first VenueReference
	for i := 0; i < maxReferencesPerVenue; i++ {
		ref, err := e.svc.AddVenueReference(ctx, owner, o.ID, v.ID, testPhoto(int64(100+i), 0, 0))
		if err != nil {
			t.Fatalf("reference %d: %v", i, err)
		}
		if i == 0 {
			first = ref
		}
	}
	_, err = e.svc.AddVenueReference(ctx, owner, o.ID, v.ID, testPhoto(999, 0, 0))
	wantCode(t, err, "reference_limit")

	vs, _ := e.svc.ListVenues(ctx, organizer, o.ID)
	if len(vs) != 1 || vs[0].ReferenceCount != maxReferencesPerVenue {
		t.Fatalf("%+v", vs)
	}
	refs, err := e.svc.ListVenueReferences(ctx, owner, o.ID, v.ID)
	if err != nil || len(refs) != maxReferencesPerVenue {
		t.Fatalf("%d %v", len(refs), err)
	}

	// tenant isolation: another organization's owner cannot see, add or delete
	other := e.user(t, "other")
	oo := e.org(t, other, "elsewhere")
	if _, err := e.svc.ListVenueReferences(ctx, other, o.ID, v.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant list: %v", err)
	}
	if err := e.svc.DeleteVenueReference(ctx, other, oo.ID, v.ID, first.ID); err != ErrNotFound {
		t.Fatalf("cross-org delete via own org: %v", err)
	}
	if _, err := e.svc.AddVenueReference(ctx, other, oo.ID, v.ID, testPhoto(5, 0, 0)); err != ErrNotFound {
		t.Fatalf("cross-org add via own org: %v", err)
	}

	if err := e.svc.DeleteVenueReference(ctx, owner, o.ID, v.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteVenueReference(ctx, owner, o.ID, v.ID, first.ID); err != ErrNotFound {
		t.Fatalf("double delete: %v", err)
	}
	// deleting the venue takes its fingerprints with it
	if err := e.svc.DeleteVenue(ctx, owner, o.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM venue_fingerprints`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d fingerprints left behind", left)
	}
}

func TestConcurrentStrictCheckins(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	prof := e.user(t, "P")
	o := e.org(t, prof, "stress")
	s, _ := e.strictVenueSession(t, prof, o)
	tok := e.token(t, prof, o, s)
	photo := testPhoto(1, 10, 5)

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
			sc, err := e.svc.Scan(ctx, u, tok)
			if err != nil {
				results[i] = "scan:" + err.Error()
				return
			}
			r, err := e.svc.CheckIn(ctx, u, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: photo})
			if err != nil {
				results[i] = "checkin:" + err.Error()
				return
			}
			results[i] = r.Status
		}()
	}
	wg.Wait()
	for i, r := range results {
		if r != "verified" {
			t.Fatalf("attendee %d: %s", i, r)
		}
	}
	got, _ := e.svc.GetSession(ctx, prof, o.ID, s.ID)
	if got.Verified != n {
		t.Fatalf("verified %d want %d", got.Verified, n)
	}
}

// A person still in review who tries again must leave the reviewer the latest
// evidence, not the first attempt's, and can still be upgraded by a good attempt.
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

	// 1st: no location -> review because of GPS
	sc, _ := e.svc.Scan(ctx, stu, e.token(t, prof, o, s))
	e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, Photo: testPhoto(1, 0, 0)})
	if r := reasonsOf(); !contains(r, "gps_unavailable") {
		t.Fatalf("first attempt reasons: %v", r)
	}
	// 2nd: location now fine but wrong room -> still review, for the NEW reason
	r, err := e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: testPhoto(7, 0, 0)})
	if err != nil || r.Status != "needs_review" || !r.AlreadyRecorded {
		t.Fatalf("%+v %v", r, err)
	}
	if got := reasonsOf(); contains(got, "gps_unavailable") || !contains(got, "visual_check_required") {
		t.Fatalf("reviewer sees stale reasons: %v", got)
	}
	// 3rd: everything right -> verified
	r, _ = e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: testPhoto(1, 15, 5)})
	if r.Status != "verified" {
		t.Fatalf("upgrade: %+v", r)
	}
	// a reviewer's decision is never overwritten by a later attempt
	e.svc.ReviewRecord(ctx, prof, o.ID, *r.RecordID, "revoked", "left early")
	r, _ = e.svc.CheckIn(ctx, stu, CheckInInput{Ticket: sc.Ticket, GPS: near(), Photo: testPhoto(1, 0, 0)})
	if r.Status != "revoked" {
		t.Fatalf("organizer decision overwritten: %+v", r)
	}
}
