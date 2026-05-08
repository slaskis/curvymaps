# curvymaps → BRouter

Files in this directory turn curvymaps' per-way curvature scores into a
sidecar that [BRouter](https://brouter.de/) can route against.

## What's here

- `lookups-additions.txt` — additions to merge into BRouter's `lookups.dat`
  before running its rd5 preprocessor. **Without this merge step, BRouter
  silently drops every `curvymaps:*` tag and routes look identical to
  vanilla** — this is the failure mode `curvymaps export brouter --verify`
  exists to catch on the export side, but on the BRouter side you just
  have to remember.
- `curvymaps-motorcycle.brf` — a *starter* profile that prefers ways with
  a high `curvymaps:franco` bucket. It is intentionally minimal; merge the
  ideas into BRouter's stock motorcycle/car profile for production use.

## End-to-end workflow

```sh
# 1. Score a PBF as usual
curvymaps ingest --db curvymaps.db monaco-latest.osm.pbf

# 2. Tag the same PBF with curvymaps:* tags, verifying the result
curvymaps export brouter \
    --db curvymaps.db \
    --pbf monaco-latest.osm.pbf \
    --out monaco-tagged.osm \
    --lookups-out lookups-additions.txt \
    --verify

# 3. Merge lookups-additions.txt into BRouter's lookups.dat
#    (BRouter's preprocessor must see this BEFORE generating rd5 files)

# 4. Compress and feed to BRouter's preprocessor
bzip2 monaco-tagged.osm
# … run BRouter's OsmFastCutter against monaco-tagged.osm.bz2

# 5. Drop curvymaps-motorcycle.brf into BRouter's profiles2/ and route
```

## Picking a different algorithm

The export emits a tag per algorithm in `curvature.Algorithms`:

| Tag                             | Algorithm                  |
|---------------------------------|----------------------------|
| `curvymaps:franco`              | Franco weighted (default)  |
| `curvymaps:sinuosity`           | Length / chord             |
| `curvymaps:heading_change`      | Sum heading change per km  |
| `curvymaps:mean_inv_radius`     | Mean 1/radius              |
| `curvymaps:max_inv_radius`      | Tightest turn (max 1/r)    |

To weight by a different algorithm, change the `assign curvymaps_score`
line in `curvymaps-motorcycle.brf` to the matching tag key. Bucket labels
(`b0`–`b4`) are consistent across algorithms — they always map to the same
five colors as the curvymaps frontend slider.

## Output format

The export writes OSM XML (`.osm`), not PBF, because no maintained pure-Go
PBF writer exists. For Monaco-scale extracts this is fine; for country
scale (Sweden ≈ 700 MB PBF → ≈ 3 GB XML) compress with `bzip2` and feed
the `.osm.bz2` to BRouter's `OsmFastCutter`. PBF output is a follow-up.
