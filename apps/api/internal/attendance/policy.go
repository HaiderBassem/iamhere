// Package attendance holds check-in decision logic and (separately) persistence.
//
// policy.go is pure: no I/O, so every decision is table-testable.
package attendance

import "math"

type Mode string

const (
	ModeStandard Mode = "standard"
	ModeVisual   Mode = "visual"
	ModeStrict   Mode = "strict"
)

type Status string

const (
	StatusVerified    Status = "verified"
	StatusNeedsReview Status = "needs_review"
	StatusRejected    Status = "rejected"
)

// GPSReading is what the browser Geolocation API reported. All fields are
// client-supplied and therefore untrusted evidence, never proof.
type GPSReading struct {
	Present   bool
	DistanceM float64 // server-computed (PostGIS) distance to the session anchor
	AccuracyM float64 // client-reported uncertainty radius
	AgeS      float64 // server-computed age of the fix at receipt
}

type Policy struct {
	Mode            Mode
	RadiusM         float64
	MaxAccuracyM    float64
	MaxFixAgeS      float64
	HasAnchor       bool
	VisualAvailable bool // venue fingerprints exist for this org
}

type Decision struct {
	Status  Status
	Reasons []string
}

// Reason codes are stable API strings.
const (
	ReasonNoAnchor        = "no_session_anchor"
	ReasonNoGPS           = "gps_unavailable"
	ReasonStaleFix        = "gps_stale"
	ReasonPoorAccuracy    = "gps_accuracy_too_low"
	ReasonOutsideRadius   = "outside_radius"
	ReasonUncertainEdge   = "within_uncertainty_of_radius"
	ReasonVisualRequired  = "visual_check_required"
	ReasonVisualNotSetUp  = "visual_not_configured"
	ReasonGPSWithinRadius = "gps_within_radius"
)

// Decide combines GPS evidence under the org policy. Principles:
//   - A genuine attendee is never hard-rejected only because the device is
//     imprecise: poor accuracy yields needs_review, not rejected.
//   - Rejection is reserved for a fix that is confidently outside the radius:
//     distance minus the reported uncertainty still exceeds the radius.
//   - Verified means "evidence is consistent with presence", not "was present".
func Decide(p Policy, g GPSReading, visualMatched bool) Decision {
	gps := gpsReason(p, g)
	if gps == ReasonOutsideRadius {
		return Decision{StatusRejected, []string{gps}}
	}
	reasons := []string{gps}

	if (p.Mode == ModeVisual || p.Mode == ModeStrict) && !visualMatched {
		if p.VisualAvailable {
			reasons = append(reasons, ReasonVisualRequired)
		} else {
			reasons = append(reasons, ReasonVisualNotSetUp)
		}
	}
	if len(reasons) == 1 && gps == ReasonGPSWithinRadius {
		return Decision{StatusVerified, reasons}
	}
	return Decision{StatusNeedsReview, reasons}
}

func gpsReason(p Policy, g GPSReading) string {
	switch {
	case !p.HasAnchor:
		// Organizer supplied only a title and no position: the session location
		// is unknown. Say so instead of inventing certainty.
		return ReasonNoAnchor
	case !g.Present, math.IsNaN(g.DistanceM), math.IsNaN(g.AccuracyM), g.AccuracyM < 0:
		return ReasonNoGPS
	case g.AgeS > p.MaxFixAgeS:
		return ReasonStaleFix
	case g.DistanceM-g.AccuracyM > p.RadiusM:
		return ReasonOutsideRadius
	case g.AccuracyM > p.MaxAccuracyM:
		return ReasonPoorAccuracy
	case g.DistanceM > p.RadiusM:
		return ReasonUncertainEdge
	}
	return ReasonGPSWithinRadius
}
