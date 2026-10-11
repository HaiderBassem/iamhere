package app

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iamhere/api/internal/auth"
	imail "github.com/iamhere/api/internal/mail"
)

// Built-in accounts: email + password, opaque bearer sessions, and one-time
// links by email for confirming the address and resetting the password.

const (
	sessionPrefix  = "iah_s_"
	linkPrefix     = "iah_l_"
	sessionTTL     = 30 * 24 * time.Hour // sliding: renewed while the device is used
	sessionSlideAt = time.Hour           // renew at most this often (one write per hour per device)
	resetTTL       = time.Hour
	verifyTTL      = 3 * 24 * time.Hour

	maxFailedSignIns = 10               // per email address...
	failWindow       = 15 * time.Minute // ...in this window, then sign-in pauses for it
)

// SignedIn is what signing in returns: a bearer token for the Authorization
// header (shown once; only its hash is stored) and who it belongs to.
type SignedIn struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

// Account is the signed-in person's sign-in details.
type Account struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

func errSignIn() *Error {
	return newErr(401, "wrong_credentials", "That email and password don’t match. Check them and try again.")
}

// normEmail lower-cases and validates an address; the domain must have a dot
// (catches "name@gmail", a common typo).
func normEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(e)
	_, domain, _ := strings.Cut(e, "@")
	if err != nil || a.Address != e || len(e) > 254 || !strings.Contains(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", newErr(400, "invalid_email", "Enter a valid email address, like name@example.com.")
	}
	return e, nil
}

func checkPassword(pw, email string) error {
	if msg := auth.ValidatePassword(pw, email); msg != "" {
		return newErr(400, "weak_password", msg)
	}
	return nil
}

// failures counts recent wrong passwords per email address, so guessing is
// slowed down for that account whatever the attacker's IP. In memory: one
// server process (see docs/BACKEND.md).
type failures struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

func (f *failures) blocked(email string, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	recent := f.m[email][:0]
	for _, t := range f.m[email] {
		if now.Sub(t) < failWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(f.m, email)
	} else {
		f.m[email] = recent
	}
	return len(recent) >= maxFailedSignIns
}

func (f *failures) add(email string, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string][]time.Time{}
	}
	if len(f.m) > 50000 { // bound memory under a spray of addresses
		for k, ts := range f.m {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > failWindow {
				delete(f.m, k)
			}
		}
	}
	f.m[email] = append(f.m[email], now)
}

func (f *failures) clear(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, email)
}

// newSession stores a new signed-in device for the user.
func (s *Service) newSession(ctx context.Context, q dbq, userID uuid.UUID, userAgent string) (string, error) {
	tok, hash, err := auth.NewToken(sessionPrefix)
	if err != nil {
		return "", err
	}
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	var ua *string
	if userAgent != "" {
		ua = &userAgent
	}
	_, err = q.Exec(ctx, `INSERT INTO auth_sessions(user_id, token_hash, user_agent, expires_at) VALUES($1,$2,$3,$4)`,
		userID, hash, ua, s.Now().Add(sessionTTL))
	return tok, err
}

