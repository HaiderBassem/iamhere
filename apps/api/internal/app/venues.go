package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	rows, err := s.pool.Query(ctx, `SELECT id, name, ST_Y(center::geometry), ST_X(center::geometry), radius_m, created_at
		FROM venues WHERE org_id=$1 ORDER BY name, id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Venue{}
	for rows.Next() {
		var v Venue
		if err := rows.Scan(&v.ID, &v.Name, &v.Lat, &v.Lon, &v.RadiusM, &v.CreatedAt); err != nil {
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
