// Package app is the application layer: authorization, tenant scoping and
// transactions. HTTP handlers call it; it never touches net/http.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nyaruka/phonenumbers"

	"github.com/iamhere/api/internal/auth"
	"github.com/iamhere/api/internal/vision"
)

// ---- errors ---------------------------------------------------------------

// Error is a client-safe error with a stable machine-readable code.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func newErr(status int, code, msg string) *Error { return &Error{status, code, msg} }
func invalid(msg string) *Error                  { return newErr(400, "invalid_request", msg) }

var (
	ErrNotFound  = newErr(404, "not_found", "Not found.")
	ErrForbidden = newErr(403, "forbidden", "You do not have permission to do that.")
)

// Helpers for the HTTP layer.
func UnauthorizedErr() *Error      { return newErr(401, "unauthenticated", "Please sign in.") }
func BadRequest(msg string) *Error { return invalid(msg) }
func TooMany() *Error {
	return newErr(429, "rate_limited", "Too many requests. Please wait a moment and try again.")
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func isFKViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}

// ---- roles & permissions --------------------------------------------------

type Role string

const (
	RoleOwner     Role = "owner"
	RoleAdmin     Role = "admin"
	RoleOrganizer Role = "organizer"
	RoleReviewer  Role = "reviewer"
	RoleReporter  Role = "reporter"
)

type Perm int

const (
	PermManageOrg Perm = iota
	PermCreateSession
	PermViewAttendance
	PermViewContacts
	PermReview
	PermManual
	PermExport
)

var rolePerms = map[Role][]Perm{
	RoleOwner:     {PermManageOrg, PermCreateSession, PermViewAttendance, PermViewContacts, PermReview, PermManual, PermExport},
	RoleAdmin:     {PermManageOrg, PermCreateSession, PermViewAttendance, PermViewContacts, PermReview, PermManual, PermExport},
	RoleOrganizer: {PermCreateSession, PermViewAttendance, PermViewContacts, PermManual},
	RoleReviewer:  {PermViewAttendance, PermReview},
	RoleReporter:  {PermViewAttendance, PermViewContacts, PermExport},
}

func (r Role) Can(p Perm) bool {
	for _, x := range rolePerms[r] {
		if x == p {
			return true
		}
	}
	return false
}

func (r Role) Valid() bool { _, ok := rolePerms[r]; return ok }

// ---- service --------------------------------------------------------------

type Options struct {
	QRGraceSlots uint64
	CheckinGrace time.Duration
	MaxFixAge    time.Duration
	// Vision is the photo-check service client; nil means strict mode's photo
	// check is not available on this server.
	Vision *vision.Client
}

type Service struct {
	pool *pgxpool.Pool
	opt  Options
	vis  *vision.Client
	Now  func() time.Time // injectable clock; server-authoritative time
}

func New(pool *pgxpool.Pool, opt Options) *Service {
	return &Service{pool: pool, opt: opt, vis: opt.Vision, Now: func() time.Time { return time.Now().UTC() }}
}

type dbq interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func audit(ctx context.Context, q dbq, org *uuid.UUID, actor uuid.UUID, action, target string, detail any) error {
	b, _ := json.Marshal(detail)
	if detail == nil {
		b = []byte("{}")
	}
	_, err := q.Exec(ctx, `INSERT INTO audit_logs(org_id, actor_id, action, target, detail) VALUES($1,$2,$3,$4,$5)`,
		org, actor, action, target, b)
	return err
}

