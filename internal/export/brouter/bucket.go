// Package brouter exports curvymaps scores into an OSM file that BRouter's
// preprocessor can ingest. Each scored way gets one synthetic tag per
// algorithm — curvymaps:<algo_id>=b0..b4 — bucketed using the algorithm's
// own DefaultStops, so the BRouter view stays in lockstep with the map's
// 5-color paint ramp.
package brouter

import (
	"fmt"
	"strings"

	"github.com/slaskis/curvymaps/internal/curvature"
)

// BucketCount is the number of bands an algorithm's DefaultStops divide
// scores into (4 thresholds → 5 bands). Reuses the frontend's paint ramp.
const BucketCount = 5

// TagPrefix is the namespace for every synthetic tag the export writes.
const TagPrefix = "curvymaps:"

// Bucket returns the band index 0..BucketCount-1 for a value, given the
// algorithm's 4 DefaultStops. value < stops[0] → 0; value ≥ stops[3] → 4.
// Stops are assumed monotonically increasing (registry guarantees this).
func Bucket(stops []float64, value float64) int {
	for i, t := range stops {
		if value < t {
			return i
		}
	}
	return len(stops)
}

// BucketLabel returns the tag value for a bucket index ("b0".."b4"). Stable
// strings — they end up in user-visible BRouter profiles, do not rename.
func BucketLabel(b int) string {
	return fmt.Sprintf("b%d", b)
}

// AllBucketLabels returns every legal bucket value, in order.
func AllBucketLabels() []string {
	out := make([]string, BucketCount)
	for i := range out {
		out[i] = BucketLabel(i)
	}
	return out
}

// TagKey returns the synthetic tag key for an algorithm (e.g. "curvymaps:franco").
func TagKey(a curvature.Algorithm) string {
	return TagPrefix + a.ID
}

// BucketFor runs the algorithm's getter and returns its bucket label.
func BucketFor(a curvature.Algorithm, s curvature.Scores) string {
	return BucketLabel(Bucket(a.DefaultStops, a.Get(s)))
}

// AlgoIDList returns a comma-joined list of algorithm IDs, for log/help text.
func AlgoIDList(algos []curvature.Algorithm) string {
	ids := make([]string, len(algos))
	for i, a := range algos {
		ids[i] = a.ID
	}
	return strings.Join(ids, ",")
}
