# curvymaps — Algorithmic Curvy Road Finder

A tile-served map where every road is colored by how curvy it is, so
motorcyclists can browse a region and discover bendy roads no algorithm
in Google Maps will ever surface.

Pipeline: ingest a Geofabrik `.osm.pbf` → score every way's curvature →
serve vector tiles → MapLibre frontend with a curvature slider.

## Quickstart

```sh
# 1. Score a Geofabrik PBF into a SQLite db
go run ./cmd/curvymaps ingest --db /tmp/cm.db testdata/monaco-latest.osm.pbf

# 2. Serve tiles + frontend
go run ./cmd/curvymaps serve --db /tmp/cm.db --addr :8080

# 3. Open http://localhost:8080
```

For Sweden, fetch
`https://download.geofabrik.de/europe/sweden-latest.osm.pbf` (~700MB) and
ingest into a separate db. Add `--region lon1,lat1,lon2,lat2` for a
fast-iteration single-kommun ingest, or `--unpaved` to keep gravel.

To re-tune the scoring math without re-parsing the PBF:

```sh
go run ./cmd/curvymaps ingest --rescore --db /tmp/cm.db
```

## Architecture

Pure Go, `modernc.org/sqlite` (no CGO), single binary plus an embedded
static frontend. Ingestion runs offline as a subcommand; serving is a
separate subcommand against the same SQLite file.

|Concern       |Choice                                              |
|--------------|----------------------------------------------------|
|Language      |Go, no CGO                                          |
|SQLite driver |`modernc.org/sqlite` (R*Tree included)              |
|OSM PBF parser|`paulmach/osm`                                      |
|Geometry / MVT|`paulmach/orb` (geo, mvt, wkb, simplify, maptile)   |
|Spatial index |SQLite R*Tree virtual table                         |
|HTTP          |`net/http` + `go-chi/chi/v5`                        |
|Frontend      |MapLibre GL JS from CDN, one embedded `index.html`  |

### Schema

`ways` rows carry highway/surface/name, length_m, sinuosity,
heading_change_deg_per_km, curvature, and a WKB LineString geometry.
`ways_rtree` is an R*Tree virtual table mapping way ID → bbox for tile
lookups. `ways_staging` is a transient table used only during ingest.

### Curvature scoring

Adam Franco's radius-bucket weighting
(<https://github.com/adamfranco/curvature>):

| radius (m)    | weight |
|---------------|--------|
| r ≥ 175       | 0.0    |
| 100 ≤ r < 175 | 1.0    |
| 60 ≤ r < 100  | 1.3    |
| 30 ≤ r < 60   | 1.6    |
| r < 30        | 2.0    |

Per-way score: Σ (segment_length_m × weight). Units: weighted meters of
curvy road. We also store sinuosity and heading-change-per-km as
auxiliary metrics.

## Layout

```
cmd/curvymaps/main.go          # CLI: ingest | serve
internal/curvature/            # Pure scoring fns + tests
internal/ingest/               # PBF parsing, way filtering, pipeline
internal/store/                # SQLite schema, queries
internal/server/               # MVT tiles, GeoJSON, LRU cache, embedded UI
testdata/monaco-latest.osm.pbf # Hermetic fixture (Geofabrik)
```

## Notes

- **No CGO.** `modernc.org/sqlite`, not `mattn/go-sqlite3`.
- **WKB geometry**, not Google encoded polylines. `paulmach/orb` does not
  ship a polyline encoder; WKB is in-tree, fast, and the size penalty is
  small.
- **OSM data is ODbL.** The frontend footer attributes
  "© OpenStreetMap contributors".
- **Routing is out of scope for v1.** Use BRouter externally if you want
  curvature-weighted A→B routing. A future agent could export per-way
  scores as a BRouter sidecar.

## Testing

```sh
go test ./... -race
```

Unit coverage for the pure curvature math (synthetic circles, straight
lines, reverse invariance, bucket boundaries) plus integration tests for
the staging→score pipeline and the HTTP handlers using an in-process
SQLite DB.

## License

Code: TBD. Map data: © OpenStreetMap contributors, available under the
Open Database License.
