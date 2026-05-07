package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

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
}

func runIngest(args []string) {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	dbPath := fs.String("db", "curvymaps.db", "Output SQLite path")
	includeUnpaved := fs.Bool("unpaved", false, "Keep unpaved roads (gravel, dirt)")
	region := fs.String("region", "", "Optional bbox lon1,lat1,lon2,lat2")
	rescore := fs.Bool("rescore", false, "Recompute scores from existing geometry; do not parse PBF")
	if err := fs.Parse(args); err != nil {
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

	if fs.NArg() < 1 {
		log.Fatal("ingest requires a path-to.osm.pbf argument (or use --rescore)")
	}
	opts := ingest.IngestOpts{
		PBFPath:        fs.Arg(0),
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
	if err := fs.Parse(args); err != nil {
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