// roleOf returns the caller's role in the org, or ErrNotFound when not a
// member, so cross-tenant probing cannot distinguish "exists" from "absent".
func roleOf(ctx context.Context, q dbq, orgID, userID uuid.UUID) (Role, error) {
	var r Role
	err := q.QueryRow(ctx, `SELECT role::text FROM organization_memberships WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&r)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return r, err
}

func (s *Service) require(ctx context.Context, u User, orgID uuid.UUID, p Perm) (Role, error) {
	r, err := roleOf(ctx, s.pool, orgID, u.ID)
	if err != nil {
		return "", err
	}
	if !r.Can(p) {
		return r, ErrForbidden
	}
	return r, nil
}

// ---- input validation -----------------------------------------------------

// validText rejects control characters and bidi-override characters (which can
// visually reorder names in reports) but allows any language and script.
func validText(s string, min, max int, field string) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if !utf8.ValidString(s) || n < min || n > max {
		return "", invalid(field + " has an invalid length.")
	}
	for _, r := range s {
		if unicode.IsControl(r) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			return "", invalid(field + " contains unsupported characters.")
		}
	}
	return s, nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// ---- users ----------------------------------------------------------------

type User struct {
	ID       uuid.UUID `json:"id"`
	FullName string    `json:"full_name"`
	Email    *string   `json:"email,omitempty"`
	Phone    *string   `json:"phone,omitempty"`
}

const userCols = `id, full_name, email, phone_e164`

func scanUser(r pgx.Row) (User, error) {
	var u User
	return u, r.Scan(&u.ID, &u.FullName, &u.Email, &u.Phone)
}

// EnsureUser maps an external identity to a local user, creating it on first
// sight. Only a name is required; email and phone are never mandatory.
func (s *Service) EnsureUser(ctx context.Context, id auth.Identity) (User, error) {
	q := `SELECT ` + userCols + ` FROM users WHERE idp_issuer=$1 AND idp_subject=$2 AND deleted_at IS NULL`
	u, err := scanUser(s.pool.QueryRow(ctx, q, id.Issuer, id.Subject))
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return u, err
	}
	name, verr := validText(id.Name, 1, 200, "name")
	if verr != nil {
		name = "New user"
	}
	var email *string
	if e := strings.TrimSpace(id.Email); e != "" && len(e) <= 254 {
		email = &e
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO users(idp_issuer, idp_subject, full_name, email) VALUES($1,$2,$3,$4)
		ON CONFLICT (idp_issuer, idp_subject) DO NOTHING`, id.Issuer, id.Subject, name, email); err != nil {
		return User{}, err
	}
	return scanUser(s.pool.QueryRow(ctx, q, id.Issuer, id.Subject))
}

func (s *Service) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1 AND deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// UpdateProfile changes name and/or phone. Phone is parsed and validated with
// libphonenumber rules for any country and stored as E.164; "" clears it.
func (s *Service) UpdateProfile(ctx context.Context, u User, name, phone *string) (User, error) {
	if name != nil {
		n, err := validText(*name, 1, 200, "name")
		if err != nil {
			return u, err
		}
		if _, err := s.pool.Exec(ctx, `UPDATE users SET full_name=$2 WHERE id=$1`, u.ID, n); err != nil {
			return u, err
		}
	}
	if phone != nil {
		var e164 *string
		if p := strings.TrimSpace(*phone); p != "" {
			num, err := phonenumbers.Parse(p, "")
			if err != nil || !phonenumbers.IsValidNumber(num) {
				return u, invalid("Enter the phone number in international format, for example +964 750 123 4567.")
			}
			f := phonenumbers.Format(num, phonenumbers.E164)
			e164 = &f
		}
		if _, err := s.pool.Exec(ctx, `UPDATE users SET phone_e164=$2 WHERE id=$1`, u.ID, e164); err != nil {
			return u, err
		}
	}
	return s.UserByID(ctx, u.ID)
}

