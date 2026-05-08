package ingest

import "github.com/paulmach/osm"

// keptHighways are the OSM road classes we score. Motorways and trunks are
// excluded — they're optimized for speed, not curve enjoyment, and tend to
// flood the map with not-very-curvy long segments.
var keptHighways = map[string]bool{
	"primary":      true,
	"secondary":    true,
	"tertiary":     true,
	"unclassified": true,
	"residential":  true,
}

// unpavedSurfaces are dropped unless --unpaved is set.
var unpavedSurfaces = map[string]bool{
	"gravel":      true,
	"dirt":        true,
	"ground":      true,
	"unpaved":     true,
	"earth":       true,
	"sand":        true,
	"mud":         true,
	"compacted":   true,
	"fine_gravel": true,
}

// IsUnpaved reports whether the OSM `surface` tag value classifies a way as
// unpaved. An empty string (no surface tag) returns false — OSM convention is
// that a missing surface tag on a `primary`/`secondary`/etc. way is paved.
func IsUnpaved(surface string) bool {
	return unpavedSurfaces[surface]
}

// FilterOpts controls way acceptance.
type FilterOpts struct {
	IncludeUnpaved bool
}

// Accept returns true if the way should be ingested.
func Accept(tags osm.Tags, opts FilterOpts) bool {
	hw := tags.Find("highway")
	if !keptHighways[hw] {
		return false
	}
	if tags.Find("area") == "yes" {
		return false
	}
	switch tags.Find("access") {
	case "no", "private":
		return false
	}
	if !opts.IncludeUnpaved {
		if IsUnpaved(tags.Find("surface")) {
			return false
		}
	}
	return true
}
