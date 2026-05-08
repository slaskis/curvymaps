package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/wkb"
)

// StagedWay is a way written by Agent 1 (ingest) and read by Agent 3 (pipeline).
type StagedWay struct {
	ID       int64
	Highway  string
	Surface  sql.NullString
	Name     sql.NullString
	Geometry orb.LineString
	MinLon   float64
	MaxLon   float64
	MinLat   float64
	MaxLat   float64
}

// Way is the final scored row returned to the server.
type Way struct {
	ID                    int64
	Highway               string
	Surface               sql.NullString
	Name                  sql.NullString
	LengthM               float64
	Sinuosity             float64
	HeadingChangeDegPerKm float64
	Curvature             float64
	MeanInvRadius         float64
	MaxInvRadius          float64
	Geometry              orb.LineString
}

// InsertStagingBatch inserts a batch of staged ways inside a single tx.
func InsertStagingBatch(ctx context.Context, db *sql.DB, batch []StagedWay) error {
	if len(batch) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
        INSERT OR REPLACE INTO ways_staging
        (id, highway, surface, name, geometry, min_lon, max_lon, min_lat, max_lat)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, w := range batch {
		blob, err := wkb.Marshal(w.Geometry)
		if err != nil {
			return fmt.Errorf("wkb marshal way %d: %w", w.ID, err)
		}
		if _, err := stmt.ExecContext(ctx, w.ID, w.Highway, w.Surface, w.Name,
			blob, w.MinLon, w.MaxLon, w.MinLat, w.MaxLat); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IterateStaging streams every staged way to fn. Caller may not hold a tx open.
func IterateStaging(ctx context.Context, db *sql.DB, fn func(StagedWay) error) error {
	rows, err := db.QueryContext(ctx, `
        SELECT id, highway, surface, name, geometry, min_lon, max_lon, min_lat, max_lat
        FROM ways_staging
    `)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var w StagedWay
		var blob []byte
		if err := rows.Scan(&w.ID, &w.Highway, &w.Surface, &w.Name, &blob,
			&w.MinLon, &w.MaxLon, &w.MinLat, &w.MaxLat); err != nil {
			return err
		}
		geom, err := wkb.Unmarshal(blob)
		if err != nil {
			return fmt.Errorf("wkb unmarshal way %d: %w", w.ID, err)
		}
		ls, ok := geom.(orb.LineString)
		if !ok {
			return fmt.Errorf("way %d geometry is %T, want LineString", w.ID, geom)
		}
		w.Geometry = ls
		if err := fn(w); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScoredRow is what we INSERT into `ways` after pipeline scoring.
type ScoredRow struct {
	ID                    int64
	Highway               string
	Surface               sql.NullString
	Name                  sql.NullString
	LengthM               float64
	Sinuosity             float64
	HeadingChangeDegPerKm float64
	Curvature             float64
	MeanInvRadius         float64
	MaxInvRadius          float64
	Geometry              orb.LineString
	MinLon, MaxLon        float64
	MinLat, MaxLat        float64
}

func InsertScoredBatch(ctx context.Context, db *sql.DB, batch []ScoredRow) error {
	if len(batch) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insWay, err := tx.PrepareContext(ctx, `
        INSERT OR REPLACE INTO ways
        (id, highway, surface, name, length_m, sinuosity,
         heading_change_deg_per_km, curvature, mean_inv_radius, max_inv_radius, geometry)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
    `)
	if err != nil {
		return err
	}
	defer insWay.Close()
	insIdx, err := tx.PrepareContext(ctx, `
        INSERT OR REPLACE INTO ways_rtree (id, min_lon, max_lon, min_lat, max_lat)
        VALUES (?, ?, ?, ?, ?)
    `)
	if err != nil {
		return err
	}
	defer insIdx.Close()
	for _, r := range batch {
		blob, err := wkb.Marshal(r.Geometry)
		if err != nil {
			return err
		}
		if _, err := insWay.ExecContext(ctx, r.ID, r.Highway, r.Surface, r.Name,
			r.LengthM, r.Sinuosity, r.HeadingChangeDegPerKm, r.Curvature,
			r.MeanInvRadius, r.MaxInvRadius, blob); err != nil {
			return err
		}
		if _, err := insIdx.ExecContext(ctx, r.ID, r.MinLon, r.MaxLon, r.MinLat, r.MaxLat); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// allowedScoreColumns lists the columns that QueryByBBox may filter on. It
// mirrors the curvature.Algorithms registry; duplicating it here keeps the
// store package free of a curvature import.
var allowedScoreColumns = map[string]struct{}{
	"curvature":                 {},
	"sinuosity":                 {},
	"heading_change_deg_per_km": {},
	"mean_inv_radius":           {},
	"max_inv_radius":            {},
}

// QueryByBBox returns ways whose bbox intersects the query bbox, optionally
// filtered by minScore on the named score column. scoreColumn must be one of
// allowedScoreColumns; an unknown column returns an error rather than
// interpolating raw user input into SQL. Pass scoreColumn="" to skip filtering.
func QueryByBBox(ctx context.Context, db *sql.DB, minLon, minLat, maxLon, maxLat float64, scoreColumn string, minScore float64) ([]Way, error) {
	args := []any{minLon, maxLon, minLat, maxLat}
	scoreClause := ""
	if scoreColumn != "" {
		if _, ok := allowedScoreColumns[scoreColumn]; !ok {
			return nil, fmt.Errorf("unknown score column %q", scoreColumn)
		}
		scoreClause = fmt.Sprintf(" AND w.%s >= ?", scoreColumn)
		args = append(args, minScore)
	}
	q := `
        SELECT w.id, w.highway, w.surface, w.name, w.length_m, w.sinuosity,
               w.heading_change_deg_per_km, w.curvature,
               w.mean_inv_radius, w.max_inv_radius, w.geometry
        FROM ways w
        JOIN ways_rtree r ON r.id = w.id
        WHERE r.max_lon >= ? AND r.min_lon <= ?
          AND r.max_lat >= ? AND r.min_lat <= ?` + scoreClause
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Way
	for rows.Next() {
		var w Way
		var blob []byte
		if err := rows.Scan(&w.ID, &w.Highway, &w.Surface, &w.Name,
			&w.LengthM, &w.Sinuosity, &w.HeadingChangeDegPerKm, &w.Curvature,
			&w.MeanInvRadius, &w.MaxInvRadius, &blob); err != nil {
			return nil, err
		}
		g, err := wkb.Unmarshal(blob)
		if err != nil {
			return nil, err
		}
		ls, ok := g.(orb.LineString)
		if !ok {
			return nil, fmt.Errorf("way %d geometry is %T", w.ID, g)
		}
		w.Geometry = ls
		out = append(out, w)
	}
	return out, rows.Err()
}

// IterateAll streams every scored way (for --rescore).
func IterateAll(ctx context.Context, db *sql.DB, fn func(Way) error) error {
	rows, err := db.QueryContext(ctx, `
        SELECT id, highway, surface, name, length_m, sinuosity,
               heading_change_deg_per_km, curvature,
               mean_inv_radius, max_inv_radius, geometry FROM ways
    `)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var w Way
		var blob []byte
		if err := rows.Scan(&w.ID, &w.Highway, &w.Surface, &w.Name,
			&w.LengthM, &w.Sinuosity, &w.HeadingChangeDegPerKm, &w.Curvature,
			&w.MeanInvRadius, &w.MaxInvRadius, &blob); err != nil {
			return err
		}
		g, err := wkb.Unmarshal(blob)
		if err != nil {
			return err
		}
		ls, ok := g.(orb.LineString)
		if !ok {
			return fmt.Errorf("way %d geometry is %T", w.ID, g)
		}
		w.Geometry = ls
		if err := fn(w); err != nil {
			return err
		}
	}
	return rows.Err()
}

// UpdateScores rewrites the score columns for an existing row (used by --rescore).
func UpdateScores(ctx context.Context, db *sql.DB, id int64, lengthM, sinuosity, headPerKm, curvature, meanInvR, maxInvR float64) error {
	_, err := db.ExecContext(ctx, `
        UPDATE ways
        SET length_m=?, sinuosity=?, heading_change_deg_per_km=?, curvature=?,
            mean_inv_radius=?, max_inv_radius=?
        WHERE id=?
    `, lengthM, sinuosity, headPerKm, curvature, meanInvR, maxInvR, id)
	return err
}
