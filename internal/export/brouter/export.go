package brouter

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"runtime"
	"strconv"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"

	"github.com/slaskis/curvymaps/internal/curvature"
)

// Stats summarises one Export run.
type Stats struct {
	Nodes         int64
	Ways          int64
	Relations     int64
	WaysTagged    int64 // ways that had a row in the scores map
	WaysUntagged  int64 // ways missing from the scores map (got default-bucket tags)
	Algorithms    []string
}

// Opts is everything Export needs.
type Opts struct {
	In         io.Reader
	Out        io.Writer
	Scores     map[int64]curvature.Scores
	Algorithms []curvature.Algorithm
}

// Export streams an OSM PBF from o.In, copies every element to o.Out as OSM
// XML, and injects one curvymaps:<algo_id>=b0..b4 tag per algorithm on every
// way. Ways missing from o.Scores still get tagged (with the bucket the score
// 0 falls into for that algorithm) so BRouter profiles can rely on the tag
// being present everywhere — empty values would route as if the way were
// unscored, which is the silent-failure mode we want to avoid.
//
// Output is OSM XML, not PBF: paulmach/osm has no PBF writer and no
// maintained pure-Go writer exists. Compress with bzip2 before feeding to
// BRouter's `OsmFastCutter`. PBF output is a follow-up if file size becomes
// a problem on country-scale extracts.
func Export(ctx context.Context, o Opts) (Stats, error) {
	if o.In == nil || o.Out == nil {
		return Stats{}, fmt.Errorf("brouter.Export: In and Out are required")
	}
	if len(o.Algorithms) == 0 {
		return Stats{}, fmt.Errorf("brouter.Export: at least one algorithm required")
	}

	stats := Stats{Algorithms: make([]string, len(o.Algorithms))}
	for i, a := range o.Algorithms {
		stats.Algorithms[i] = a.ID
	}

	// Bucket label for the zero-score case, computed once per algorithm.
	zeroBuckets := make([]string, len(o.Algorithms))
	for i, a := range o.Algorithms {
		zeroBuckets[i] = BucketFor(a, curvature.Scores{})
	}

	w := bufio.NewWriterSize(o.Out, 1<<20)
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return stats, err
	}
	if _, err := io.WriteString(w, `<osm version="0.6" generator="curvymaps">`+"\n"); err != nil {
		return stats, err
	}

	scanner := osmpbf.New(ctx, o.In, runtime.GOMAXPROCS(-1))
	defer scanner.Close()

	for scanner.Scan() {
		switch e := scanner.Object().(type) {
		case *osm.Node:
			stats.Nodes++
			if err := writeNode(w, e); err != nil {
				return stats, fmt.Errorf("encode node %d: %w", e.ID, err)
			}
		case *osm.Way:
			stats.Ways++
			s, ok := o.Scores[int64(e.ID)]
			extra := make(osm.Tags, 0, len(o.Algorithms))
			for i, a := range o.Algorithms {
				key := TagKey(a)
				var val string
				if ok {
					val = BucketFor(a, s)
				} else {
					val = zeroBuckets[i]
				}
				extra = append(extra, osm.Tag{Key: key, Value: val})
			}
			if ok {
				stats.WaysTagged++
			} else {
				stats.WaysUntagged++
			}
			e.Tags = append(e.Tags, extra...)
			if err := writeWay(w, e); err != nil {
				return stats, fmt.Errorf("encode way %d: %w", e.ID, err)
			}
		case *osm.Relation:
			stats.Relations++
			if err := writeRelation(w, e); err != nil {
				return stats, fmt.Errorf("encode relation %d: %w", e.ID, err)
			}
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return stats, fmt.Errorf("scan input: %w", err)
	}
	if _, err := io.WriteString(w, "</osm>\n"); err != nil {
		return stats, err
	}
	return stats, w.Flush()
}

// The slim writers below emit only fields BRouter's preprocessor needs:
// id, geometry, node refs, members, and tags. Skipping user/uid/changeset/
// timestamp/visible/version metadata cuts output size roughly in half on
// real-world PBFs, which makes country-scale exports tractable.

func writeNode(w *bufio.Writer, n *osm.Node) error {
	if _, err := fmt.Fprintf(w, `  <node id="%d" lat="%s" lon="%s"`,
		int64(n.ID), strconv.FormatFloat(n.Lat, 'f', -1, 64),
		strconv.FormatFloat(n.Lon, 'f', -1, 64)); err != nil {
		return err
	}
	if len(n.Tags) == 0 {
		_, err := io.WriteString(w, "/>\n")
		return err
	}
	if _, err := io.WriteString(w, ">\n"); err != nil {
		return err
	}
	if err := writeTags(w, n.Tags, "    "); err != nil {
		return err
	}
	_, err := io.WriteString(w, "  </node>\n")
	return err
}

func writeWay(w *bufio.Writer, way *osm.Way) error {
	if _, err := fmt.Fprintf(w, `  <way id="%d">`+"\n", int64(way.ID)); err != nil {
		return err
	}
	for _, n := range way.Nodes {
		if _, err := fmt.Fprintf(w, `    <nd ref="%d"/>`+"\n", int64(n.ID)); err != nil {
			return err
		}
	}
	if err := writeTags(w, way.Tags, "    "); err != nil {
		return err
	}
	_, err := io.WriteString(w, "  </way>\n")
	return err
}

func writeRelation(w *bufio.Writer, r *osm.Relation) error {
	if _, err := fmt.Fprintf(w, `  <relation id="%d">`+"\n", int64(r.ID)); err != nil {
		return err
	}
	for _, m := range r.Members {
		if _, err := fmt.Fprintf(w, `    <member type="%s" ref="%d" role="`, m.Type, m.Ref); err != nil {
			return err
		}
		if err := xml.EscapeText(w, []byte(m.Role)); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\"/>\n"); err != nil {
			return err
		}
	}
	if err := writeTags(w, r.Tags, "    "); err != nil {
		return err
	}
	_, err := io.WriteString(w, "  </relation>\n")
	return err
}

func writeTags(w *bufio.Writer, tags osm.Tags, indent string) error {
	for _, t := range tags {
		if _, err := io.WriteString(w, indent); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `<tag k="`); err != nil {
			return err
		}
		if err := xml.EscapeText(w, []byte(t.Key)); err != nil {
			return err
		}
		if _, err := io.WriteString(w, `" v="`); err != nil {
			return err
		}
		if err := xml.EscapeText(w, []byte(t.Value)); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\"/>\n"); err != nil {
			return err
		}
	}
	return nil
}
