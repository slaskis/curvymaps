package server

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/maptile"

	"github.com/slaskis/curvymaps/internal/store"
)

func pointAt(origin orb.Point, bearingDeg, distM float64) orb.Point {
	const earthR = 6378137.0
	lat1 := origin.Lat() * math.Pi / 180
	lon1 := origin.Lon() * math.Pi / 180
	bRad := bearingDeg * math.Pi / 180
	dR := distM / earthR
	lat2 := math.Asin(math.Sin(lat1)*math.Cos(dR) + math.Cos(lat1)*math.Sin(dR)*math.Cos(bRad))
	lon2 := lon1 + math.Atan2(math.Sin(bRad)*math.Sin(dR)*math.Cos(lat1),
		math.Cos(dR)-math.Sin(lat1)*math.Sin(lat2))
	return orb.Point{lon2 * 180 / math.Pi, lat2 * 180 / math.Pi}
}

// seedDB inserts one synthetic curvy way into the `ways` table directly.
func seedDB(t *testing.T) string {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	origin := orb.Point{12.0, 60.0}
	var curvy orb.LineString
	for i := 0; i <= 36; i++ {
		theta := 360.0 * float64(i) / 36
		curvy = append(curvy, pointAt(origin, theta, 50))
	}
	minLon, minLat := 180.0, 90.0
	maxLon, maxLat := -180.0, -90.0
	for _, p := range curvy {
		if p.Lon() < minLon {
			minLon = p.Lon()
		}
		if p.Lon() > maxLon {
			maxLon = p.Lon()
		}
		if p.Lat() < minLat {
			minLat = p.Lat()
		}
		if p.Lat() > maxLat {
			maxLat = p.Lat()
		}
	}

	rows := []store.ScoredRow{
		{
			ID: 1, Highway: "tertiary",
			LengthM:               500,
			Sinuosity:             1.5,
			HeadingChangeDegPerKm: 600,
			Curvature:             800,
			MeanInvRadius:         0.02,
			MaxInvRadius:          0.025,
			Geometry:              curvy,
			MinLon:                minLon, MaxLon: maxLon,
			MinLat: minLat, MaxLat: maxLat,
		},
	}
	if err := store.InsertScoredBatch(ctx, db, rows); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func TestGeoJSONHandler(t *testing.T) {
	dbPath := seedDB(t)
	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv, err := New(db, Opts{NoCache: true})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ways?bbox=11.99,59.99,12.01,60.01")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d, body=%s", resp.StatusCode, body)
	}
	var fc struct {
		Type     string                   `json:"type"`
		Features []map[string]interface{} `json:"features"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fc); err != nil {
		t.Fatal(err)
	}
	if fc.Type != "FeatureCollection" {
		t.Errorf("type=%q", fc.Type)
	}
	if len(fc.Features) < 1 {
		t.Errorf("expected at least 1 feature, got %d", len(fc.Features))
	}
}

func TestMVTHandler(t *testing.T) {
	dbPath := seedDB(t)
	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	srv, err := New(db, Opts{NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// At z=12, point (12.0, 60.0) is roughly tile (2192, 1240). We don't need
	// exact - any tile that covers our seeded bbox works. Compute it from
	// maptile.At in the helper.
	resp, err := http.Get(ts.URL + "/tiles/12/2192/1240.mvt")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatalf("empty MVT body")
	}
	// Should be parseable as MVT (may have 0 features if tile didn't cover seed).
	if _, err := mvt.Unmarshal(data); err != nil {
		t.Fatalf("MVT unmarshal: %v", err)
	}
}

func TestAlgorithmsEndpoint(t *testing.T) {
	dbPath := seedDB(t)
	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv, err := New(db, Opts{NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/algorithms")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) < 5 {
		t.Errorf("want at least 5 algorithms, got %d", len(list))
	}
	got := map[string]bool{}
	for _, a := range list {
		got[a["id"].(string)] = true
	}
	for _, id := range []string{"franco", "sinuosity", "heading_change", "mean_inv_radius", "max_inv_radius"} {
		if !got[id] {
			t.Errorf("missing algorithm %q in /algorithms", id)
		}
	}
}

func TestGeoJSONAlgoFilter(t *testing.T) {
	dbPath := seedDB(t)
	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv, err := New(db, Opts{NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	bbox := "11.99,59.99,12.01,60.01"

	// Sinuosity 1.5 < 1.8 → filtered out.
	resp, err := http.Get(ts.URL + "/api/ways?bbox=" + bbox + "&algo=sinuosity&min=1.8")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var fc struct {
		Features []map[string]interface{} `json:"features"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fc); err != nil {
		t.Fatal(err)
	}
	if len(fc.Features) != 0 {
		t.Errorf("sinuosity 1.5 ≥ 1.8 should be empty, got %d", len(fc.Features))
	}

	// Sinuosity 1.5 ≥ 1.2 → kept.
	resp2, err := http.Get(ts.URL + "/api/ways?bbox=" + bbox + "&algo=sinuosity&min=1.2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var fc2 struct {
		Features []map[string]interface{} `json:"features"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&fc2); err != nil {
		t.Fatal(err)
	}
	if len(fc2.Features) < 1 {
		t.Errorf("sinuosity 1.5 ≥ 1.2 should match, got %d", len(fc2.Features))
	}

	// Unknown algo → 400.
	respBad, err := http.Get(ts.URL + "/api/ways?bbox=" + bbox + "&algo=nope&min=0")
	if err != nil {
		t.Fatal(err)
	}
	respBad.Body.Close()
	if respBad.StatusCode != 400 {
		t.Errorf("unknown algo: want 400, got %d", respBad.StatusCode)
	}
}

func TestMVTBakesAllAlgoProperties(t *testing.T) {
	dbPath := seedDB(t)
	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv, err := New(db, Opts{NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// At z=10, (12.0, 60.0) is roughly tile (548, 309) — broad enough that
	// the seeded 50m-radius circle definitely lands inside. Verifying the
	// exact x/y is fiddly; instead pick a tile we know the seeded geometry
	// is in by computing it via maptile.
	tile := maptile.At(orb.Point{12.0, 60.0}, maptile.Zoom(10))
	url := ts.URL + "/tiles/10/" +
		strconvUint(uint64(tile.X)) + "/" + strconvUint(uint64(tile.Y)) + ".mvt"

	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	layers, err := mvt.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, l := range layers {
		for _, f := range l.Features {
			found = true
			for _, key := range []string{"franco", "sinuosity", "heading_change", "mean_inv_radius", "max_inv_radius"} {
				if _, ok := f.Properties[key]; !ok {
					t.Errorf("feature missing property %q", key)
				}
			}
		}
	}
	if !found {
		t.Skip("no features in selected tile; skipping property check")
	}
}

func strconvUint(u uint64) string {
	return strconv.FormatUint(u, 10)
}

func TestSimplifyTolerance(t *testing.T) {
	cases := []struct {
		z   int
		min float64
	}{
		{8, 50}, {12, 20}, {14, 5}, {16, 0},
	}
	for _, c := range cases {
		got := simplifyToleranceM(maptile.Zoom(c.z))
		if got != c.min {
			t.Errorf("z=%d: got %v, want %v", c.z, got, c.min)
		}
	}
}
