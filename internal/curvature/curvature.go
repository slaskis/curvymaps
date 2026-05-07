// Package curvature scores polyline curvature using Adam Franco's
// radius-bucket weighting (https://github.com/adamfranco/curvature).
//
// Bucket weights, verified from add_segment_curvature.py:
//
//	r ≥ 175m       → 0.0 (straight)
//	100m ≤ r < 175 → 1.0
//	60m  ≤ r < 100 → 1.3
//	30m  ≤ r < 60  → 1.6
//	r < 30m        → 2.0
//
// Per-way score: Σ (segment_length_m × weight). Units: weighted meters.
package curvature

import (
	"math"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/geo"
)

type Scores struct {
	LengthM               float64
	Sinuosity             float64 // L / chord; 1.0 = straight
	HeadingChangeDegPerKm float64
	Curvature             float64 // weighted meters, Franco-style
}

// LengthM returns the haversine length of the polyline in meters.
func LengthM(line orb.LineString) float64 {
	if len(line) < 2 {
		return 0
	}
	var d float64
	for i := 1; i < len(line); i++ {
		d += geo.Distance(line[i-1], line[i])
	}
	return d
}

// Sinuosity returns length / chord-length. Straight road ≈ 1.0; switchback > 1.5.
// Returns 1.0 (rather than NaN) for zero-chord (closed loop) for callers' sanity.
func Sinuosity(line orb.LineString) float64 {
	if len(line) < 2 {
		return 1
	}
	chord := geo.Distance(line[0], line[len(line)-1])
	if chord < 1e-9 {
		return 1
	}
	return LengthM(line) / chord
}

// HeadingChangeDegPerKm sums the absolute bearing changes between consecutive
// segments, normalized per kilometer of polyline.
func HeadingChangeDegPerKm(line orb.LineString) float64 {
	if len(line) < 3 {
		return 0
	}
	var sum float64
	for i := 0; i+2 < len(line); i++ {
		b1 := geo.Bearing(line[i], line[i+1])
		b2 := geo.Bearing(line[i+1], line[i+2])
		sum += math.Abs(angleDiffDeg(b1, b2))
	}
	km := LengthM(line) / 1000
	if km < 1e-9 {
		return 0
	}
	return sum / km
}

// Curvature returns Franco's radius-bucket weighted score.
func Curvature(line orb.LineString) float64 {
	return All(line).Curvature
}

// All computes every score in a single pass. Cheaper than calling each one.
func All(line orb.LineString) Scores {
	var s Scores
	if len(line) < 2 {
		return s
	}

	// Length and segment-length cache.
	segLen := make([]float64, len(line)-1)
	for i := 1; i < len(line); i++ {
		segLen[i-1] = geo.Distance(line[i-1], line[i])
		s.LengthM += segLen[i-1]
	}

	// Sinuosity.
	chord := geo.Distance(line[0], line[len(line)-1])
	if chord >= 1e-9 {
		s.Sinuosity = s.LengthM / chord
	} else {
		s.Sinuosity = 1
	}

	if len(line) < 3 {
		return s
	}

	// Triplet pass: heading change + Franco curvature.
	var headSum float64
	for i := 0; i+2 < len(line); i++ {
		a, b, c := line[i], line[i+1], line[i+2]
		// Heading change for this triplet.
		b1 := geo.Bearing(a, b)
		b2 := geo.Bearing(b, c)
		headSum += math.Abs(angleDiffDeg(b1, b2))

		// Circumradius → bucket weight × length of the segment "owned" by
		// this triplet. We attribute segLen[i+1] (the b→c edge) to this
		// triplet, plus segLen[0] gets attributed to the first triplet so
		// every segment is counted exactly once.
		r := circumradiusM(a, b, c)
		w := bucketWeight(r)
		if i == 0 {
			s.Curvature += w * segLen[0]
		}
		s.Curvature += w * segLen[i+1]
	}

	if km := s.LengthM / 1000; km >= 1e-9 {
		s.HeadingChangeDegPerKm = headSum / km
	}
	return s
}

// bucketWeight returns Franco's per-radius weight in meters.
func bucketWeight(r float64) float64 {
	switch {
	case r < 30:
		return 2.0
	case r < 60:
		return 1.6
	case r < 100:
		return 1.3
	case r < 175:
		return 1.0
	default:
		return 0.0
	}
}

// circumradiusM returns the radius of the circle through three lat/lon points,
// in meters. Uses a local equirectangular projection anchored on `b`. Triplet
// segments are <100m in OSM data, so projection error is sub-millimeter.
// Returns +Inf for collinear or duplicate points (treated as straight).
func circumradiusM(a, b, c orb.Point) float64 {
	const earthR = 6378137.0
	lat0 := b.Lat() * math.Pi / 180
	cosLat := math.Cos(lat0)
	toXY := func(p orb.Point) (float64, float64) {
		dLon := (p.Lon() - b.Lon()) * math.Pi / 180
		dLat := (p.Lat() - b.Lat()) * math.Pi / 180
		return dLon * earthR * cosLat, dLat * earthR
	}
	ax, ay := toXY(a)
	bx, by := toXY(b)
	cx, cy := toXY(c)

	abx, aby := bx-ax, by-ay
	bcx, bcy := cx-bx, cy-by
	cax, cay := ax-cx, ay-cy

	ab := math.Hypot(abx, aby)
	bc := math.Hypot(bcx, bcy)
	ca := math.Hypot(cax, cay)

	// 2 * signed triangle area = |x_AB * y_BC - y_AB * x_BC|
	twoArea := math.Abs(abx*bcy - aby*bcx)
	if twoArea < 1e-9 {
		return math.Inf(1)
	}
	return (ab * bc * ca) / (2 * twoArea)
}

// angleDiffDeg returns the signed shortest-arc difference b - a in degrees,
// in the range (-180, 180].
func angleDiffDeg(a, b float64) float64 {
	d := math.Mod(b-a+540, 360) - 180
	return d
}
