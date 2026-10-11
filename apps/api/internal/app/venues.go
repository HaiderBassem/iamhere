package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iamhere/api/internal/vision"
)

// maxVenuesPerOrg is a blunt abuse limit, like maxOwnedOrgs.
const maxVenuesPerOrg = 200

type Venue struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	RadiusM   int       `json:"radius_m"`
	CreatedAt time.Time `json:"created_at"`
	// ReferenceCount is how many reference photos the visual check can compare
	// against. Strict mode needs at least one to be able to verify anyone.
	ReferenceCount int `json:"reference_count"`
}

// CreateVenue registers a physical place. Sessions bound to it are checked
// against the venue's centre and radius instead of the organizer's device.
func (s *Service) CreateVenue(ctx context.Context, u User, orgID uuid.UUID, name string, lat, lon float64, radiusM int) (Venue, error) {
	if _, err := s.require(ctx, u, orgID, PermManageOrg); err != nil {
		return Venue{}, err
	}
	name, err := validText(name, 1, 200, "Name")
	if err != nil {
		return Venue{}, err
	}
	if !validCoord(lat, lon) {
		return Venue{}, invalid("Invalid location.")
	}
	if radiusM == 0 {
		radiusM = 30
	}
	if radiusM < 5 || radiusM > 1000 {
		return Venue{}, invalid("Radius must be between 5 and 1000 meters.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Venue{}, err
	}
	defer tx.Rollback(ctx)
	// serialize concurrent creates so the cap cannot be overshot
	if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id=$1 FOR UPDATE`, orgID); err != nil {
		return Venue{}, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM venues WHERE org_id=$1`, orgID).Scan(&n); err != nil {
		return Venue{}, err
	}
	if n >= maxVenuesPerOrg {
		return Venue{}, newErr(429, "venue_limit", "This organization has reached the venue limit.")
	}
	v := Venue{Name: name, Lat: lat, Lon: lon, RadiusM: radiusM}
	err = tx.QueryRow(ctx, `INSERT INTO venues(org_id, name, center, radius_m)
		VALUES($1,$2,ST_SetSRID(ST_MakePoint($4::float8,$3::float8),4326)::geography,$5) RETURNING id, created_at`,
		orgID, name, lat, lon, radiusM).Scan(&v.ID, &v.CreatedAt)
	if err != nil {
		return Venue{}, err
	}
	if err := audit(ctx, tx, &orgID, u.ID, "venue.create", v.ID.String(), nil); err != nil {
		return Venue{}, err
	}
	return v, tx.Commit(ctx)
}

