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

## BRouter export

`curvymaps export brouter` rewrites the same PBF you ingested with one
synthetic tag per algorithm — `curvymaps:franco`, `curvymaps:sinuosity`,
`curvymaps:heading_change`, `curvymaps:mean_inv_radius`,
`curvymaps:max_inv_radius` — bucketed `b0`–`b4` using each algorithm's
`DefaultStops` so the BRouter view stays in lockstep with the map's
5-color ramp. Routing then happens entirely inside BRouter using a
profile that biases costfactor against the bucket.

```sh
curvymaps export brouter \
    --db curvymaps.db \
    --pbf monaco-latest.osm.pbf \
    --out monaco-tagged.osm \
    --lookups-out lookups-additions.txt \
    --verify
bzip2 monaco-tagged.osm
```

Then merge `lookups-additions.txt` into BRouter's `lookups.dat` **before**
preprocessing. Skip that merge and BRouter silently drops every
`curvymaps:*` tag from the rd5 segments and routes look identical to
vanilla — `--verify` exists to catch the export-side half of this failure
mode; the lookups-merge half has to be remembered.

Output is OSM XML (`.osm`), not PBF, because no maintained pure-Go PBF
writer exists. After `bzip2` the result is roughly the size of the input
PBF and feeds directly into BRouter's `OsmFastCutter`. PBF output is a
follow-up. See `examples/brouter/` for a starter motorcycle profile and
the lookups-additions file.

Algorithm IDs are a stable public surface — they appear in BRouter tag
keys, profile filenames, and the `?algo=` query param. Renaming an
algorithm in `curvature.Algorithms` breaks downstream user setups.

## Layout

```
cmd/curvymaps/main.go          # CLI: ingest | serve | export
internal/curvature/            # Pure scoring fns + tests
internal/ingest/               # PBF parsing, way filtering, pipeline
internal/store/                # SQLite schema, queries
internal/server/               # MVT tiles, GeoJSON, LRU cache, embedded UI
internal/export/brouter/       # BRouter sidecar export (PBF → tagged .osm)
examples/brouter/              # Sample BRouter profile + lookups additions
testdata/monaco-latest.osm.pbf # Hermetic fixture (Geofabrik)
```

## Notes

- **No CGO.** `modernc.org/sqlite`, not `mattn/go-sqlite3`.
- **WKB geometry**, not Google encoded polylines. `paulmach/orb` does not
  ship a polyline encoder; WKB is in-tree, fast, and the size penalty is
  small.
- **OSM data is ODbL.** The frontend footer attributes
  "© OpenStreetMap contributors".
- **Routing is out of scope for v1.** Use BRouter externally — see the
  BRouter export section above — if you want curvature-weighted A→B
  routing.

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
