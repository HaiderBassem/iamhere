package qr

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	secret   = []byte("0123456789abcdef0123456789abcdef")
	rotation = 10 * time.Second
	t0       = time.Unix(1_800_000_005, 0)
)

func TestRoundTripAndManyScanners(t *testing.T) {
	id := uuid.New()
	tok := Sign(secret, id, t0, rotation)
	// the same token must verify for any number of independent callers
	for i := 0; i < 500; i++ {
		if err := Verify(secret, id, tok, t0.Add(2*time.Second), rotation, time.Second); err != nil {
			t.Fatalf("scanner %d: %v", i, err)
		}
	}
}

func TestRejects(t *testing.T) {
	id := uuid.New()
	tok := Sign(secret, id, t0, rotation)

	cases := []struct {
		name   string
		secret []byte
		id     uuid.UUID
		token  string
		now    time.Time
		want   error
	}{
		{"wrong secret", []byte("another-secret-another-secret-xx"), id, tok, t0, ErrBadMAC},
		{"wrong session", secret, uuid.New(), tok, t0, ErrBadMAC},
		{"garbage", secret, id, "not-a-token", t0, ErrMalformed},
		{"empty", secret, id, "", t0, ErrMalformed},
		{"replaced long ago", secret, id, tok, t0.Add(40 * time.Second), ErrExpired},
		{"far future", secret, id, tok, t0.Add(-60 * time.Second), ErrFuture},
	}
	for _, c := range cases {
		if err := Verify(c.secret, c.id, c.token, c.now, rotation, time.Second); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

func TestTamperedSlotRejected(t *testing.T) {
	id := uuid.New()
	old := Sign(secret, id, t0, rotation)
	fresh := Sign(secret, id, t0.Add(100*time.Second), rotation)
	// splice the fresh slot onto the old MAC: must fail the MAC check
	spliced := fresh[:len(fresh)-22] + old[len(old)-22:]
	if err := Verify(secret, id, spliced, t0.Add(100*time.Second), rotation, time.Second); err == nil {
		t.Fatal("spliced token accepted")
	}
}

// An old code stops working the moment the next one replaces it, apart from
// the allowance for a request already on its way.
func TestReplacedCodeExpires(t *testing.T) {
	id := uuid.New()
	slot := Slot(t0, rotation)
	tok := Sign(secret, id, SlotStart(slot, rotation), rotation)
	next := SlotStart(slot+1, rotation) // the moment the screen shows the next code
	for _, c := range []struct {
		name      string
		at        time.Time
		allowance time.Duration
		want      error
	}{
		{"while on screen", next.Add(-time.Millisecond), 0, nil},
		{"replaced, no allowance", next, 0, nil},
		{"1 ms after, no allowance", next.Add(time.Millisecond), 0, ErrExpired},
		{"in flight, within 1 s", next.Add(900 * time.Millisecond), time.Second, nil},
		{"after the allowance", next.Add(1100 * time.Millisecond), time.Second, ErrExpired},
		{"two codes later", next.Add(rotation), time.Second, ErrExpired},
	} {
		if err := Verify(secret, id, tok, c.at, rotation, c.allowance); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	// the next code, a moment early (another server's clock), is fine; two ahead is not
	if err := Verify(secret, id, Sign(secret, id, next, rotation), next.Add(-time.Second), rotation, 0); err != nil {
		t.Errorf("next code 1 s early: %v", err)
	}
	if err := Verify(secret, id, Sign(secret, id, next.Add(rotation), rotation), next.Add(-time.Second), rotation, 0); !errors.Is(err, ErrFuture) {
		t.Errorf("two codes ahead: %v", err)
	}
}

func TestSessionIDExtraction(t *testing.T) {
	id := uuid.New()
	got, err := SessionID(Sign(secret, id, t0, rotation))
	if err != nil || got != id {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestTicketLifecycle(t *testing.T) {
	sid, uid, other := uuid.New(), uuid.New(), uuid.New()
	tk := SignTicket(secret, sid, uid, t0)

	got, err := TicketSessionID(tk)
	if err != nil || got != sid {
		t.Fatalf("session id: %v %v", got, err)
	}
	if err := VerifyTicket(secret, sid, uid, tk, t0.Add(TicketTTL-time.Second)); err != nil {
		t.Fatalf("valid ticket: %v", err)
	}
	if err := VerifyTicket(secret, sid, uid, tk, t0.Add(TicketTTL+2*time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	// a ticket is bound to its user and its session, and to the session secret
	if err := VerifyTicket(secret, sid, other, tk, t0); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("other user: %v", err)
	}
	if err := VerifyTicket(secret, uuid.New(), uid, tk, t0); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("other session: %v", err)
	}
	if err := VerifyTicket([]byte("another-secret-another-secret-12"), sid, uid, tk, t0); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("other secret: %v", err)
	}
	// extending the expiry by editing the token must break the MAC
	raw := []byte(tk)
	raw[len(raw)/2] ^= 1
	if err := VerifyTicket(secret, sid, uid, string(raw), t0); err == nil {
		t.Fatal("tampered ticket accepted")
	}
	// a QR token is not a ticket and vice versa
	if _, err := TicketSessionID(Sign(secret, sid, t0, rotation)); err == nil {
		t.Fatal("QR token accepted as ticket")
	}
	if _, err := SessionID(tk); err == nil {
		t.Fatal("ticket accepted as QR token")
	}
}

func TestClaimLifecycle(t *testing.T) {
	secret, sid, t0 := []byte("0123456789abcdef0123456789abcdef"), uuid.New(), time.Unix(1_700_000_000, 0)
	c := SignClaim(secret, sid, t0)
	if got, err := ClaimSessionID(c); err != nil || got != sid {
		t.Fatalf("session id: %v %v", got, err)
	}
	if err := VerifyClaim(secret, sid, c, t0.Add(ClaimTTL-time.Second)); err != nil {
		t.Fatalf("valid claim: %v", err)
	}
	if err := VerifyClaim(secret, sid, c, t0.Add(ClaimTTL+2*time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired claim: %v", err)
	}
	if err := VerifyClaim(secret, uuid.New(), c, t0); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("other session: %v", err)
	}
	if err := VerifyClaim([]byte("another secret, another session!"), sid, c, t0); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("other secret: %v", err)
	}
	// a QR code is not a claim, and a claim is not a QR code (domain separation)
	code := Sign(secret, sid, t0, 5*time.Second)
	if err := VerifyClaim(secret, sid, code, t0); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("code accepted as claim: %v", err)
	}
	if err := Verify(secret, sid, c, t0, 5*time.Second, time.Second); err == nil {
		t.Fatal("claim accepted as code")
	}
}