// SignUp creates an account and signs it in. Only a name, an email address
// and a password are needed.
func (s *Service) SignUp(ctx context.Context, name, email, password, userAgent string) (SignedIn, error) {
	n, err := validText(name, 1, 200, "name")
	if err != nil {
		return SignedIn{}, newErr(400, "invalid_name", "Enter your name.")
	}
	e, err := normEmail(email)
	if err != nil {
		return SignedIn{}, err
	}
	if err := checkPassword(password, e); err != nil {
		return SignedIn{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return SignedIn{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SignedIn{}, err
	}
	defer tx.Rollback(ctx)
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO users(id, idp_issuer, idp_subject, full_name, email) VALUES($1,'local',$2,$3,$4)`, id, id.String(), n, e); err != nil {
		return SignedIn{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO accounts(user_id, email, password_hash) VALUES($1,$2,$3)`, id, e, hash); err != nil {
		if isUnique(err) {
			return SignedIn{}, newErr(409, "email_taken", "There is already an account with this email. Sign in instead, or reset the password.")
		}
		return SignedIn{}, err
	}
	tok, err := s.newSession(ctx, tx, id, userAgent)
	if err != nil {
		return SignedIn{}, err
	}
	if err := audit(ctx, tx, nil, id, "account.create", id.String(), nil); err != nil {
		return SignedIn{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SignedIn{}, err
	}
	u, err := s.UserByID(ctx, id)
	if err != nil {
		return SignedIn{}, err
	}
	if s.mailOn() {
		_ = s.sendLink(ctx, id, e, n, "verify_email")
	}
	return SignedIn{Token: tok, User: u}, nil
}

// SignIn checks an email and password. Wrong email and wrong password get the
// same answer in the same time, so it cannot be used to find who has an account.
func (s *Service) SignIn(ctx context.Context, email, password, userAgent string) (SignedIn, error) {
	e, err := normEmail(email)
	if err != nil {
		auth.CheckDummy(password)
		return SignedIn{}, errSignIn()
	}
	now := s.Now()
	if s.fails.blocked(e, now) {
		return SignedIn{}, newErr(429, "too_many_attempts", "Too many wrong passwords for this account. Wait 15 minutes, or reset the password.")
	}
	var id uuid.UUID
	var hash string
	err = s.pool.QueryRow(ctx, `SELECT a.user_id, a.password_hash FROM accounts a JOIN users u ON u.id=a.user_id
		WHERE a.email=$1 AND u.deleted_at IS NULL`, e).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		auth.CheckDummy(password)
		s.fails.add(e, now)
		return SignedIn{}, errSignIn()
	}
	if err != nil {
		return SignedIn{}, err
	}
	ok, err := auth.CheckPassword(hash, password)
	if err != nil {
		return SignedIn{}, err
	}
	if !ok {
		s.fails.add(e, now)
		return SignedIn{}, errSignIn()
	}
	s.fails.clear(e)
	tok, err := s.newSession(ctx, s.pool, id, userAgent)
	if err != nil {
		return SignedIn{}, err
	}
	u, err := s.UserByID(ctx, id)
	return SignedIn{Token: tok, User: u}, err
}

// SessionUser resolves a bearer session token to its user, renewing the
// session's expiry while the device keeps being used.
func (s *Service) SessionUser(ctx context.Context, token string) (User, uuid.UUID, error) {
	if !strings.HasPrefix(token, sessionPrefix) || len(token) > 100 {
		return User{}, uuid.Nil, UnauthorizedErr()
	}
	var u User
	var sid uuid.UUID
	var seen time.Time
	now := s.Now()
	err := s.pool.QueryRow(ctx, `SELECT s.id, s.last_seen_at, u.id, u.full_name, u.email, u.phone_e164
		FROM auth_sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.expires_at > $2 AND u.deleted_at IS NULL`, auth.HashToken(token), now).
		Scan(&sid, &seen, &u.ID, &u.FullName, &u.Email, &u.Phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, uuid.Nil, UnauthorizedErr()
	}
	if err != nil {
		return User{}, uuid.Nil, err
	}
	if now.Sub(seen) > sessionSlideAt {
		if _, err := s.pool.Exec(ctx, `UPDATE auth_sessions SET last_seen_at=$2, expires_at=$3 WHERE id=$1`, sid, now, now.Add(sessionTTL)); err != nil {
			return User{}, uuid.Nil, err
		}
	}
	return u, sid, nil
}

// SignOut ends one session (this device).
func (s *Service) SignOut(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_sessions WHERE id=$1`, sessionID)
	return err
}

// SignOutOthers ends every session of the user except the given one.
func (s *Service) SignOutOthers(ctx context.Context, u User, keep uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM auth_sessions WHERE user_id=$1 AND id<>$2`, u.ID, keep)
	return tag.RowsAffected(), err
}

