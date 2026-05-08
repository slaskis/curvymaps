package brouter

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"

	"github.com/slaskis/curvymaps/internal/curvature"
)

func TestBucket(t *testing.T) {
	stops := []float64{50, 200, 500, 1000} // Franco's DefaultStops
	cases := []struct {
		v    float64
		want int
	}{
		{-1, 0}, {0, 0}, {49.99, 0},
		{50, 1}, {199.99, 1},
		{200, 2}, {499.99, 2},
		{500, 3}, {999.99, 3},
		{1000, 4}, {1e9, 4},
	}
	for _, c := range cases {
		if got := Bucket(stops, c.v); got != c.want {
			t.Errorf("Bucket(%v) = %d, want %d", c.v, got, c.want)
		}
	}
}

func TestBucketLabelsAreStable(t *testing.T) {
	want := []string{"b0", "b1", "b2", "b3", "b4"}
	got := AllBucketLabels()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("label[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLookupsAdditionsCoversEveryAlgorithm(t *testing.T) {
	out := LookupsAdditions(curvature.Algorithms)
	for _, a := range curvature.Algorithms {
		key := TagKey(a)
		if !strings.Contains(out, key) {
			t.Errorf("lookups missing tag key %q", key)
		}
		// Every bucket value must appear on the same line as the key.
		for _, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, key) {
				continue
			}
			for _, v := range AllBucketLabels() {
				if !strings.Contains(line, " "+v) {
					t.Errorf("lookups line for %q missing %q: %q", key, v, line)
				}
			}
		}
	}
}

// TestExportRoundTrip uses testdata/monaco-latest.osm.pbf as a real, varied
// input. It builds a synthetic scores map (scoring every other way) and
// asserts the export emits the expected tags and that Verify reports the
// same counts.
func TestExportRoundTrip(t *testing.T) {
	pbfPath := filepath.Join("..", "..", "..", "testdata", "monaco-latest.osm.pbf")
	if _, err := os.Stat(pbfPath); err != nil {
		t.Skipf("fixture missing: %v", err)
	}

	// First pass: collect every way ID so we can construct a deterministic
	// scores map. This exercises both the "scored" and "unscored" paths in
	// Export — every other way gets a score; the rest fall through to the
	// zero-bucket default.
	wayIDs := scanWayIDs(t, pbfPath)
	if len(wayIDs) == 0 {
		t.Fatal("monaco fixture has no ways")
	}
	scores := make(map[int64]curvature.Scores, len(wayIDs)/2)
	for i, id := range wayIDs {
		if i%2 == 0 {
			// Push every algorithm into a non-zero bucket so Verify sees varied tags.
			scores[id] = curvature.Scores{
				LengthM:               1000,
				Sinuosity:             1.5,
				HeadingChangeDegPerKm: 600,
				Curvature:             1500, // Franco b4
				MeanInvRadius:         0.03,
				MaxInvRadius:          0.08,
			}
		}
	}

	in, err := os.Open(pbfPath)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	var out bytes.Buffer
	stats, err := Export(context.Background(), Opts{
		In: in, Out: &out,
		Scores: scores, Algorithms: curvature.Algorithms,
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if int(stats.Ways) != len(wayIDs) {
		t.Errorf("stats.Ways = %d, want %d", stats.Ways, len(wayIDs))
	}
	if got, want := int(stats.WaysTagged), len(scores); got != want {
		t.Errorf("WaysTagged = %d, want %d", got, want)
	}
	if got, want := int(stats.WaysUntagged), len(wayIDs)-len(scores); got != want {
		t.Errorf("WaysUntagged = %d, want %d", got, want)
	}

	// Every algorithm tag should appear at least once in the output XML.
	body := out.String()
	for _, a := range curvature.Algorithms {
		if !strings.Contains(body, TagKey(a)) {
			t.Errorf("output missing tag key %q", TagKey(a))
		}
	}
	if !strings.Contains(body, `<osm version="0.6"`) {
		t.Error("output missing OSM root element")
	}

	// Round-trip via Verify: every way in the output has every expected tag
	// (since the export tags both scored and unscored ways).
	v, err := Verify(context.Background(), strings.NewReader(body), curvature.Algorithms)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if int(v.Ways) != len(wayIDs) {
		t.Errorf("Verify.Ways = %d, want %d", v.Ways, len(wayIDs))
	}
	if v.Missing != 0 {
		t.Errorf("Verify.Missing = %d, want 0", v.Missing)
	}
	if v.UnknownBucket != 0 {
		t.Errorf("Verify.UnknownBucket = %d, want 0", v.UnknownBucket)
	}
	if int(v.Tagged) != len(wayIDs) {
		t.Errorf("Verify.Tagged = %d, want %d", v.Tagged, len(wayIDs))
	}
}

// TestVerifyDetectsMissingTags constructs an OSM XML doc with one way that
// has no curvymaps:* tags and asserts Verify flags it.
func TestVerifyDetectsMissingTags(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<osm version="0.6" generator="test">
  <way id="1" visible="true">
    <nd ref="1"/><nd ref="2"/>
    <tag k="highway" v="primary"/>
  </way>
</osm>
`
	v, err := Verify(context.Background(), strings.NewReader(doc), curvature.Algorithms)
	if err != nil {
		t.Fatal(err)
	}
	if v.Ways != 1 {
		t.Errorf("Ways = %d, want 1", v.Ways)
	}
	if v.Tagged != 0 {
		t.Errorf("Tagged = %d, want 0", v.Tagged)
	}
	if v.Missing != 1 {
		t.Errorf("Missing = %d, want 1", v.Missing)
	}
}

// TestVerifyDetectsBadBucket plants a tag value outside b0..b4.
func TestVerifyDetectsBadBucket(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<osm version="0.6">
  <way id="1" visible="true">
    <tag k="highway" v="primary"/>
    <tag k="curvymaps:franco" v="extreme"/>
    <tag k="curvymaps:sinuosity" v="b1"/>
    <tag k="curvymaps:heading_change" v="b1"/>
    <tag k="curvymaps:mean_inv_radius" v="b1"/>
    <tag k="curvymaps:max_inv_radius" v="b1"/>
  </way>
</osm>
`
	v, err := Verify(context.Background(), strings.NewReader(doc), curvature.Algorithms)
	if err != nil {
		t.Fatal(err)
	}
	if v.UnknownBucket != 1 {
		t.Errorf("UnknownBucket = %d, want 1", v.UnknownBucket)
	}
}

// scanWayIDs returns every way ID in a PBF, in scan order.
func scanWayIDs(t *testing.T, pbfPath string) []int64 {
	t.Helper()
	f, err := os.Open(pbfPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := osmpbf.New(context.Background(), f, runtime.GOMAXPROCS(-1))
	scanner.SkipNodes = true
	scanner.SkipRelations = true
	defer scanner.Close()
	var out []int64
	for scanner.Scan() {
		if w, ok := scanner.Object().(*osm.Way); ok {
			out = append(out, int64(w.ID))
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	return out
}
