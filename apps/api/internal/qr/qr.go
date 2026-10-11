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
