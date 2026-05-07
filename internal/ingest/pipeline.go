package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"

	"github.com/slaskis/curvymaps/internal/curvature"
	"github.com/slaskis/curvymaps/internal/store"
)

// Score reads ways_staging, scores every row, and writes to `ways` +
// `ways_rtree`. Drops `ways_staging` when complete.
func Score(ctx context.Context, db *sql.DB) (Summary, error) {
	const batchSize = 5000
	var summary Summary
	var batch []store.ScoredRow

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := store.InsertScoredBatch(ctx, db, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	err := store.IterateStaging(ctx, db, func(w store.StagedWay) error {
		s := curvature.All(w.Geometry)
		row := store.ScoredRow{
			ID:                    w.ID,
			Highway:               w.Highway,
			Surface:               w.Surface,
			Name:                  w.Name,
			LengthM:               s.LengthM,
			Sinuosity:             s.Sinuosity,
			HeadingChangeDegPerKm: s.HeadingChangeDegPerKm,
			Curvature:             s.Curvature,
			Geometry:              w.Geometry,
			MinLon:                w.MinLon,
			MaxLon:                w.MaxLon,
			MinLat:                w.MinLat,
			MaxLat:                w.MaxLat,
		}
		batch = append(batch, row)
		summary.collect(row)
		if len(batch) >= batchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return summary, fmt.Errorf("score iterate: %w", err)
	}
	if err := flush(); err != nil {
		return summary, err
	}
	if err := store.DropStaging(db); err != nil {
		log.Printf("warn: drop ways_staging: %v", err)
	}
	summary.finalize()
	return summary, nil
}

// Rescore recomputes scores in-place from existing geometry, no PBF re-parse.
func Rescore(ctx context.Context, db *sql.DB) (Summary, error) {
	var summary Summary

	// Collect scored updates first; iterating + updating within the same
	// connection is fine, but batching lets us do all writes in one tx.
	type update struct {
		id      int64
		highway string
		name    sql.NullString
		lengthM float64
		sinuo   float64
		hdpk    float64
		curv    float64
	}
	var updates []update

	if err := store.IterateAll(ctx, db, func(w store.Way) error {
		s := curvature.All(w.Geometry)
		updates = append(updates, update{
			id: w.ID, highway: w.Highway, name: w.Name,
			lengthM: s.LengthM, sinuo: s.Sinuosity,
			hdpk: s.HeadingChangeDegPerKm, curv: s.Curvature,
		})
		summary.collect(store.ScoredRow{
			ID:        w.ID,
			Highway:   w.Highway,
			Name:      w.Name,
			LengthM:   s.LengthM,
			Curvature: s.Curvature,
		})
		return nil
	}); err != nil {
		return summary, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return summary, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
        UPDATE ways SET length_m=?, sinuosity=?, heading_change_deg_per_km=?, curvature=?
        WHERE id=?
    `)
	if err != nil {
		return summary, err
	}
	defer stmt.Close()
	for _, u := range updates {
		if _, err := stmt.ExecContext(ctx, u.lengthM, u.sinuo, u.hdpk, u.curv, u.id); err != nil {
			return summary, err
		}
	}
	if err := tx.Commit(); err != nil {
		return summary, err
	}
	summary.finalize()
	return summary, nil
}

// Summary aggregates ingest stats for the post-run printout.
type Summary struct {
	Ways         int
	TotalLengthM float64
	lengths      []float64
	curvatures   []float64
	topNamed     topHeap // top-10 named ways by curvature

	// Filled by finalize:
	LengthP50   float64
	LengthP90   float64
	LengthP99   float64
	CurvatureQ1 float64 // 20th percentile
	CurvatureQ2 float64 // 40th
	CurvatureQ3 float64 // 60th
	CurvatureQ4 float64 // 80th
	TopNamed    []NamedScore
}

type NamedScore struct {
	ID        int64
	Highway   string
	Name      string
	LengthM   float64
	Curvature float64
}

func (s *Summary) collect(r store.ScoredRow) {
	s.Ways++
	s.TotalLengthM += r.LengthM
	s.lengths = append(s.lengths, r.LengthM)
	s.curvatures = append(s.curvatures, r.Curvature)
	if r.Name.Valid && r.Name.String != "" {
		s.topNamed.push(NamedScore{
			ID:        r.ID,
			Highway:   r.Highway,
			Name:      r.Name.String,
			LengthM:   r.LengthM,
			Curvature: r.Curvature,
		}, 10)
	}
}

func (s *Summary) finalize() {
	sort.Float64s(s.lengths)
	sort.Float64s(s.curvatures)
	s.LengthP50 = pctile(s.lengths, 0.50)
	s.LengthP90 = pctile(s.lengths, 0.90)
	s.LengthP99 = pctile(s.lengths, 0.99)
	s.CurvatureQ1 = pctile(s.curvatures, 0.20)
	s.CurvatureQ2 = pctile(s.curvatures, 0.40)
	s.CurvatureQ3 = pctile(s.curvatures, 0.60)
	s.CurvatureQ4 = pctile(s.curvatures, 0.80)
	s.TopNamed = s.topNamed.sorted()
}

func pctile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p * float64(len(sorted)))
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

// Print writes a human summary to the logger.
func (s Summary) Print() {
	log.Printf("=== ingest summary ===")
	log.Printf("ways:        %d", s.Ways)
	log.Printf("total km:    %.1f", s.TotalLengthM/1000)
	log.Printf("length p50:  %.0f m", s.LengthP50)
	log.Printf("length p90:  %.0f m", s.LengthP90)
	log.Printf("length p99:  %.0f m", s.LengthP99)
	log.Printf("curvature quintiles (20/40/60/80): %.1f / %.1f / %.1f / %.1f",
		s.CurvatureQ1, s.CurvatureQ2, s.CurvatureQ3, s.CurvatureQ4)
	log.Printf("top 10 named ways by curvature:")
	for i, t := range s.TopNamed {
		log.Printf("  %2d. %-30s %-12s %5.1f km  curvature=%.0f",
			i+1, truncate(t.Name, 30), t.Highway, t.LengthM/1000, t.Curvature)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// topHeap is a small bounded "keep top N by curvature" container. Cheap
// linear-scan since N is tiny.
type topHeap []NamedScore

func (h *topHeap) push(s NamedScore, max int) {
	if len(*h) < max {
		*h = append(*h, s)
		return
	}
	// Find the smallest entry; replace if s beats it.
	minIdx := 0
	for i := range *h {
		if (*h)[i].Curvature < (*h)[minIdx].Curvature {
			minIdx = i
		}
	}
	if s.Curvature > (*h)[minIdx].Curvature {
		(*h)[minIdx] = s
	}
}

func (h topHeap) sorted() []NamedScore {
	out := append([]NamedScore(nil), h...)
	sort.Slice(out, func(i, j int) bool { return out[i].Curvature > out[j].Curvature })
	return out
}
