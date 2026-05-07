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

`ways` rows carry highway/surface/name plus all five score columns
(length_m, sinuosity, heading_change_deg_per_km, curvature,
mean_inv_radius, max_inv_radius) and a WKB LineString geometry.
`ways_rtree` is an R*Tree virtual table mapping way ID → bbox for tile
lookups. `ways_staging` is a transient table used only during ingest.

DBs created before the multi-algorithm columns existed are migrated
automatically on first `Open()` (the new columns are added with default
0). After upgrading, run `curvymaps ingest --rescore --db <path>` once
to populate them with real values.

### Algorithms

Every way is scored with five algorithms in a single pass at ingest
time. The frontend exposes a dropdown to switch which one ranks and
colors the map; tiles already carry every score, so swapping is
instant — no re-ingest, no tile refetch.

| ID                | Formula                                     | Unit       | Typical range |
|-------------------|---------------------------------------------|------------|---------------|
| `franco`          | Σ length × radius-bucket weight (see below) | weighted m | 0 – 2000+     |
| `sinuosity`       | length / chord                              | ratio      | 1.0 – 2.0+    |
| `heading_change`  | Σ \|Δbearing\| / km                         | deg/km     | 0 – 1500      |
| `mean_inv_radius` | length-weighted mean of 1/r over triplets   | 1/m        | 0 – 0.05      |
| `max_inv_radius`  | maximum 1/r along the way (tightest turn)   | 1/m        | 0 – 0.1       |

`franco` uses Adam Franco's radius-bucket weighting
(<https://github.com/adamfranco/curvature>):

| radius (m)    | weight |
|---------------|--------|
| r ≥ 175       | 0.0    |
| 100 ≤ r < 175 | 1.0    |
| 60 ≤ r < 100  | 1.3    |
| 30 ≤ r < 60   | 1.6    |
| r < 30        | 2.0    |

`mean_inv_radius` rewards roads with sustained gentle bends; long, mildly
curvy stretches climb the ranking. `max_inv_radius` highlights single
hairpins on otherwise-straight roads. Comparing the two side-by-side
makes the difference obvious.

### API

- `GET /tiles/{z}/{x}/{y}.mvt` — MVT vector tiles. Every feature
  carries every algorithm's score as a property.
- `GET /api/ways?bbox=lon1,lat1,lon2,lat2&algo=<id>&min=<float>` —
  GeoJSON `FeatureCollection`, optionally filtered by min score on the
  selected algorithm. `algo` defaults to `franco`. **Breaking:** the
  former `min_curvature` parameter has been removed.
- `GET /algorithms` — JSON list of algorithm metadata (id, label,
  unit, default slider stops, color ramp). The frontend uses this to
  build the dropdown and the legend.

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