// DeleteAccount anonymizes the account: profile data and the per-record
// snapshots are erased, attendance rows remain (without personal data) so
// organization statistics stay consistent. Refused while the user is the sole
// owner of an organization.
func (s *Service) DeleteAccount(ctx context.Context, u User) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var blocked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM organization_memberships m
		WHERE m.user_id=$1 AND m.role='owner'
		AND NOT EXISTS (SELECT 1 FROM organization_memberships o WHERE o.org_id=m.org_id AND o.role='owner' AND o.user_id<>$1))`, u.ID).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return newErr(409, "sole_owner", "Transfer ownership of your organizations before deleting your account.")
	}
	stmts := []string{
		`DELETE FROM organization_memberships WHERE user_id=$1`,
		`UPDATE attendance_records SET full_name_snapshot='Deleted user', email_snapshot=NULL, phone_snapshot=NULL WHERE user_id=$1`,
		`UPDATE users SET full_name='Deleted user', email=NULL, phone_e164=NULL,
			idp_subject='deleted:'||id::text, deleted_at=now() WHERE id=$1`,
	}
	for _, st := range stmts {
		if _, err := tx.Exec(ctx, st, u.ID); err != nil {
			return err
		}
	}
	if err := audit(ctx, tx, nil, u.ID, "account.delete", u.ID.String(), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- organizations --------------------------------------------------------

type Org struct {
	ID       uuid.UUID `json:"id"`
	Slug     string    `json:"slug"`
	Name     string    `json:"name"`
	Country  *string   `json:"country,omitempty"`
	Timezone string    `json:"timezone"`
	Role     Role      `json:"role"`
}

const maxOwnedOrgs = 10 // blunt abuse limit against mass fake-organization creation

func (s *Service) CreateOrg(ctx context.Context, u User, name, slug, country, tz string) (Org, error) {
	name, err := validText(name, 1, 200, "name")
	if err != nil {
		return Org{}, err
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if !slugRe.MatchString(slug) {
		return Org{}, invalid("Identifier must be 2-63 characters: lowercase letters, digits and hyphens.")
	}
	if tz == "" {
		tz = "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return Org{}, invalid("Unknown timezone.")
	}
	var cc *string
	if country != "" {
		c := strings.ToUpper(country)
		if len(c) != 2 {
			return Org{}, invalid("Country must be a two-letter ISO code.")
		}
		cc = &c
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Org{}, err
	}
	defer tx.Rollback(ctx)

	var owned int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE user_id=$1 AND role='owner'`, u.ID).Scan(&owned); err != nil {
		return Org{}, err
	}
	if owned >= maxOwnedOrgs {
		return Org{}, newErr(429, "org_limit", "You have reached the organization limit.")
	}
	o := Org{Slug: slug, Name: name, Country: cc, Timezone: tz, Role: RoleOwner}
	err = tx.QueryRow(ctx, `INSERT INTO organizations(slug,name,country,timezone) VALUES($1,$2,$3,$4) RETURNING id`,
		slug, name, cc, tz).Scan(&o.ID)
	if isUnique(err) {
		return Org{}, newErr(409, "slug_taken", "That identifier is already taken.")
	}
	if err != nil {
		return Org{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_settings(org_id) VALUES($1)`, o.ID); err != nil {
		return Org{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_memberships(org_id,user_id,role) VALUES($1,$2,'owner')`, o.ID, u.ID); err != nil {
		return Org{}, err
	}
	if err := audit(ctx, tx, &o.ID, u.ID, "org.create", o.ID.String(), nil); err != nil {
		return Org{}, err
	}
	return o, tx.Commit(ctx)
}

func (s *Service) ListOrgs(ctx context.Context, u User) ([]Org, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.slug, o.name, o.country, o.timezone, m.role::text
		FROM organizations o JOIN organization_memberships m ON m.org_id=o.id WHERE m.user_id=$1 ORDER BY o.name, o.id`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Org{}
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.Slug, &o.Name, &o.Country, &o.Timezone, &o.Role); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ---- settings -------------------------------------------------------------

type Settings struct {
	VerificationMode string `json:"verification_mode"`
	DefaultDurationS int    `json:"default_duration_s"`
	GPSRadiusM       int    `json:"gps_radius_m"`
	QRRotationS      int    `json:"qr_rotation_s"`
	MaxGPSAccuracyM  int    `json:"max_gps_accuracy_m"`
}

func (s *Service) GetSettings(ctx context.Context, u User, orgID uuid.UUID) (Settings, error) {
	if _, err := s.require(ctx, u, orgID, PermViewAttendance); err != nil {
		return Settings{}, err
	}
	var st Settings
	err := s.pool.QueryRow(ctx, `SELECT verification_mode::text, default_duration_s, gps_radius_m, qr_rotation_s, max_gps_accuracy_m
		FROM organization_settings WHERE org_id=$1`, orgID).
		Scan(&st.VerificationMode, &st.DefaultDurationS, &st.GPSRadiusM, &st.QRRotationS, &st.MaxGPSAccuracyM)
	return st, err
}

// UpdateSettings affects only sessions created afterwards: each session
// snapshots its policy at creation, so a mid-session change cannot silently
// alter the rules attendees were checked against.
func (s *Service) UpdateSettings(ctx context.Context, u User, orgID uuid.UUID, st Settings) (Settings, error) {
	if _, err := s.require(ctx, u, orgID, PermManageOrg); err != nil {
		return Settings{}, err
	}
	in := func(v, lo, hi int) bool { return v >= lo && v <= hi }
	switch {
	case st.VerificationMode != "standard" && st.VerificationMode != "visual" && st.VerificationMode != "strict":
		return Settings{}, invalid("Unknown verification mode.")
	case !in(st.DefaultDurationS, 60, 86400), !in(st.GPSRadiusM, 5, 5000),
		!in(st.QRRotationS, 5, 120), !in(st.MaxGPSAccuracyM, 5, 2000):
		return Settings{}, invalid("A setting is outside its allowed range.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE organization_settings SET verification_mode=$2::verification_mode, default_duration_s=$3,
		gps_radius_m=$4, qr_rotation_s=$5, max_gps_accuracy_m=$6 WHERE org_id=$1`,
		orgID, st.VerificationMode, st.DefaultDurationS, st.GPSRadiusM, st.QRRotationS, st.MaxGPSAccuracyM); err != nil {
		return Settings{}, err
	}
	if err := audit(ctx, tx, &orgID, u.ID, "settings.update", orgID.String(), st); err != nil {
		return Settings{}, err
	}
	return st, tx.Commit(ctx)
}

