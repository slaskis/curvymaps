# testdata

`monaco-latest.osm.pbf` — Geofabrik's nightly extract of Monaco
(<https://download.geofabrik.de/europe/monaco-latest.osm.pbf>, ~700KB). It is
the canonical small fixture for OSM tooling: hilly Mediterranean coast, lots
of switchback residential climbs, runs through ingest end-to-end in <1s on
a laptop. Re-fetch with:

```
curl -fL -o testdata/monaco-latest.osm.pbf \
  https://download.geofabrik.de/europe/monaco-latest.osm.pbf
```

The file is committed for hermetic CI. If you replace it with a different
extract, update this README.