// AccountOf returns the user's sign-in details.
func (s *Service) AccountOf(ctx context.Context, u User) (Account, error) {
	var a Account
	var verified *time.Time
	err := s.pool.QueryRow(ctx, `SELECT email, email_verified_at FROM accounts WHERE user_id=$1`, u.ID).Scan(&a.Email, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	a.EmailVerified = verified != nil
	return a, err
}

// ChangePassword needs the current password; other devices are signed out.
func (s *Service) ChangePassword(ctx context.Context, u User, keep uuid.UUID, current, next string) error {
	var hash, email string
	err := s.pool.QueryRow(ctx, `SELECT password_hash, email FROM accounts WHERE user_id=$1`, u.ID).Scan(&hash, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ok, err := auth.CheckPassword(hash, current); err != nil || !ok {
		if err != nil {
			return err
		}
		return newErr(400, "wrong_password", "Your current password is not right.")
	}
	return s.setPassword(ctx, u.ID, email, next, &keep)
}

// setPassword stores a new password and ends sessions (all, or all but keep).
func (s *Service) setPassword(ctx context.Context, userID uuid.UUID, email, pw string, keep *uuid.UUID) error {
	if err := checkPassword(pw, email); err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE accounts SET password_hash=$2, password_changed_at=now() WHERE user_id=$1`, userID, hash); err != nil {
		return err
	}
	k := uuid.Nil
	if keep != nil {
		k = *keep
	}
	if _, err := tx.Exec(ctx, `DELETE FROM auth_sessions WHERE user_id=$1 AND id<>$2`, userID, k); err != nil {
		return err
	}
	// any other reset link still in someone's inbox stops working
	if _, err := tx.Exec(ctx, `UPDATE auth_tokens SET used_at=now() WHERE user_id=$1 AND purpose='reset_password' AND used_at IS NULL`, userID); err != nil {
		return err
	}
	if err := audit(ctx, tx, nil, userID, "account.password", userID.String(), nil); err != nil {
		return err
	}
	s.fails.clear(email)
	return tx.Commit(ctx)
}

// ---- one-time links -------------------------------------------------------------

func (s *Service) mailOn() bool { return s.mail != nil && s.mail.Enabled() && s.opt.PublicURL != "" }

// MailEnabled tells the web app whether email links (reset, confirm) exist.
func (s *Service) MailEnabled() bool { return s.mailOn() }

// newLink stores a one-time link token and returns the URL to send.
func (s *Service) newLink(ctx context.Context, userID uuid.UUID, email, purpose string) (string, error) {
	tok, hash, err := auth.NewToken(linkPrefix)
	if err != nil {
		return "", err
	}
	ttl := resetTTL
	if purpose == "verify_email" {
		ttl = verifyTTL
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO auth_tokens(token_hash, user_id, purpose, email, expires_at) VALUES($1,$2,$3,$4,$5)`,
		hash, userID, purpose, email, s.Now().Add(ttl)); err != nil {
		return "", err
	}
	path := map[string]string{"verify_email": "#/verify/", "reset_password": "#/reset/"}[purpose]
	return strings.TrimRight(s.opt.PublicURL, "/") + "/" + path + tok, nil
}

// sendLink creates a link and emails it. Delivery happens in the background,
// so the answer (and its timing) never depends on the mail server.
func (s *Service) sendLink(ctx context.Context, userID uuid.UUID, email, name, purpose string) error {
	link, err := s.newLink(ctx, userID, email, purpose)
	if err != nil {
		return err
	}
	m := linkMail(purpose, email, name, link, s.opt.ProductName)
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := s.mail.Send(c, m); err != nil && s.opt.Log != nil {
			s.opt.Log.Error("send email", "purpose", purpose, "err", err.Error())
		}
	}()
	return nil
}

func linkMail(purpose, to, name, link, product string) imail.Message {
	if product == "" {
		product = "I Am Here"
	}
	var subject, intro, button, outro string
	switch purpose {
	case "reset_password":
		subject = "Reset your " + product + " password"
		intro = "Someone (hopefully you) asked to reset the password of your " + product + " account."
		button = "Choose a new password"
		outro = "The link works once, for one hour. If you did not ask for this, ignore this email: your password stays the same."
	default:
		subject = "Confirm your email for " + product
		intro = "Welcome to " + product + "! Please confirm this is your email address."
		button = "Confirm my email"
		outro = "The link works for 3 days. If you did not create an account, ignore this email."
	}
	greet := "Hi " + name + ","
	text := fmt.Sprintf("%s\n\n%s\n\n%s:\n%s\n\n%s\n\n%s", greet, intro, button, link, outro, product)
	e := html.EscapeString
	htm := fmt.Sprintf(`<!doctype html><html><body style="margin:0;background:#f4f5f8;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1b1d2a">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:32px 16px">
<table role="presentation" width="100%%" style="max-width:480px;background:#ffffff;border-radius:16px;padding:32px" cellpadding="0" cellspacing="0"><tr><td>
<p style="font-size:18px;font-weight:700;margin:0 0 24px">%s</p>
<p style="font-size:15px;line-height:1.6;margin:0 0 8px">%s</p>
<p style="font-size:15px;line-height:1.6;margin:0 0 24px">%s</p>
<p style="margin:0 0 24px"><a href="%s" style="display:inline-block;background:#4338ca;color:#ffffff;text-decoration:none;font-weight:600;padding:12px 20px;border-radius:10px">%s</a></p>
<p style="font-size:13px;line-height:1.6;color:#5b5f73;margin:0 0 8px">%s</p>
<p style="font-size:12px;line-height:1.6;color:#8a8ea3;margin:0;word-break:break-all">%s</p>
</td></tr></table></td></tr></table></body></html>`,
		e(product), e(greet), e(intro), e(link), e(button), e(outro), e(link))
	return imail.Message{To: to, Subject: subject, Text: text, HTML: htm}
}