// ---- members --------------------------------------------------------------

type Member struct {
	UserID   uuid.UUID `json:"user_id"`
	FullName string    `json:"full_name"`
	Role     Role      `json:"role"`
}

func (s *Service) ListMembers(ctx context.Context, u User, orgID uuid.UUID) ([]Member, error) {
	if _, err := roleOf(ctx, s.pool, orgID, u.ID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT m.user_id, us.full_name, m.role::text FROM organization_memberships m
		JOIN users us ON us.id=m.user_id WHERE m.org_id=$1 ORDER BY us.full_name, m.user_id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.FullName, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// lockMembers loads all memberships of an org under row locks so role changes
// and the "last owner" check are serialized.
func lockMembers(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (map[uuid.UUID]Role, error) {
	rows, err := tx.Query(ctx, `SELECT user_id, role::text FROM organization_memberships WHERE org_id=$1 FOR UPDATE`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[uuid.UUID]Role{}
	for rows.Next() {
		var id uuid.UUID
		var r Role
		if err := rows.Scan(&id, &r); err != nil {
			return nil, err
		}
		m[id] = r
	}
	return m, rows.Err()
}

func otherOwners(m map[uuid.UUID]Role, except uuid.UUID) int {
	n := 0
	for id, r := range m {
		if r == RoleOwner && id != except {
			n++
		}
	}
	return n
}

// SetMember adds a user or changes their role. Hierarchy: admins manage
// organizer/reviewer/reporter; only owners create or change owners and admins;
// an organization always keeps at least one owner.
func (s *Service) SetMember(ctx context.Context, actor User, orgID, target uuid.UUID, role Role) error {
	if !role.Valid() {
		return invalid("Unknown role.")
	}
	if _, err := s.UserByID(ctx, target); err != nil {
		return invalid("That user does not exist.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	members, err := lockMembers(ctx, tx, orgID)
	if err != nil {
		return err
	}
	ar, ok := members[actor.ID]
	if !ok {
		return ErrNotFound
	}
	if !ar.Can(PermManageOrg) {
		return ErrForbidden
	}
	existing, has := members[target]
	privileged := func(r Role) bool { return r == RoleOwner || r == RoleAdmin }
	if ar != RoleOwner && (privileged(role) || (has && privileged(existing))) {
		return ErrForbidden
	}
	if has && existing == RoleOwner && role != RoleOwner && otherOwners(members, target) == 0 {
		return newErr(409, "last_owner", "An organization must keep at least one owner.")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_memberships(org_id,user_id,role) VALUES($1,$2,$3::org_role)
		ON CONFLICT (org_id,user_id) DO UPDATE SET role=EXCLUDED.role`, orgID, target, role); err != nil {
		return err
	}
	if err := audit(ctx, tx, &orgID, actor.ID, "member.set", target.String(), map[string]any{"role": role}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveMember removes a member; anyone may remove themselves (leave).
func (s *Service) RemoveMember(ctx context.Context, actor User, orgID, target uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	members, err := lockMembers(ctx, tx, orgID)
	if err != nil {
		return err
	}
	ar, ok := members[actor.ID]
	if !ok {
		return ErrNotFound
	}
	existing, has := members[target]
	if !has {
		return ErrNotFound
	}
	if target != actor.ID {
		if !ar.Can(PermManageOrg) || (ar != RoleOwner && (existing == RoleOwner || existing == RoleAdmin)) {
			return ErrForbidden
		}
	}
	if existing == RoleOwner && otherOwners(members, target) == 0 {
		return newErr(409, "last_owner", "An organization must keep at least one owner.")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM organization_memberships WHERE org_id=$1 AND user_id=$2`, orgID, target); err != nil {
		return err
	}
	if err := audit(ctx, tx, &orgID, actor.ID, "member.remove", target.String(), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
