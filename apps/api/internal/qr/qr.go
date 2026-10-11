// Package qr issues and verifies rotating, stateless QR tokens.
//
// A token is NOT single-use: hundreds of attendees scan the same displayed
// code. It proves only "this request holds a code that was on the organizer's
// screen within the last few rotations". It does not prove physical presence
// (screenshots and relays can forward it); that is the job of other evidence.
package qr

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	macLen   = 16
	tokenLen = 16 + 8 + macLen
	domain   = "iah-qr-v1"
)

var (
	ErrMalformed = errors.New("qr: malformed token")
	ErrBadMAC    = errors.New("qr: invalid token")
	ErrExpired   = errors.New("qr: token expired")
	ErrFuture    = errors.New("qr: token from the future")
)

// Slot returns the rotation slot for t. Server clocks only; never client time.
func Slot(t time.Time, rotation time.Duration) uint64 {
	return uint64(t.Unix()) / uint64(rotation.Seconds())
}

func mac(secret []byte, sessionID uuid.UUID, slot uint64) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(domain))
	h.Write(sessionID[:])
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], slot)
	h.Write(b[:])
	return h.Sum(nil)[:macLen]
}

// Sign returns the token valid for the slot containing now.
func Sign(secret []byte, sessionID uuid.UUID, now time.Time, rotation time.Duration) string {
	slot := Slot(now, rotation)
	buf := make([]byte, 0, tokenLen)
	buf = append(buf, sessionID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, slot)
	buf = append(buf, mac(secret, sessionID, slot)...)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// SessionID extracts the (unauthenticated) session id so the caller can load
// the session secret. The result must be confirmed with Verify.
func SessionID(token string) (uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != tokenLen {
		return uuid.Nil, ErrMalformed
	}
	var id uuid.UUID
	copy(id[:], raw[:16])
	return id, nil
}

// Verify checks the MAC in constant time and that the token's slot is no older
// than `grace` slots (covers scan time + network delay) and not ahead of now
// by more than one slot (clock skew between replicas).
func Verify(secret []byte, sessionID uuid.UUID, token string, now time.Time, rotation time.Duration, grace uint64) error {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != tokenLen {
		return ErrMalformed
	}
	var id uuid.UUID
	copy(id[:], raw[:16])
	if id != sessionID {
		return ErrBadMAC
	}
	slot := binary.BigEndian.Uint64(raw[16:24])
	if !hmac.Equal(raw[24:], mac(secret, sessionID, slot)) {
		return ErrBadMAC
	}
	cur := Slot(now, rotation)
	if slot > cur+1 {
		return ErrFuture
	}
	if cur > slot && cur-slot > grace {
		return ErrExpired
	}
	return nil
}

// ---- check-in tickets -------------------------------------------------------
//
// With a 5-second rotation a person cannot always finish a multi-step check-in
// (location, camera) before the code they scanned expires. So the QR is checked
// once, at scan time, and the server hands back a ticket: a short-lived token
// bound to this session AND this user, proving "this user presented a fresh
// code a moment ago". Only a fresh scan can mint a ticket, so a ticket extends
// nothing for someone who never saw the screen.

const (
	ticketLen    = 16 + 16 + 8 + macLen
	ticketDomain = "iah-ticket-v1"
)

// TicketTTL is how long after a fresh scan the remaining steps may be completed.
const TicketTTL = 120 * time.Second

func ticketMAC(secret []byte, sessionID, userID uuid.UUID, exp uint64) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(ticketDomain))
	h.Write(sessionID[:])
	h.Write(userID[:])
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], exp)
	h.Write(b[:])
	return h.Sum(nil)[:macLen]
}

// SignTicket issues a ticket for (session, user) valid until now+TicketTTL.
func SignTicket(secret []byte, sessionID, userID uuid.UUID, now time.Time) string {
	exp := uint64(now.Add(TicketTTL).Unix())
	buf := make([]byte, 0, ticketLen)
	buf = append(buf, sessionID[:]...)
	buf = append(buf, userID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, exp)
	buf = append(buf, ticketMAC(secret, sessionID, userID, exp)...)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// TicketSessionID extracts the (unauthenticated) session id so the caller can
// load the session secret; confirm with VerifyTicket.
func TicketSessionID(ticket string) (uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil || len(raw) != ticketLen {
		return uuid.Nil, ErrMalformed
	}
	var id uuid.UUID
	copy(id[:], raw[:16])
	return id, nil
}

// VerifyTicket checks the MAC in constant time, that the ticket belongs to this
// session and user, and that it has not expired.
func VerifyTicket(secret []byte, sessionID, userID uuid.UUID, ticket string, now time.Time) error {
	raw, err := base64.RawURLEncoding.DecodeString(ticket)
	if err != nil || len(raw) != ticketLen {
		return ErrMalformed
	}
	var sid, uid uuid.UUID
	copy(sid[:], raw[:16])
	copy(uid[:], raw[16:32])
	exp := binary.BigEndian.Uint64(raw[32:40])
	if sid != sessionID || uid != userID || !hmac.Equal(raw[40:], ticketMAC(secret, sessionID, userID, exp)) {
		return ErrBadMAC
	}
	if uint64(now.Unix()) > exp {
		return ErrExpired
	}
	return nil
}
