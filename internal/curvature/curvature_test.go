package curvature

import (
	"math"
	"testing"

	"github.com/paulmach/orb"
)

const (
	earthR = 6378137.0
)

// pointAtBearing returns a point at distance d (m) from origin on bearing b (deg).
// Used to synthesize geometries with known properties.
func pointAtBearing(origin orb.Point, bearingDeg, distM float64) orb.Point {
	lat1 := origin.Lat() * math.Pi / 180
	lon1 := origin.Lon() * math.Pi / 180
	bRad := bearingDeg * math.Pi / 180
	dR := distM / earthR
	lat2 := math.Asin(math.Sin(lat1)*math.Cos(dR) + math.Cos(lat1)*math.Sin(dR)*math.Cos(bRad))
	lon2 := lon1 + math.Atan2(math.Sin(bRad)*math.Sin(dR)*math.Cos(lat1),
		math.Cos(dR)-math.Sin(lat1)*math.Sin(lat2))
	return orb.Point{lon2 * 180 / math.Pi, lat2 * 180 / math.Pi}
}

// circle samples a planar circle of radius rM (m) around `center` at N points.
func circle(center orb.Point, rM float64, n int) orb.LineString {
	ls := make(orb.LineString, n)
	for i := 0; i < n; i++ {
		theta := 360.0 * float64(i) / float64(n)
		ls[i] = pointAtBearing(center, theta, rM)
	}
	return ls
}

func approx(t *testing.T, got, want, tol float64, name string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s: got %.4f, want %.4f ± %.4f", name, got, want, tol)
	}
}

func TestStraightLine(t *testing.T) {
	origin := orb.Point{12.0, 60.0}
	// Two-point line of 10km north.
	p1 := pointAtBearing(origin, 0, 10000)
	line := orb.LineString{origin, p1}

	s := All(line)
	approx(t, s.LengthM, 10000, 5, "length 2pt")
	approx(t, s.Sinuosity, 1.0, 1e-3, "sinuosity 2pt")
	if s.Curvature != 0 {
		t.Errorf("curvature 2pt: got %.3f, want 0", s.Curvature)
	}
	if s.HeadingChangeDegPerKm != 0 {
		t.Errorf("hcdpk 2pt: got %.3f, want 0", s.HeadingChangeDegPerKm)
	}

	// Multi-point straight line: same bearing every step.
	pts := orb.LineString{origin}
	for i := 1; i <= 10; i++ {
		pts = append(pts, pointAtBearing(origin, 0, float64(i)*1000))
	}
	s = All(pts)
	approx(t, s.LengthM, 10000, 5, "length 11pt")
	approx(t, s.Sinuosity, 1.0, 1e-3, "sinuosity 11pt")
	if s.Curvature > 1.0 { // small float noise allowed
		t.Errorf("curvature straight: got %.3f, want ~0", s.Curvature)
	}
	if s.HeadingChangeDegPerKm > 0.5 {
		t.Errorf("hcdpk straight: got %.3f, want ~0", s.HeadingChangeDegPerKm)
	}
}

func TestCircle50m(t *testing.T) {
	// 50m radius is in [30,60) bucket → weight 1.6.
	// 36-gon: 36 triplets, each with circumradius ≈ 50m.
	// Curvature ≈ length × 1.6 ≈ 2π·50 · 1.6 ≈ 502.65.
	center := orb.Point{12.0, 60.0}
	c := circle(center, 50, 36)
	c = append(c, c[0]) // close the loop so we get a triplet for the seam too
	s := All(c)

	wantLen := 2 * math.Pi * 50
	if math.Abs(s.LengthM-wantLen) > wantLen*0.02 {
		t.Errorf("circle length: got %.2f, want %.2f", s.LengthM, wantLen)
	}
	wantCurv := wantLen * 1.6
	if math.Abs(s.Curvature-wantCurv) > wantCurv*0.10 {
		t.Errorf("circle curvature: got %.2f, want %.2f ± 10%%", s.Curvature, wantCurv)
	}
}

func TestCircle200m(t *testing.T) {
	// 200m radius is ≥ 175 bucket → weight 0 → curvature 0.
	c := circle(orb.Point{12, 60}, 200, 36)
	c = append(c, c[0])
	s := All(c)
	if s.Curvature > 1.0 {
		t.Errorf("200m circle should score 0, got %.2f", s.Curvature)
	}
}

func TestTighterArcScoresHigher(t *testing.T) {
	// Two arcs spanning the same total angle. Tight one must score higher.
	origin := orb.Point{12, 60}
	tight := circle(origin, 25, 36)[:9]   // 90° arc, r=25 → bucket 2.0
	gentle := circle(origin, 200, 36)[:9] // 90° arc, r=200 → bucket 0
	if Curvature(tight) <= Curvature(gentle) {
		t.Errorf("tight arc (%.2f) must beat gentle arc (%.2f)",
			Curvature(tight), Curvature(gentle))
	}
}

func TestReverseInvariant(t *testing.T) {
	c := circle(orb.Point{12, 60}, 50, 36)
	rev := make(orb.LineString, len(c))
	for i, p := range c {
		rev[len(c)-1-i] = p
	}
	a := All(c)
	b := All(rev)
	approx(t, a.LengthM, b.LengthM, 1e-3, "length reversed")
	approx(t, a.Curvature, b.Curvature, 1e-3, "curvature reversed")
	approx(t, a.HeadingChangeDegPerKm, b.HeadingChangeDegPerKm, 1e-3, "hcdpk reversed")
}

func TestDegenerate(t *testing.T) {
	// Empty.
	if got := Curvature(orb.LineString{}); got != 0 {
		t.Errorf("empty: %v", got)
	}
	// Single point.
	if got := Curvature(orb.LineString{{0, 0}}); got != 0 {
		t.Errorf("1pt: %v", got)
	}
	// Two points.
	if got := Curvature(orb.LineString{{0, 0}, {1, 1}}); got != 0 {
		t.Errorf("2pt: %v", got)
	}
	// Three collinear points (must not divide-by-zero).
	origin := orb.Point{12, 60}
	collinear := orb.LineString{
		origin,
		pointAtBearing(origin, 90, 100),
		pointAtBearing(origin, 90, 200),
	}
	s := All(collinear)
	if s.Curvature > 1e-3 {
		t.Errorf("collinear curvature: %.4f", s.Curvature)
	}
	if math.IsNaN(s.Sinuosity) || math.IsInf(s.Sinuosity, 0) {
		t.Errorf("collinear sinuosity: %v", s.Sinuosity)
	}
}

func TestBucketBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		r      float64
		weight float64
	}{
		{"hairpin 20m", 20, 2.0},
		{"hairpin 29m", 29.99, 2.0},
		{"tight 30m", 30, 1.6},
		{"tight 59m", 59.99, 1.6},
		{"medium 60m", 60, 1.3},
		{"medium 99m", 99.99, 1.3},
		{"broad 100m", 100, 1.0},
		{"broad 174m", 174.99, 1.0},
		{"straight 175m", 175, 0},
		{"straight 1000m", 1000, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := bucketWeight(tc.r); got != tc.weight {
				t.Errorf("r=%.2f: got %v, want %v", tc.r, got, tc.weight)
			}
		})
	}
}
