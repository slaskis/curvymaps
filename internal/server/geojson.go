package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/paulmach/orb/geojson"

	"github.com/slaskis/curvymaps/internal/store"
)

func (s *Server) handleGeoJSON(w http.ResponseWriter, r *http.Request) {
	bbStr := r.URL.Query().Get("bbox")
	if bbStr == "" {
		http.Error(w, "missing bbox", http.StatusBadRequest)
		return
	}
	bb, err := parseBBoxQS(bbStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	minCurv := 0.0
	if v := r.URL.Query().Get("min_curvature"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			http.Error(w, "bad min_curvature", http.StatusBadRequest)
			return
		}
		minCurv = f
	}

	ways, err := store.QueryByBBox(r.Context(), s.db, bb[0], bb[1], bb[2], bb[3], minCurv)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fc := geojson.NewFeatureCollection()
	for _, way := range ways {
		fc.Append(wayToFeature(way, way.Geometry))
	}

	w.Header().Set("Content-Type", "application/geo+json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	if err := enc.Encode(fc); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func parseBBoxQS(s string) ([4]float64, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return [4]float64{}, fmt.Errorf("bbox needs 4 comma-separated values")
	}
	var out [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return out, fmt.Errorf("bbox part %d: %w", i, err)
		}
		out[i] = f
	}
	if out[0] >= out[2] || out[1] >= out[3] {
		return out, fmt.Errorf("bbox order: lon1<lon2, lat1<lat2")
	}
	return out, nil
}
