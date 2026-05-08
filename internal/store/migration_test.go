package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// preMigrationSchema is the original `ways` schema before mean/max 1/r columns
// were added. The migration in Open() must add the missing columns.
const preMigrationSchema = `
CREATE TABLE ways (
    id                          INTEGER PRIMARY KEY,
    highway                     TEXT NOT NULL,
    surface                     TEXT,
    name                        TEXT,
    length_m                    REAL NOT NULL,
    sinuosity                   REAL NOT NULL,
    heading_change_deg_per_km   REAL NOT NULL,
    curvature                   REAL NOT NULL,
    geometry                    BLOB NOT NULL
);
CREATE VIRTUAL TABLE ways_rtree USING rtree(
    id, min_lon, max_lon, min_lat, max_lat
);
`

func TestMigrationAddsNewColumns(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "old.db")
	// Build a DB at the pre-migration schema using a raw connection.
	raw, err := sql.Open("sqlite", "file:"+tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(preMigrationSchema); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO ways
        (id, highway, length_m, sinuosity, heading_change_deg_per_km, curvature, geometry)
        VALUES (1, 'tertiary', 100, 1.2, 50, 80, x'00')`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()

	// Open via store.Open: migration should add mean_inv_radius and max_inv_radius.
	db, err := Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cols, err := tableColumns(db, "ways")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mean_inv_radius", "max_inv_radius"} {
		if _, ok := cols[want]; !ok {
			t.Errorf("missing column %q after migration; have %v", want, cols)
		}
	}

	// Existing row's defaults should be 0.
	var mean, mx float64
	if err := db.QueryRowContext(context.Background(),
		`SELECT mean_inv_radius, max_inv_radius FROM ways WHERE id=1`).Scan(&mean, &mx); err != nil {
		t.Fatal(err)
	}
	if mean != 0 || mx != 0 {
		t.Errorf("default values: mean=%v max=%v, want 0/0", mean, mx)
	}

	// Idempotent: a second Open must not error.
	db.Close()
	db2, err := Open(tmp)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	db2.Close()
}

func tableColumns(db *sql.DB, table string) (map[string]struct{}, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var (
			cid          int
			name, ctype  string
			notnull, pk  int
			dflt         sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}
