// Package qr issues and verifies rotating, stateless QR tokens.
//
// A token is NOT single-use: hundreds of attendees scan the same displayed
// code. It proves only "this request holds the code that is on the organizer's
// screen right now" (an old code stops working the moment it is replaced). It does not prove physical presence
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

// MaxSkew is how early a code may arrive before its slot starts: the clocks of
// two API replicas may differ a little. It never makes an old code valid.
const MaxSkew = 2 * time.Second

// SlotStart is when slot begins.
func SlotStart(slot uint64, rotation time.Duration) time.Time {
	return time.Unix(int64(slot)*int64(rotation.Seconds()), 0)
}

// Verify checks the MAC in constant time and that the token is the code on the
// screen now. A code stops being valid when the next one replaces it; only a
// request that arrives within `allowance` of that moment (it was sent while the
// code was still showing) is accepted. allowance 0 = the current code only.
func Verify(secret []byte, sessionID uuid.UUID, token string, now time.Time, rotation, allowance time.Duration) error {
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
	switch {
	case slot == cur:
		return nil
	case slot > cur:
		if slot == cur+1 && SlotStart(slot, rotation).Sub(now) <= MaxSkew {
			return nil
		}
		return ErrFuture
	default:
		// replaced at the start of the slot after it
		if now.Sub(SlotStart(slot+1, rotation)) <= allowance {
			return nil
		}
		return ErrExpired
	}
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

// ---- sign-up claims ---------------------------------------------------------
//
// Someone without an account scans the code, then needs about 20 seconds to
// create one: longer than the code lives. A claim holds that fresh scan through
// sign-up. It is bound to the session but not to a user (there is none yet), so
// it is only accepted together with the creation of a new account, and only
// for ClaimTTL. Domain separation keeps claims, codes and tickets apart even
// though a claim has the same length as a code.

const claimDomain = "iah-claim-v1"

// ClaimTTL is how long a scan is held while the person creates an account.
const ClaimTTL = 5 * time.Minute

func claimMAC(secret []byte, sessionID uuid.UUID, exp uint64) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(claimDomain))
	h.Write(sessionID[:])
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], exp)
	h.Write(b[:])
	return h.Sum(nil)[:macLen]
}

// SignClaim issues a claim on the session valid until now+ClaimTTL.
func SignClaim(secret []byte, sessionID uuid.UUID, now time.Time) string {
	exp := uint64(now.Add(ClaimTTL).Unix())
	buf := make([]byte, 0, tokenLen)
	buf = append(buf, sessionID[:]...)
	buf = binary.BigEndian.AppendUint64(buf, exp)
	buf = append(buf, claimMAC(secret, sessionID, exp)...)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// ClaimSessionID extracts the (unauthenticated) session id; confirm with VerifyClaim.
func ClaimSessionID(claim string) (uuid.UUID, error) { return SessionID(claim) }

// VerifyClaim checks the MAC in constant time and the expiry.
func VerifyClaim(secret []byte, sessionID uuid.UUID, claim string, now time.Time) error {
	raw, err := base64.RawURLEncoding.DecodeString(claim)
	if err != nil || len(raw) != tokenLen {
		return ErrMalformed
	}
	var sid uuid.UUID
	copy(sid[:], raw[:16])
	exp := binary.BigEndian.Uint64(raw[16:24])
	if sid != sessionID || !hmac.Equal(raw[24:], claimMAC(secret, sessionID, exp)) {
		return ErrBadMAC
	}
	if uint64(now.Unix()) > exp {
		return ErrExpired
	}
	return nil
}
