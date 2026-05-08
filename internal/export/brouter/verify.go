package brouter

import (
	"context"
	"fmt"
	"io"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmxml"

	"github.com/slaskis/curvymaps/internal/curvature"
)

// VerifyResult is what Verify returns. Missing > 0 means the export is
// broken; UnknownBucket > 0 means a tag value drifted out of b0..b4.
type VerifyResult struct {
	Ways          int64
	Tagged        int64            // ways with every expected tag present
	Missing       int64            // ways missing at least one expected tag
	UnknownBucket int64            // ways with at least one tag whose value is not b0..b4
	PerAlgo       map[string]int64 // per-algo tag presence count
}

// Verify reads an exported OSM XML file and confirms each way carries the
// curvymaps:<algo_id> tags it should. Use this to catch the silent-failure
// modes that BRouter's preprocessor would otherwise mask: a missed tag
// injection (export bug) or a corrupted bucket value (drift).
func Verify(ctx context.Context, r io.Reader, algos []curvature.Algorithm) (VerifyResult, error) {
	res := VerifyResult{PerAlgo: make(map[string]int64, len(algos))}
	allowed := make(map[string]struct{}, BucketCount)
	for _, l := range AllBucketLabels() {
		allowed[l] = struct{}{}
	}

	scanner := osmxml.New(ctx, r)
	defer scanner.Close()

	for scanner.Scan() {
		w, ok := scanner.Object().(*osm.Way)
		if !ok {
			continue
		}
		res.Ways++
		hadAll := true
		hadBadValue := false
		for _, a := range algos {
			key := TagKey(a)
			val := w.Tags.Find(key)
			if val == "" {
				hadAll = false
				continue
			}
			res.PerAlgo[a.ID]++
			if _, ok := allowed[val]; !ok {
				hadBadValue = true
			}
		}
		if hadAll {
			res.Tagged++
		} else {
			res.Missing++
		}
		if hadBadValue {
			res.UnknownBucket++
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		return res, fmt.Errorf("scan output: %w", err)
	}
	return res, nil
}
