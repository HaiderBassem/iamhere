package attendance

import "testing"

func TestDecide(t *testing.T) {
	std := Policy{Mode: ModeStandard, RadiusM: 20, MaxAccuracyM: 100, MaxFixAgeS: 60, HasAnchor: true}
	vis := std
	vis.Mode = ModeVisual
	vis.VisualAvailable = true
	visNone := vis
	visNone.VisualAvailable = false
	noAnchor := std
	noAnchor.HasAnchor = false

	g := func(dist, acc, age float64) GPSReading {
		return GPSReading{Present: true, DistanceM: dist, AccuracyM: acc, AgeS: age}
	}

	cases := []struct {
		name   string
		p      Policy
		g      GPSReading
		visual bool
		want   Status
		reason string
	}{
		{"inside, good accuracy", std, g(8, 10, 2), false, StatusVerified, ReasonGPSWithinRadius},
		{"confidently outside", std, g(200, 10, 2), false, StatusRejected, ReasonOutsideRadius},
		// 80m accuracy at 30m distance: could be inside; must not reject a genuine attendee
		{"poor accuracy not rejected", std, g(30, 80, 2), false, StatusNeedsReview, ReasonUncertainEdge},
		{"accuracy beyond ceiling", std, g(5, 500, 2), false, StatusNeedsReview, ReasonPoorAccuracy},
		{"just outside but uncertain", std, g(25, 10, 2), false, StatusNeedsReview, ReasonUncertainEdge},
		{"no gps", std, GPSReading{}, false, StatusNeedsReview, ReasonNoGPS},
		{"stale fix", std, g(5, 5, 600), false, StatusNeedsReview, ReasonStaleFix},
		{"no anchor", noAnchor, g(5, 5, 1), false, StatusNeedsReview, ReasonNoAnchor},
		{"visual required, missing", vis, g(5, 5, 1), false, StatusNeedsReview, ReasonVisualRequired},
		{"visual required, matched", vis, g(5, 5, 1), true, StatusVerified, ReasonGPSWithinRadius},
		{"visual required, not configured", visNone, g(5, 5, 1), false, StatusNeedsReview, ReasonVisualNotSetUp},
		{"outside beats visual match", vis, g(300, 5, 1), true, StatusRejected, ReasonOutsideRadius},
	}
	for _, c := range cases {
		d := Decide(c.p, c.g, c.visual)
		if d.Status != c.want {
			t.Errorf("%s: status %s want %s (%v)", c.name, d.Status, c.want, d.Reasons)
		}
		found := false
		for _, r := range d.Reasons {
			found = found || r == c.reason
		}
		if !found {
			t.Errorf("%s: reasons %v missing %s", c.name, d.Reasons, c.reason)
		}
	}
}
