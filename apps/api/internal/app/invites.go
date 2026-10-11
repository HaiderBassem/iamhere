package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	maxOpenInvites    = 100
	defaultInviteTTL  = 7 * 24 * time.Hour
	maxInviteTTL      = 30 * 24 * time.Hour
	inviteTokenPrefix = "iah_inv_"
)

type Invite struct {
	ID        uuid.UUID `json:"id"`
	Role      Role      `json:"role"`
	CreatedBy uuid.UUID `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// Token is returned exactly once, at creation; only its hash is stored.
	Token string `json:"token,omitempty"`
}

func hashInvite(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// CreateInvite issues a single-use link that adds whoever redeems it to the
// organization with the given role. The same hierarchy as SetMember applies:
// admins may invite organizers, reviewers and reporters; only owners invite
// admins. Owners are never created by invitation.
func (s *Service) CreateInvite(ctx context.Context, actor User, orgID uuid.UUID, role Role, ttl time.Duration) (Invite, error) {
	ar, err := s.require(ctx, actor, orgID, PermManageOrg)
	if err != nil {
		return Invite{}, err
	}
	if !role.Valid() || role == RoleOwner {
		return Invite{}, invalid("Invitations can grant admin, organizer, reviewer or reporter.")
	}
	if ar != RoleOwner && role == RoleAdmin {
		return Invite{}, ErrForbidden
	}
	if ttl == 0 {
		ttl = defaultInviteTTL
	}
	if ttl < time.Hour || ttl > maxInviteTTL {
		return Invite{}, invalid("Expiry must be between 1 hour and 30 days.")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Invite{}, err
	}
	token := inviteTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invite{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id=$1 FOR UPDATE`, orgID); err != nil {
		return Invite{}, err
	}
	now := s.Now()
	var open int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM org_invites WHERE org_id=$1 AND used_at IS NULL AND expires_at > $2`, orgID, now).Scan(&open); err != nil {
		return Invite{}, err
	}
	if open >= maxOpenInvites {
		return Invite{}, newErr(429, "invite_limit", "Too many open invitations. Revoke some first.")
	}
	inv := Invite{Role: role, CreatedBy: actor.ID, CreatedAt: now, ExpiresAt: now.Add(ttl), Token: token}
	if err := tx.QueryRow(ctx, `INSERT INTO org_invites(org_id, role, token_hash, created_by, created_at, expires_at)
		VALUES($1,$2::org_role,$3,$4,$5,$6) RETURNING id`,
		orgID, role, hashInvite(token), actor.ID, inv.CreatedAt, inv.ExpiresAt).Scan(&inv.ID); err != nil {
		return Invite{}, err
	}
	if err := audit(ctx, tx, &orgID, actor.ID, "invite.create", inv.ID.String(), map[string]any{"role": role}); err != nil {
		return Invite{}, err
	}
	return inv, tx.Commit(ctx)
}

// ListInvites returns open (unused, unexpired) invitations without secrets.
func (s *Service) ListInvites(ctx context.Context, actor User, orgID uuid.UUID) ([]Invite, error) {
	if _, err := s.require(ctx, actor, orgID, PermManageOrg); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, role::text, created_by, created_at, expires_at FROM org_invites
		WHERE org_id=$1 AND used_at IS NULL AND expires_at > $2 ORDER BY created_at DESC, id`, orgID, s.Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var i Invite
		if err := rows.Scan(&i.ID, &i.Role, &i.CreatedBy, &i.CreatedAt, &i.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// RevokeInvite deletes an open invitation. Admins cannot revoke admin invites.
func (s *Service) RevokeInvite(ctx context.Context, actor User, orgID, inviteID uuid.UUID) error {
	ar, err := s.require(ctx, actor, orgID, PermManageOrg)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role Role
	err = tx.QueryRow(ctx, `SELECT role::text FROM org_invites WHERE id=$1 AND org_id=$2 AND used_at IS NULL FOR UPDATE`, inviteID, orgID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if ar != RoleOwner && role == RoleAdmin {
		return ErrForbidden
	}
	if _, err := tx.Exec(ctx, `DELETE FROM org_invites WHERE id=$1`, inviteID); err != nil {
		return err
	}
	if err := audit(ctx, tx, &orgID, actor.ID, "invite.revoke", inviteID.String(), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AcceptInvite redeems a token for the caller. Unknown, expired, used and
// revoked tokens are indistinguishable (one 404), so tokens cannot be probed.
// A person who is already a member keeps their role and the invite stays valid.
func (s *Service) AcceptInvite(ctx context.Context, u User, token string) (Org, error) {
	token = strings.TrimSpace(token)
	gone := newErr(404, "invite_invalid", "This invitation is invalid, expired or already used.")
	if !strings.HasPrefix(token, inviteTokenPrefix) || len(token) > 200 {
		return Org{}, gone
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Org{}, err
	}
	defer tx.Rollback(ctx)

	var id, orgID uuid.UUID
	var role Role
	err = tx.QueryRow(ctx, `SELECT id, org_id, role::text FROM org_invites
		WHERE token_hash=$1 AND used_at IS NULL AND expires_at > $2 FOR UPDATE`, hashInvite(token), s.Now()).Scan(&id, &orgID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return Org{}, gone
	}
	if err != nil {
		return Org{}, err
	}
	var already bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships WHERE org_id=$1 AND user_id=$2)`, orgID, u.ID).Scan(&already); err != nil {
		return Org{}, err
	}
	if already {
		return Org{}, newErr(409, "already_member", "You are already a member of this organization.")
	}
	if _, err := tx.Exec(ctx, `UPDATE org_invites SET used_at=$2, used_by=$3 WHERE id=$1`, id, s.Now(), u.ID); err != nil {
		return Org{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_memberships(org_id,user_id,role) VALUES($1,$2,$3::org_role)`, orgID, u.ID, role); err != nil {
		return Org{}, err
	}
	if err := audit(ctx, tx, &orgID, u.ID, "invite.accept", id.String(), map[string]any{"role": role}); err != nil {
		return Org{}, err
	}
	o := Org{ID: orgID, Role: role}
	if err := tx.QueryRow(ctx, `SELECT slug, name, country, timezone FROM organizations WHERE id=$1`, orgID).
		Scan(&o.Slug, &o.Name, &o.Country, &o.Timezone); err != nil {
		return Org{}, err
	}
	return o, tx.Commit(ctx)
}