// RequestPasswordReset emails a reset link if the address has an account. The
// answer is the same either way.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	if !s.mailOn() {
		return newErr(503, "email_unavailable", "Password reset by email is not available on this server. Ask the person who runs it for a reset link.")
	}
	e, err := normEmail(email)
	if err != nil {
		return err
	}
	var id uuid.UUID
	var name string
	var recent int
	err = s.pool.QueryRow(ctx, `SELECT u.id, u.full_name,
		(SELECT count(*) FROM auth_tokens t WHERE t.user_id=u.id AND t.purpose='reset_password' AND t.created_at > now() - interval '1 hour')
		FROM accounts a JOIN users u ON u.id=a.user_id WHERE a.email=$1 AND u.deleted_at IS NULL`, e).Scan(&id, &name, &recent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if recent >= 5 { // do not let anyone flood an inbox
		return nil
	}
	return s.sendLink(ctx, id, e, name, "reset_password")
}

// ResetLink makes a reset link without sending it (for the server's operator,
// `api reset-link`, when there is no email service).
func (s *Service) ResetLink(ctx context.Context, email string) (string, error) {
	if s.opt.PublicURL == "" {
		return "", errors.New("set IAH_PUBLIC_URL (the address of the web app) first")
	}
	e, err := normEmail(email)
	if err != nil {
		return "", err
	}
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, `SELECT a.user_id FROM accounts a JOIN users u ON u.id=a.user_id WHERE a.email=$1 AND u.deleted_at IS NULL`, e).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("no account with the email %s", e)
	}
	if err != nil {
		return "", err
	}
	return s.newLink(ctx, id, e, "reset_password")
}

// useLink consumes a one-time link, returning whose it is.
func (s *Service) useLink(ctx context.Context, tx pgx.Tx, token, purpose string) (uuid.UUID, string, error) {
	bad := newErr(410, "link_expired", "This link has expired or was already used. Ask for a new one.")
	if !strings.HasPrefix(token, linkPrefix) || len(token) > 100 {
		return uuid.Nil, "", bad
	}
	var id uuid.UUID
	var email string
	err := tx.QueryRow(ctx, `UPDATE auth_tokens SET used_at=now()
		WHERE token_hash=$1 AND purpose=$2 AND used_at IS NULL AND expires_at > $3
		RETURNING user_id, email`, auth.HashToken(token), purpose, s.Now()).Scan(&id, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", bad
	}
	return id, email, err
}

// ResetPassword sets a new password from an emailed link, signs out every
// other device, and signs this one in.
func (s *Service) ResetPassword(ctx context.Context, token, password, userAgent string) (SignedIn, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SignedIn{}, err
	}
	defer tx.Rollback(ctx)
	id, _, err := s.useLink(ctx, tx, token, "reset_password")
	if err != nil {
		return SignedIn{}, err
	}
	var email string
	if err := tx.QueryRow(ctx, `SELECT email FROM accounts WHERE user_id=$1`, id).Scan(&email); err != nil {
		return SignedIn{}, err
	}
	if err := checkPassword(password, email); err != nil {
		return SignedIn{}, err // the link is not consumed: they can try another password
	}
	if err := tx.Commit(ctx); err != nil {
		return SignedIn{}, err
	}
	if err := s.setPassword(ctx, id, email, password, nil); err != nil {
		return SignedIn{}, err
	}
	// following the link proves the address too
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET email_verified_at=coalesce(email_verified_at, now()) WHERE user_id=$1`, id); err != nil {
		return SignedIn{}, err
	}
	tok, err := s.newSession(ctx, s.pool, id, userAgent)
	if err != nil {
		return SignedIn{}, err
	}
	u, err := s.UserByID(ctx, id)
	return SignedIn{Token: tok, User: u}, err
}

// ConfirmEmail marks the address as confirmed, if it is still the account's.
func (s *Service) ConfirmEmail(ctx context.Context, token string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id, email, err := s.useLink(ctx, tx, token, "verify_email")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET email_verified_at=coalesce(email_verified_at, now()) WHERE user_id=$1 AND email=$2`, id, email); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ResendConfirmation emails a new confirmation link to the signed-in user.
func (s *Service) ResendConfirmation(ctx context.Context, u User) error {
	if !s.mailOn() {
		return newErr(503, "email_unavailable", "Sending email is not set up on this server.")
	}
	a, err := s.AccountOf(ctx, u)
	if err != nil {
		return err
	}
	if a.EmailVerified {
		return nil
	}
	var recent int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM auth_tokens WHERE user_id=$1 AND purpose='verify_email' AND created_at > now() - interval '1 hour'`, u.ID).Scan(&recent); err != nil {
		return err
	}
	if recent >= 5 {
		return TooMany()
	}
	return s.sendLink(ctx, u.ID, a.Email, u.FullName, "verify_email")
}

// PurgeAuth deletes expired sessions and links.
func (s *Service) PurgeAuth(ctx context.Context) error {
	now := s.Now()
	if _, err := s.pool.Exec(ctx, `DELETE FROM auth_sessions WHERE expires_at < $1`, now); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_tokens WHERE expires_at < $1`, now.Add(-24*time.Hour))
	return err
}
