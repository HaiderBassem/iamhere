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
		if err := Verify(secret, id, tok, t0.Add(2*time.Second), rotation, 2); err != nil {
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
		{"expired beyond grace", secret, id, tok, t0.Add(40 * time.Second), ErrExpired},
		{"far future", secret, id, tok, t0.Add(-60 * time.Second), ErrFuture},
	}
	for _, c := range cases {
		if err := Verify(c.secret, c.id, c.token, c.now, rotation, 2); !errors.Is(err, c.want) {
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
	if err := Verify(secret, id, spliced, t0.Add(100*time.Second), rotation, 2); err == nil {
		t.Fatal("spliced token accepted")
	}
}

func TestGraceBoundary(t *testing.T) {
	id := uuid.New()
	tok := Sign(secret, id, t0, rotation) // slot S
	if err := Verify(secret, id, tok, t0.Add(20*time.Second), rotation, 2); err != nil {
		t.Fatalf("2 slots old should pass: %v", err)
	}
	if err := Verify(secret, id, tok, t0.Add(30*time.Second), rotation, 2); !errors.Is(err, ErrExpired) {
		t.Fatalf("3 slots old should expire: %v", err)
	}
}

func TestSessionIDExtraction(t *testing.T) {
	id := uuid.New()
	got, err := SessionID(Sign(secret, id, t0, rotation))
	if err != nil || got != id {
		t.Fatalf("got %v %v", got, err)
	}
}
