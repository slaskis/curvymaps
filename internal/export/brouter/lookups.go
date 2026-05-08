package brouter

import (
	"fmt"
	"strings"

	"github.com/slaskis/curvymaps/internal/curvature"
)

// LookupsAdditions returns the text to merge into BRouter's lookups.dat
// before running the rd5 preprocessor. Without this merge, the preprocessor
// silently drops every curvymaps:* tag from the segment files and routes
// look identical to vanilla BRouter — the silent-failure mode the export
// exists to guard against.
//
// Format mirrors lookups.dat: one blank line, then `key v0 v1 v2 …` per tag.
func LookupsAdditions(algos []curvature.Algorithm) string {
	var b strings.Builder
	b.WriteString("# curvymaps tag additions — merge into BRouter's lookups.dat\n")
	b.WriteString("# before running the rd5 preprocessor. Each tag carries a\n")
	b.WriteString("# bucket label b0..b4 matching the curvymaps frontend ramp.\n")
	for _, a := range algos {
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s", TagKey(a))
		for _, v := range AllBucketLabels() {
			fmt.Fprintf(&b, " %s", v)
		}
		b.WriteString("\n")
	}
	return b.String()
}
