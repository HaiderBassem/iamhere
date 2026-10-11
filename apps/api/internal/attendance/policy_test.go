package attendance

import "testing"

func TestDecide(t *testing.T) {
	strict := Policy{Mode: ModeStrict, RadiusM: 20, MaxAccuracyM: 100, MaxFixAgeS: 30, HasAnchor: true, VisualAvailable: true}
	legacy := strict
	legacy.Mode = ModeVisual
	noPhotos := strict
	noPhotos.VisualAvailable = false
	noAnchor := strict
	noAnchor.HasAnchor = false
	normal := Policy{Mode: ModeStandard, RadiusM: 20, MaxAccuracyM: 100, MaxFixAgeS: 60}

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
		// normal mode: QR only, whatever the location evidence says (or lacks)
		{"normal: no gps", normal, GPSReading{}, false, StatusVerified, ReasonQROnly},
		{"normal: far away gps ignored", normal, g(5000, 5, 1), false, StatusVerified, ReasonQROnly},
		{"normal: no anchor", normal, GPSReading{}, false, StatusVerified, ReasonQROnly},

		// strict: GPS + photo
		{"inside, photo matched", strict, g(8, 10, 2), true, StatusVerified, ReasonGPSWithinRadius},
		{"confidently outside", strict, g(200, 10, 2), true, StatusRejected, ReasonOutsideRadius},
		// 80m accuracy at 30m distance: could be inside; must not reject a genuine attendee
		{"poor accuracy not rejected", strict, g(30, 80, 2), true, StatusNeedsReview, ReasonUncertainEdge},
		{"accuracy beyond ceiling", strict, g(5, 500, 2), true, StatusNeedsReview, ReasonPoorAccuracy},
		{"just outside but uncertain", strict, g(25, 10, 2), true, StatusNeedsReview, ReasonUncertainEdge},
		{"no gps", strict, GPSReading{}, true, StatusNeedsReview, ReasonNoGPS},
		{"stale fix", strict, g(5, 5, 600), true, StatusNeedsReview, ReasonStaleFix},
		{"no anchor", noAnchor, g(5, 5, 1), true, StatusNeedsReview, ReasonNoAnchor},
		{"photo missing", strict, g(5, 5, 1), false, StatusNeedsReview, ReasonVisualRequired},
		{"photo does not match", strict, g(5, 5, 1), false, StatusNeedsReview, ReasonVisualRequired},
		{"no reference photos for the place", noPhotos, g(5, 5, 1), false, StatusNeedsReview, ReasonVisualNotSetUp},
		{"outside beats photo match", strict, g(300, 5, 1), true, StatusRejected, ReasonOutsideRadius},
		{"legacy visual mode acts like strict", legacy, g(5, 5, 1), true, StatusVerified, ReasonGPSWithinRadius},
		{"legacy visual mode needs photo", legacy, g(5, 5, 1), false, StatusNeedsReview, ReasonVisualRequired},
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

func TestModeChecked(t *testing.T) {
	if ModeStandard.Checked() || !ModeStrict.Checked() || !ModeVisual.Checked() {
		t.Fatal("only strict and legacy visual check location/photo")
	}
}
