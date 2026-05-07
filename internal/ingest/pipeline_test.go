package ingest

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/paulmach/orb"

	"github.com/slaskis/curvymaps/internal/store"
)

// pointAt is duplicated from curvature_test (no cross-package test imports).
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

func bbox(ls orb.LineString) (minLon, minLat, maxLon, maxLat float64) {
	minLon, minLat = 180, 90
	maxLon, maxLat = -180, -90
	for _, p := range ls {
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
	return
}

func TestPipelineEndToEnd(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	origin := orb.Point{12.0, 60.0}

	// Curvy way: a 50m-radius half-circle (in the 30-60m bucket → 1.6).
	var curvy orb.LineString
	for i := 0; i <= 36; i++ {
		theta := 360.0 * float64(i) / 36
		curvy = append(curvy, pointAt(origin, theta, 50))
	}
	curvyMinLon, curvyMinLat, curvyMaxLon, curvyMaxLat := bbox(curvy)

	// Straight way: 1km north-east line.
	straight := orb.LineString{origin}
	for i := 1; i <= 5; i++ {
		straight = append(straight, pointAt(origin, 45, float64(i)*200))
	}
	straightMinLon, straightMinLat, straightMaxLon, straightMaxLat := bbox(straight)

	staged := []store.StagedWay{
		{
			ID: 1, Highway: "tertiary",
			Geometry: curvy,
			MinLon:   curvyMinLon, MaxLon: curvyMaxLon,
			MinLat: curvyMinLat, MaxLat: curvyMaxLat,
		},
		{
			ID: 2, Highway: "tertiary",
			Geometry: straight,
			MinLon:   straightMinLon, MaxLon: straightMaxLon,
			MinLat: straightMinLat, MaxLat: straightMaxLat,
		},
	}
	if err := store.InsertStagingBatch(ctx, db, staged); err != nil {
		t.Fatal(err)
	}

	summary, err := Score(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Ways != 2 {
		t.Fatalf("ways: got %d, want 2", summary.Ways)
	}

	// Confirm staging dropped.
	var n int
	err = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='ways_staging'`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("ways_staging not dropped")
	}

	// Verify scores: curvy > straight.
	var curvyScore, straightScore float64
	if err := db.QueryRow(`SELECT curvature FROM ways WHERE id=1`).Scan(&curvyScore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT curvature FROM ways WHERE id=2`).Scan(&straightScore); err != nil {
		t.Fatal(err)
	}
	if !(curvyScore > straightScore) {
		t.Errorf("curvy %.2f should beat straight %.2f", curvyScore, straightScore)
	}
	if straightScore > 1 {
		t.Errorf("straight curvature should be ~0, got %.2f", straightScore)
	}

	// R*Tree query roundtrip.
	rows, err := store.QueryByBBox(ctx, db,
		curvyMinLon-0.001, curvyMinLat-0.001, curvyMaxLon+0.001, curvyMaxLat+0.001, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 1 {
		t.Errorf("bbox query returned 0 rows; expected curvy way")
	}
}
