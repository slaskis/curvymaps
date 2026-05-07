package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"

	"github.com/paulmach/orb"
	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"

	"github.com/slaskis/curvymaps/internal/store"
)

// IngestOpts is everything ingest.Run needs.
type IngestOpts struct {
	PBFPath        string
	IncludeUnpaved bool
	// Region is an optional bbox lon1,lat1,lon2,lat2; ways whose bbox lies
	// entirely outside the region are dropped. Zero value = no region filter.
	HasRegion        bool
	MinLon, MinLat   float64
	MaxLon, MaxLat   float64
	StagingBatchSize int
}

func (o IngestOpts) regionContainsBBox(minLon, minLat, maxLon, maxLat float64) bool {
	if !o.HasRegion {
		return true
	}
	// Drop only ways entirely outside the region.
	if maxLon < o.MinLon || minLon > o.MaxLon {
		return false
	}
	if maxLat < o.MinLat || minLat > o.MaxLat {
		return false
	}
	return true
}

// keptWay is what we stash in memory between passes.
type keptWay struct {
	id      int64
	tags    osm.Tags
	nodeIDs []int64
}

// Run streams `path` twice and writes filtered+resolved ways to ways_staging.
func Run(ctx context.Context, db *sql.DB, opts IngestOpts) (int, error) {
	if opts.StagingBatchSize <= 0 {
		opts.StagingBatchSize = 5000
	}

	// Pass 1: ways. Collect kept ways and the set of node IDs they reference.
	log.Printf("ingest: pass 1 — scanning ways")
	kept, needed, err := scanWays(ctx, opts)
	if err != nil {
		return 0, fmt.Errorf("pass 1: %w", err)
	}
	log.Printf("ingest: pass 1 — %d ways kept, %d unique nodes referenced",
		len(kept), len(needed))

	// Pass 2: nodes. Resolve coords for needed IDs.
	log.Printf("ingest: pass 2 — resolving node coords")
	coords, err := scanNodes(ctx, opts.PBFPath, needed)
	if err != nil {
		return 0, fmt.Errorf("pass 2: %w", err)
	}
	log.Printf("ingest: pass 2 — %d/%d node coords resolved", len(coords), len(needed))

	// Materialize: build LineStrings and write to ways_staging in batches.
	log.Printf("ingest: materializing ways into staging")
	written, err := materialize(ctx, db, kept, coords, opts)
	if err != nil {
		return 0, fmt.Errorf("materialize: %w", err)
	}
	log.Printf("ingest: %d ways written to ways_staging", written)
	return written, nil
}

func openPBF(path string) (*os.File, error) {
	return os.Open(path)
}

func scanWays(ctx context.Context, opts IngestOpts) ([]keptWay, map[int64]struct{}, error) {
	f, err := openPBF(opts.PBFPath)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	scanner := osmpbf.New(ctx, f, runtime.GOMAXPROCS(-1))
	scanner.SkipNodes = true
	scanner.SkipRelations = true
	defer scanner.Close()

	fopts := FilterOpts{IncludeUnpaved: opts.IncludeUnpaved}
	scanner.FilterWay = func(w *osm.Way) bool { return Accept(w.Tags, fopts) }

	var kept []keptWay
	needed := make(map[int64]struct{})
	for scanner.Scan() {
		w, ok := scanner.Object().(*osm.Way)
		if !ok {
			continue
		}
		if len(w.Nodes) < 2 {
			continue
		}
		ids := make([]int64, len(w.Nodes))
		for i, n := range w.Nodes {
			ids[i] = int64(n.ID)
			needed[ids[i]] = struct{}{}
		}
		kept = append(kept, keptWay{id: int64(w.ID), tags: w.Tags, nodeIDs: ids})
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, nil, err
	}
	return kept, needed, nil
}

// nodeCoord stores lat/lon as float32. Sub-meter precision near 60°N is fine
// for our purposes and halves the map's value-side cost.
type nodeCoord struct {
	lon, lat float32
}

func scanNodes(ctx context.Context, path string, needed map[int64]struct{}) (map[int64]nodeCoord, error) {
	f, err := openPBF(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := osmpbf.New(ctx, f, runtime.GOMAXPROCS(-1))
	scanner.SkipWays = true
	scanner.SkipRelations = true
	defer scanner.Close()

	scanner.FilterNode = func(n *osm.Node) bool {
		_, want := needed[int64(n.ID)]
		return want
	}

	out := make(map[int64]nodeCoord, len(needed))
	for scanner.Scan() {
		n, ok := scanner.Object().(*osm.Node)
		if !ok {
			continue
		}
		out[int64(n.ID)] = nodeCoord{lon: float32(n.Lon), lat: float32(n.Lat)}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}

func materialize(ctx context.Context, db *sql.DB, kept []keptWay, coords map[int64]nodeCoord, opts IngestOpts) (int, error) {
	var batch []store.StagedWay
	written := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := store.InsertStagingBatch(ctx, db, batch); err != nil {
			return err
		}
		written += len(batch)
		batch = batch[:0]
		return nil
	}

	for _, kw := range kept {
		ls := make(orb.LineString, 0, len(kw.nodeIDs))
		minLon, minLat := 180.0, 90.0
		maxLon, maxLat := -180.0, -90.0
		for _, nid := range kw.nodeIDs {
			c, ok := coords[nid]
			if !ok {
				ls = nil
				break
			}
			lon, lat := float64(c.lon), float64(c.lat)
			ls = append(ls, orb.Point{lon, lat})
			if lon < minLon {
				minLon = lon
			}
			if lon > maxLon {
				maxLon = lon
			}
			if lat < minLat {
				minLat = lat
			}
			if lat > maxLat {
				maxLat = lat
			}
		}
		if len(ls) < 2 {
			continue
		}
		if !opts.regionContainsBBox(minLon, minLat, maxLon, maxLat) {
			continue
		}

		var surface, name sql.NullString
		if v := kw.tags.Find("surface"); v != "" {
			surface = sql.NullString{String: v, Valid: true}
		}
		if v := kw.tags.Find("name"); v != "" {
			name = sql.NullString{String: v, Valid: true}
		}

		batch = append(batch, store.StagedWay{
			ID:       kw.id,
			Highway:  kw.tags.Find("highway"),
			Surface:  surface,
			Name:     name,
			Geometry: ls,
			MinLon:   minLon, MaxLon: maxLon,
			MinLat: minLat, MaxLat: maxLat,
		})
		if len(batch) >= opts.StagingBatchSize {
			if err := flush(); err != nil {
				return written, err
			}
		}
	}
	if err := flush(); err != nil {
		return written, err
	}
	return written, nil
}