// ListVenues is visible to everyone who can start a session (they pick from it).
func (s *Service) ListVenues(ctx context.Context, u User, orgID uuid.UUID) ([]Venue, error) {
	if _, err := s.require(ctx, u, orgID, PermViewAttendance); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT v.id, v.name, ST_Y(v.center::geometry), ST_X(v.center::geometry), v.radius_m, v.created_at,
		(SELECT count(*) FROM venue_fingerprints f WHERE f.venue_id=v.id AND f.org_id=v.org_id)
		FROM venues v WHERE v.org_id=$1 ORDER BY v.name, v.id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Venue{}
	for rows.Next() {
		var v Venue
		if err := rows.Scan(&v.ID, &v.Name, &v.Lat, &v.Lon, &v.RadiusM, &v.CreatedAt, &v.ReferenceCount); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteVenue removes a venue. Past sessions keep their own location snapshot.
func (s *Service) DeleteVenue(ctx context.Context, u User, orgID, venueID uuid.UUID) error {
	if _, err := s.require(ctx, u, orgID, PermManageOrg); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM venues WHERE id=$1 AND org_id=$2`, venueID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := audit(ctx, tx, &orgID, u.ID, "venue.delete", venueID.String(), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var errNoVenue = errors.New("no venue")

// resolveLocation decides where a new session is anchored.
// Precedence: explicit venue > organizer device position > the organization's
// only venue (title-only flow) > none (every check-in then needs review).
type sessionLocation struct {
	source   string
	venueID  *uuid.UUID
	lat, lon *float64
	accuracy *float64
	radiusM  *int // venue radius overrides the organization default
}

func (s *Service) resolveLocation(ctx context.Context, orgID uuid.UUID, in CreateSessionInput) (sessionLocation, error) {
	if in.VenueID != nil {
		loc, err := s.venueLocation(ctx, orgID, *in.VenueID)
		if errors.Is(err, errNoVenue) {
			return loc, invalid("That venue does not exist.")
		}
		loc.source = "venue"
		return loc, err
	}
	if a := in.Anchor; a != nil {
		return sessionLocation{source: "organizer", lat: &a.Lat, lon: &a.Lon, accuracy: &a.AccuracyM}, nil
	}
	var ids []uuid.UUID
	rows, err := s.pool.Query(ctx, `SELECT id FROM venues WHERE org_id=$1 LIMIT 2`, orgID)
	if err != nil {
		return sessionLocation{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return sessionLocation{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return sessionLocation{}, err
	}
	rows.Close()
	if len(ids) == 1 {
		loc, err := s.venueLocation(ctx, orgID, ids[0])
		if errors.Is(err, errNoVenue) { // deleted in between: fall back to none
			return sessionLocation{source: "none"}, nil
		}
		loc.source = "sole_venue"
		return loc, err
	}
	return sessionLocation{source: "none"}, nil
}

func (s *Service) venueLocation(ctx context.Context, orgID, id uuid.UUID) (sessionLocation, error) {
	var lat, lon float64
	var radius int
	err := s.pool.QueryRow(ctx, `SELECT ST_Y(center::geometry), ST_X(center::geometry), radius_m FROM venues WHERE id=$1 AND org_id=$2`,
		id, orgID).Scan(&lat, &lon, &radius)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionLocation{}, errNoVenue
	}
	zero := 0.0
	return sessionLocation{venueID: &id, lat: &lat, lon: &lon, accuracy: &zero, radiusM: &radius}, err
}

// ---- reference photos for the visual check ------------------------------------

// maxReferencesPerVenue lets an organization cover a place from several angles.
const maxReferencesPerVenue = 8

type VenueReference struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// photoError maps a vision error to a client-safe API error.
func photoError(err error) error {
	switch {
	case errors.Is(err, vision.ErrUnusable):
		return newErr(400, "photo_unusable", "The photo is too dark, covered or blank. Point the camera at the room and try again.")
	case errors.Is(err, vision.ErrTooLarge):
		return newErr(400, "photo_too_large", "That photo is too large.")
	default:
		return newErr(400, "photo_invalid", "That file is not a usable photo.")
	}
}

// AddVenueReference fingerprints a photo of the venue and stores only the
// fingerprint. Only people who manage the organization can enrol references,
// because they decide what "this place" looks like.
func (s *Service) AddVenueReference(ctx context.Context, u User, orgID, venueID uuid.UUID, photo []byte) (VenueReference, error) {
	if _, err := s.require(ctx, u, orgID, PermManageOrg); err != nil {
		return VenueReference{}, err
	}
	fp, err := vision.Fingerprint(photo)
	if err != nil {
		return VenueReference{}, photoError(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VenueReference{}, err
	}
	defer tx.Rollback(ctx)
	// lock the venue row so the per-venue cap cannot be overshot concurrently
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM venues WHERE id=$1 AND org_id=$2 FOR UPDATE`, venueID, orgID).Scan(&one); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VenueReference{}, ErrNotFound
		}
		return VenueReference{}, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM venue_fingerprints WHERE venue_id=$1 AND org_id=$2`, venueID, orgID).Scan(&n); err != nil {
		return VenueReference{}, err
	}
	if n >= maxReferencesPerVenue {
		return VenueReference{}, newErr(409, "reference_limit", "This place already has the maximum number of reference photos. Remove one first.")
	}
	var ref VenueReference
	if err := tx.QueryRow(ctx, `INSERT INTO venue_fingerprints(org_id, venue_id, fingerprint, created_by)
		VALUES($1,$2,$3,$4) RETURNING id, created_at`, orgID, venueID, fp, u.ID).Scan(&ref.ID, &ref.CreatedAt); err != nil {
		return VenueReference{}, err
	}
	if err := audit(ctx, tx, &orgID, u.ID, "venue.reference.add", ref.ID.String(), map[string]any{"venue": venueID}); err != nil {
		return VenueReference{}, err
	}
	return ref, tx.Commit(ctx)
}

func (s *Service) ListVenueReferences(ctx context.Context, u User, orgID, venueID uuid.UUID) ([]VenueReference, error) {
	if _, err := s.require(ctx, u, orgID, PermManageOrg); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, created_at FROM venue_fingerprints
		WHERE venue_id=$1 AND org_id=$2 ORDER BY created_at, id`, venueID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VenueReference{}
	for rows.Next() {
		var r VenueReference
		if err := rows.Scan(&r.ID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) DeleteVenueReference(ctx context.Context, u User, orgID, venueID, refID uuid.UUID) error {
	if _, err := s.require(ctx, u, orgID, PermManageOrg); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM venue_fingerprints WHERE id=$1 AND venue_id=$2 AND org_id=$3`, refID, venueID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := audit(ctx, tx, &orgID, u.ID, "venue.reference.delete", refID.String(), map[string]any{"venue": venueID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// venueFingerprints loads the reference fingerprints used by the visual check.
func (s *Service) venueFingerprints(ctx context.Context, orgID, venueID uuid.UUID) ([][]byte, error) {
	rows, err := s.pool.Query(ctx, `SELECT fingerprint FROM venue_fingerprints WHERE venue_id=$1 AND org_id=$2`, venueID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var fp []byte
		if err := rows.Scan(&fp); err != nil {
			return nil, err
		}
		out = append(out, fp)
	}
	return out, rows.Err()
}
