package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id with the OWASP-recommended minimum (19 MiB, 2 passes, 1 lane). The
// parameters are stored with each hash, so they can be raised later without
// breaking existing passwords.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// hashing costs memory; bound how many run at once so a burst of sign-ins
// cannot exhaust the server (excess requests wait their turn)
var hashSlots = make(chan struct{}, 4)

func argon(pw, salt []byte, t, m uint32, p uint8, n uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	return argon2.IDKey(pw, salt, t, m, p, n)
}

// HashPassword returns a PHC-format Argon2id hash.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errBadHash = errors.New("auth: malformed password hash")

// CheckPassword reports whether pw matches the stored hash, in constant time.
func CheckPassword(hash, pw string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var v int
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return false, errBadHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || m > 1<<20 || t == 0 || t > 10 || p == 0 {
		return false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 || len(want) > 64 {
		return false, errBadHash
	}
	got := argon([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is checked against when an email has no account, so a wrong email
// and a wrong password take the same time (no account enumeration by timing).
var dummyHash, _ = HashPassword("not-a-real-password-just-for-timing")

// CheckDummy burns the same work as a real password check.
func CheckDummy(pw string) { _, _ = CheckPassword(dummyHash, pw) }

// ValidatePassword applies the account password rules (NIST SP 800-63B: length
// over composition rules, and no well-known passwords). It returns a message
// for people, or "" when the password is acceptable.
func ValidatePassword(pw, email string) string {
	n := utf8.RuneCountInString(pw)
	switch {
	case !utf8.ValidString(pw):
		return "The password contains unsupported characters."
	case n < 8:
		return "Use at least 8 characters."
	case n > 128:
		return "Use at most 128 characters."
	case strings.Count(pw, string([]rune(pw)[0])) == n:
		return "Choose a password that is harder to guess."
	}
	low := strings.ToLower(pw)
	if commonPasswords[low] {
		return "That password is too common. Choose one that is harder to guess."
	}
	if local, _, ok := strings.Cut(strings.ToLower(email), "@"); ok && len(local) >= 4 && strings.Contains(low, local) {
		return "The password must not contain your email address."
	}
	return ""
}

// NewToken returns a random URL-safe token with a readable prefix (so leaked
// tokens are recognisable, e.g. by secret scanners) and the SHA-256 that is
// stored instead of the token itself.
func NewToken(prefix string) (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token = prefix + base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken is how a token is looked up: only its hash is ever stored.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// A short list of the most common passwords from public breach corpora. The
// length rule does most of the work; this stops the obvious ones.
var commonPasswords = func() map[string]bool {
	m := map[string]bool{}
	for _, p := range strings.Fields(`
		12345678 123456789 1234567890 12345678910 123123123 11111111 00000000 88888888 87654321 11223344
		password password1 password12 password123 passw0rd p@ssw0rd p@ssword qwertyui qwerty123 qwertyuiop
		1q2w3e4r 1q2w3e4r5t 1qaz2wsx zaq12wsx asdfghjk asdfasdf iloveyou iloveyou1 sunshine princess
		football baseball welcome1 welcome123 letmein1 admin123 administrator abc12345 abcd1234 aa123456
		superman batman123 trustno1 whatever starwars dragon123 monkey123 master123 computer internet
		changeme changeme1 secret123 football1 michael1 jennifer charlie1 shadow123 freedom1 mustang1
		attendance iamhere iamhere123 123qweasd qweasdzxc 147258369 987654321 112233445566 a1b2c3d4
	`) {
		m[p] = true
	}
	return m
}()
