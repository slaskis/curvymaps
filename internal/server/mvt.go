package server

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geo"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/simplify"

	"github.com/slaskis/curvymaps/internal/store"
)

// simplifyToleranceM picks a meter tolerance for the Radial simplifier based
// on zoom. Lower zoom = coarser tile = more aggressive smoothing.
func simplifyToleranceM(z maptile.Zoom) float64 {
	switch {
	case z < 10:
		return 50
	case z < 14:
		return 20
	case z == 14:
		return 5
	default:
		return 0 // no simplification at z>=15
	}
}

func (s *Server) handleTile(w http.ResponseWriter, r *http.Request) {
	z, err := strconv.Atoi(chi.URLParam(r, "z"))
	if err != nil || z < 0 || z > 22 {
		http.Error(w, "bad z", http.StatusBadRequest)
		return
	}
	x, err := strconv.ParseUint(chi.URLParam(r, "x"), 10, 32)
	if err != nil {
		http.Error(w, "bad x", http.StatusBadRequest)
		return
	}
	y, err := strconv.ParseUint(chi.URLParam(r, "y"), 10, 32)
	if err != nil {
		http.Error(w, "bad y", http.StatusBadRequest)
		return
	}
	tile := maptile.New(uint32(x), uint32(y), maptile.Zoom(z))

	// Cache key prefix bumped (v2) when the tile schema changed to bake all
	// algorithm score properties; old single-property tiles must not be served.
	cacheKey := fmt.Sprintf("v2/%d/%d/%d", z, x, y)
	if cached, ok := s.cache.get(cacheKey); ok {
		writeMVT(w, cached)
		return
	}

	bound := tile.Bound()
	ways, err := store.QueryByBBox(r.Context(), s.db,
		bound.Min.Lon(), bound.Min.Lat(), bound.Max.Lon(), bound.Max.Lat(), "", 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fc := geojson.NewFeatureCollection()
	if tol := simplifyToleranceM(tile.Z); tol > 0 {
		simp := simplify.Radial(geo.DistanceHaversine, tol)
		for _, way := range ways {
			g := simp.Simplify(way.Geometry.Clone()).(orb.LineString)
			if len(g) < 2 {
				continue
			}
			fc.Append(wayToFeature(way, g))
		}
	} else {
		for _, way := range ways {
			fc.Append(wayToFeature(way, way.Geometry))
		}
	}

	layers := mvt.NewLayers(map[string]*geojson.FeatureCollection{"ways": fc})
	layers.ProjectToTile(tile)
	layers.Clip(mvt.MapboxGLDefaultExtentBound)
	layers.RemoveEmpty(1.0, 0)
	data, err := mvt.Marshal(layers)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.cache.put(cacheKey, data)
	writeMVT(w, data)
}

func wayToFeature(w store.Way, geom orb.LineString) *geojson.Feature {
	f := geojson.NewFeature(geom)
	f.ID = w.ID
	// Bake every algorithm's score so the frontend can switch metrics
	// without a tile refetch. Property names match curvature.Algorithm.ID.
	f.Properties["franco"] = w.Curvature
	f.Properties["sinuosity"] = w.Sinuosity
	f.Properties["heading_change"] = w.HeadingChangeDegPerKm
	f.Properties["mean_inv_radius"] = w.MeanInvRadius
	f.Properties["max_inv_radius"] = w.MaxInvRadius
	f.Properties["highway"] = w.Highway
	f.Properties["length_m"] = w.LengthM
	if w.Name.Valid {
		f.Properties["name"] = w.Name.String
	}
	return f
}

func writeMVT(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(data)
}
