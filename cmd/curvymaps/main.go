package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/slaskis/curvymaps/internal/curvature"
	"github.com/slaskis/curvymaps/internal/export/brouter"
	"github.com/slaskis/curvymaps/internal/ingest"
	"github.com/slaskis/curvymaps/internal/server"
	"github.com/slaskis/curvymaps/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "ingest":
		runIngest(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	case "export":
		runExport(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: curvymaps <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  ingest <path-to.osm.pbf>   Score a Geofabrik PBF into a SQLite db")
	fmt.Fprintln(os.Stderr, "  serve                      Serve MVT tiles + frontend from a db")
	fmt.Fprintln(os.Stderr, "  export brouter             Tag a PBF with curvymaps:* tags for BRouter")
}

func runIngest(args []string) {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	dbPath := fs.String("db", "curvymaps.db", "Output SQLite path")
	includeUnpaved := fs.Bool("unpaved", false, "Keep unpaved roads (gravel, dirt)")
	region := fs.String("region", "", "Optional bbox lon1,lat1,lon2,lat2")
	rescore := fs.Bool("rescore", false, "Recompute scores from existing geometry; do not parse PBF")
	positional, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		os.Exit(2)
	}

	ctx := context.Background()
	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	if *rescore {
		summary, err := ingest.Rescore(ctx, db)
		if err != nil {
			log.Fatalf("rescore: %v", err)
		}
		summary.Print()
		return
	}

	if len(positional) < 1 {
		log.Fatal("ingest requires a path-to.osm.pbf argument (or use --rescore)")
	}
	opts := ingest.IngestOpts{
		PBFPath:        positional[0],
		IncludeUnpaved: *includeUnpaved,
	}
	if *region != "" {
		bb, err := parseBBox(*region)
		if err != nil {
			log.Fatalf("--region: %v", err)
		}
		opts.HasRegion = true
		opts.MinLon, opts.MinLat, opts.MaxLon, opts.MaxLat = bb[0], bb[1], bb[2], bb[3]
	}

	if _, err := ingest.Run(ctx, db, opts); err != nil {
		log.Fatalf("ingest.Run: %v", err)
	}
	summary, err := ingest.Score(ctx, db)
	if err != nil {
		log.Fatalf("ingest.Score: %v", err)
	}
	summary.Print()
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "curvymaps.db", "SQLite db path")
	addr := fs.String("addr", ":8080", "HTTP listen address")
	noCache := fs.Bool("no-cache", false, "Disable in-memory tile cache")
	if _, err := parseFlagsAnywhere(fs, args); err != nil {
		os.Exit(2)
	}

	db, err := store.OpenReadOnly(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	srv, err := server.New(db, server.Opts{NoCache: *noCache})
	if err != nil {
		log.Fatalf("server.New: %v", err)
	}
	log.Printf("listening on %s", *addr)
	if err := srv.ListenAndServe(*addr); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func runExport(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "export requires a target: brouter")
		os.Exit(2)
	}
	switch args[0] {
	case "brouter":
		runExportBrouter(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown export target: %q\n", args[0])
		os.Exit(2)
	}
}

