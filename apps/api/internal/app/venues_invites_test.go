package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVenueBindingAndPrecedence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, org := e.user(t, "owner"), (Org{})
	org = e.org(t, owner, "venues")
	organizer := e.user(t, "organizer")
	if err := e.svc.SetMember(ctx, owner, org.ID, organizer.ID, RoleOrganizer); err != nil {
		t.Fatal(err)
	}

	// organizers can list venues but not manage them
	if _, err := e.svc.CreateVenue(ctx, organizer, org.ID, "Hall A", 36.19, 44.01, 0); err != ErrForbidden {
		t.Fatalf("organizer creating venue: %v", err)
	}
	if _, err := e.svc.CreateVenue(ctx, owner, org.ID, "Bad", 123, 0, 0); err == nil {
		t.Fatal("invalid coordinates accepted")
	}
	if _, err := e.svc.CreateVenue(ctx, owner, org.ID, "Bad", 1, 1, 2); err == nil {
		t.Fatal("radius below minimum accepted")
	}

	// no venues, no anchor: location unknown, every check-in is needs_review
	s0 := e.session(t, organizer, org, nil)
	if s0.LocationSource != "none" || s0.HasAnchor {
		t.Fatalf("%+v", s0)
	}

	// exactly one venue: a title-only session binds to it automatically
	hall, err := e.svc.CreateVenue(ctx, owner, org.ID, "Hall A", 36.19, 44.01, 50)
	if err != nil || hall.RadiusM != 50 {
		t.Fatalf("%+v %v", hall, err)
	}
	s1 := e.session(t, organizer, org, nil)
	if s1.LocationSource != "sole_venue" || !s1.HasAnchor || s1.VenueID == nil || *s1.VenueID != hall.ID || s1.GPSRadiusM != 50 {
		t.Fatalf("%+v", s1)
	}
	stu := e.user(t, "student")
	res, err := e.svc.CheckIn(ctx, stu, CheckInInput{Token: e.token(t, organizer, org, s1), GPS: near()})
	if err != nil || res.Status != "verified" {
		t.Fatalf("%+v %v", res, err)
	}

	// an organizer's own position beats the implicit sole venue
	s2 := e.session(t, organizer, org, &Anchor{Lat: 10, Lon: 10, AccuracyM: 5})
	if s2.LocationSource != "organizer" || s2.VenueID != nil || s2.GPSRadiusM != 20 {
		t.Fatalf("%+v", s2)
	}

	// two venues: no implicit choice; an explicit one wins over the anchor
	hall2, _ := e.svc.CreateVenue(ctx, owner, org.ID, "Hall B", 36.30, 44.20, 0)
	s3 := e.session(t, organizer, org, nil)
	if s3.LocationSource != "none" {
		t.Fatalf("ambiguous venues must not bind: %+v", s3)
	}
	s4, err := e.svc.CreateSession(ctx, organizer, org.ID, CreateSessionInput{Title: "B", Anchor: anchor, VenueID: &hall2.ID, Mode: "strict"})
	if err != nil || s4.LocationSource != "venue" || *s4.VenueID != hall2.ID || s4.GPSRadiusM != 30 {
		t.Fatalf("%+v %v", s4, err)
	}
	// attendee at Hall A is outside Hall B's radius: confidently rejected
	res, err = e.svc.CheckIn(ctx, e.user(t, "far"), CheckInInput{Token: e.token(t, organizer, org, s4), GPS: near()})
	if err != nil || res.Status != "rejected" {
		t.Fatalf("%+v %v", res, err)
	}

	// venues are tenant-scoped
	otherOwner := e.user(t, "other")
	other := e.org(t, otherOwner, "other-org")
	_, err = e.svc.CreateSession(ctx, otherOwner, other.ID, CreateSessionInput{Title: "x", VenueID: &hall.ID})
	wantCode(t, err, "invalid_request")
	if err := e.svc.DeleteVenue(ctx, otherOwner, other.ID, hall.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	if _, err := e.svc.ListVenues(ctx, otherOwner, org.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant list: %v", err)
	}

	// deleting a venue keeps old sessions and their location snapshot
	if err := e.svc.DeleteVenue(ctx, owner, org.ID, hall.ID); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.GetSession(ctx, organizer, org.ID, s1.ID)
	if err != nil || got.VenueID != nil || !got.HasAnchor || got.LocationSource != "sole_venue" {
		t.Fatalf("%+v %v", got, err)
	}
	vs, _ := e.svc.ListVenues(ctx, organizer, org.ID)
	if len(vs) != 1 || vs[0].ID != hall2.ID {
		t.Fatalf("%+v", vs)
	}
}

func TestInviteLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, admin, organizer := e.user(t, "owner"), e.user(t, "admin"), e.user(t, "organizer")
	org := e.org(t, owner, "invites")
	e.svc.SetMember(ctx, owner, org.ID, admin.ID, RoleAdmin)
	e.svc.SetMember(ctx, owner, org.ID, organizer.ID, RoleOrganizer)

	// hierarchy: nobody mints owners, only owners mint admins, organizers mint nothing
	if _, err := e.svc.CreateInvite(ctx, owner, org.ID, RoleOwner, 0); err == nil {
		t.Fatal("owner invite accepted")
	}
	if _, err := e.svc.CreateInvite(ctx, admin, org.ID, RoleAdmin, 0); err != ErrForbidden {
		t.Fatalf("admin minting admin: %v", err)
	}
	if _, err := e.svc.CreateInvite(ctx, organizer, org.ID, RoleReporter, 0); err != ErrForbidden {
		t.Fatalf("organizer minting: %v", err)
	}
	if _, err := e.svc.CreateInvite(ctx, owner, org.ID, "godmode", 0); err == nil {
		t.Fatal("unknown role accepted")
	}
	if _, err := e.svc.CreateInvite(ctx, owner, org.ID, RoleReviewer, time.Minute); err == nil {
		t.Fatal("too-short expiry accepted")
	}

	inv, err := e.svc.CreateInvite(ctx, admin, org.ID, RoleReviewer, 0)
	if err != nil || inv.Token == "" {
		t.Fatalf("%+v %v", inv, err)
	}
	list, _ := e.svc.ListInvites(ctx, owner, org.ID)
	if len(list) != 1 || list[0].Token != "" {
		t.Fatalf("listing must never expose tokens: %+v", list)
	}
	var secret string
	e.pool.QueryRow(ctx, `SELECT encode(token_hash,'escape') FROM org_invites WHERE id=$1`, inv.ID).Scan(&secret)
	if secret == inv.Token {
		t.Fatal("token stored in plaintext")
	}

	// redemption grants the role, once
	newbie := e.user(t, "newbie")
	o, err := e.svc.AcceptInvite(ctx, newbie, inv.Token)
	if err != nil || o.ID != org.ID || o.Role != RoleReviewer || o.Slug != "invites" {
		t.Fatalf("%+v %v", o, err)
	}
	wantCode(t, func() error { _, err := e.svc.AcceptInvite(ctx, e.user(t, "late"), inv.Token); return err }(), "invite_invalid")
	if r, _ := roleOf(ctx, e.pool, org.ID, newbie.ID); r != RoleReviewer {
		t.Fatalf("role %q", r)
	}

	// garbage and unknown tokens look identical
	for _, tok := range []string{"", "x", inviteTokenPrefix + "AAAA", inviteTokenPrefix + string(make([]byte, 500))} {
		wantCode(t, func() error { _, err := e.svc.AcceptInvite(ctx, newbie, tok); return err }(), "invite_invalid")
	}

	// an existing member is told so and the invite is not burned
	inv2, _ := e.svc.CreateInvite(ctx, owner, org.ID, RoleAdmin, 0)
	_, err = e.svc.AcceptInvite(ctx, organizer, inv2.Token)
	wantCode(t, err, "already_member")
	if r, _ := roleOf(ctx, e.pool, org.ID, organizer.ID); r != RoleOrganizer {
		t.Fatalf("existing member's role must not change: %q", r)
	}
	if _, err := e.svc.AcceptInvite(ctx, e.user(t, "fresh"), inv2.Token); err != nil {
		t.Fatalf("invite wrongly consumed: %v", err)
	}

	// revocation (admins cannot revoke admin invites) and expiry
	inv3, _ := e.svc.CreateInvite(ctx, owner, org.ID, RoleAdmin, 0)
	if err := e.svc.RevokeInvite(ctx, admin, org.ID, inv3.ID); err != ErrForbidden {
		t.Fatalf("admin revoking admin invite: %v", err)
	}
	if err := e.svc.RevokeInvite(ctx, owner, org.ID, inv3.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, func() error { _, err := e.svc.AcceptInvite(ctx, e.user(t, "z"), inv3.Token); return err }(), "invite_invalid")
	if err := e.svc.RevokeInvite(ctx, owner, org.ID, inv3.ID); err != ErrNotFound {
		t.Fatalf("double revoke: %v", err)
	}

	inv4, _ := e.svc.CreateInvite(ctx, owner, org.ID, RoleReporter, time.Hour)
	e.advance(time.Hour + time.Second)
	wantCode(t, func() error { _, err := e.svc.AcceptInvite(ctx, e.user(t, "q"), inv4.Token); return err }(), "invite_invalid")

	// tenant isolation: another org's admin sees 404 for listing and revoking
	otherOwner := e.user(t, "oo")
	e.org(t, otherOwner, "elsewhere")
	if _, err := e.svc.ListInvites(ctx, otherOwner, org.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant list: %v", err)
	}
	if err := e.svc.RevokeInvite(ctx, otherOwner, org.ID, inv.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant revoke: %v", err)
	}
}

func TestInviteSingleUseUnderConcurrency(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.user(t, "owner")
	org := e.org(t, owner, "race")
	inv, err := e.svc.CreateInvite(ctx, owner, org.ID, RoleReporter, 0)
	if err != nil {
		t.Fatal(err)
	}
	users := make([]User, 20)
	for i := range users {
		users[i] = e.user(t, "u")
	}
	var ok atomic.Int32
	var wg sync.WaitGroup
	for _, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.AcceptInvite(ctx, u, inv.Token); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("single-use invite redeemed %d times", ok.Load())
	}
	ms, _ := e.svc.ListMembers(ctx, owner, org.ID)
	if len(ms) != 2 {
		t.Fatalf("members: %d", len(ms))
	}
}