// stringList is a flag.Value for repeatable --algo flags.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func runExportBrouter(args []string) {
	fs := flag.NewFlagSet("export brouter", flag.ExitOnError)
	dbPath := fs.String("db", "curvymaps.db", "Source SQLite db produced by `ingest`")
	pbfPath := fs.String("pbf", "", "Input OSM PBF (the same one used for ingest)")
	outPath := fs.String("out", "", "Output OSM XML path (.osm). Compress with bzip2 for BRouter's OsmFastCutter.")
	lookupsPath := fs.String("lookups-out", "", "Optional path to write lookups.dat additions to")
	verify := fs.Bool("verify", false, "Re-read --out after writing and assert tag presence")
	var algoFilter stringList
	fs.Var(&algoFilter, "algo", "Algorithm IDs to emit (repeatable; default: all)")
	if _, err := parseFlagsAnywhere(fs, args); err != nil {
		os.Exit(2)
	}

	if *pbfPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "export brouter requires --pbf and --out")
		fs.Usage()
		os.Exit(2)
	}

	algos, err := selectAlgorithms(algoFilter)
	if err != nil {
		log.Fatalf("--algo: %v", err)
	}

	ctx := context.Background()
	db, err := store.OpenReadOnly(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	scores := make(map[int64]curvature.Scores)
	if err := store.IterateScores(ctx, db, func(id int64, s curvature.Scores) error {
		scores[id] = s
		return nil
	}); err != nil {
		log.Fatalf("load scores: %v", err)
	}
	log.Printf("export: loaded %d scored ways from %s", len(scores), *dbPath)

	in, err := os.Open(*pbfPath)
	if err != nil {
		log.Fatalf("open pbf: %v", err)
	}
	defer in.Close()

	out, err := os.Create(*outPath)
	if err != nil {
		log.Fatalf("create output: %v", err)
	}
	defer out.Close()

	stats, err := brouter.Export(ctx, brouter.Opts{
		In: in, Out: out, Scores: scores, Algorithms: algos,
	})
	if err != nil {
		log.Fatalf("export: %v", err)
	}
	if err := out.Close(); err != nil {
		log.Fatalf("close output: %v", err)
	}
	log.Printf("export: %d nodes, %d ways (%d scored, %d default-bucket), %d relations → %s [algos=%s]",
		stats.Nodes, stats.Ways, stats.WaysTagged, stats.WaysUntagged, stats.Relations,
		*outPath, brouter.AlgoIDList(algos))

	if *lookupsPath != "" {
		text := brouter.LookupsAdditions(algos)
		if err := os.WriteFile(*lookupsPath, []byte(text), 0o644); err != nil {
			log.Fatalf("write lookups: %v", err)
		}
		log.Printf("export: wrote lookups additions to %s", *lookupsPath)
	}

	if *verify {
		f, err := os.Open(*outPath)
		if err != nil {
			log.Fatalf("verify open: %v", err)
		}
		defer f.Close()
		v, err := brouter.Verify(ctx, f, algos)
		if err != nil {
			log.Fatalf("verify: %v", err)
		}
		log.Printf("verify: %d ways, %d tagged, %d missing, %d bad-bucket",
			v.Ways, v.Tagged, v.Missing, v.UnknownBucket)
		if v.Missing > 0 || v.UnknownBucket > 0 {
			log.Fatal("verify failed: see counts above")
		}
	}
}

// selectAlgorithms returns curvature.Algorithms filtered by the --algo IDs,
// preserving registry order. Empty filter = every algorithm.
func selectAlgorithms(filter []string) ([]curvature.Algorithm, error) {
	if len(filter) == 0 {
		return curvature.Algorithms, nil
	}
	want := make(map[string]struct{}, len(filter))
	for _, id := range filter {
		if _, ok := curvature.ByID(id); !ok {
			return nil, fmt.Errorf("unknown algorithm %q", id)
		}
		want[id] = struct{}{}
	}
	out := make([]curvature.Algorithm, 0, len(want))
	for _, a := range curvature.Algorithms {
		if _, ok := want[a.ID]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// parseFlagsAnywhere parses flags interleaved with positional args. Go's
// flag.Parse stops at the first non-flag, which surprises users who put
// `cmd <positional> --flag`. This loop keeps parsing flags after each
// positional and returns the positionals in order.
func parseFlagsAnywhere(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func parseBBox(s string) ([4]float64, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return [4]float64{}, fmt.Errorf("want 4 comma-separated floats, got %d parts", len(parts))
	}
	var out [4]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return out, fmt.Errorf("part %d: %w", i, err)
		}
		out[i] = v
	}
	if out[0] >= out[2] || out[1] >= out[3] {
		return out, fmt.Errorf("bbox order: want lon1<lon2 and lat1<lat2")
	}
	return out, nil
}
