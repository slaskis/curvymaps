package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// tablesSQL creates tables. Indexes are deferred to indexesSQL so that any
// migration which adds columns runs before indexes that reference them.
const tablesSQL = `
CREATE TABLE IF NOT EXISTS ways (
    id                          INTEGER PRIMARY KEY,
    highway                     TEXT NOT NULL,
    surface                     TEXT,
    name                        TEXT,
    length_m                    REAL NOT NULL,
    sinuosity                   REAL NOT NULL,
    heading_change_deg_per_km   REAL NOT NULL,
    curvature                   REAL NOT NULL,
    mean_inv_radius             REAL NOT NULL DEFAULT 0,
    max_inv_radius              REAL NOT NULL DEFAULT 0,
    geometry                    BLOB NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS ways_rtree USING rtree(
    id, min_lon, max_lon, min_lat, max_lat
);

CREATE TABLE IF NOT EXISTS ways_staging (
    id          INTEGER PRIMARY KEY,
    highway     TEXT NOT NULL,
    surface     TEXT,
    name        TEXT,
    geometry    BLOB NOT NULL,
    min_lon     REAL NOT NULL,
    max_lon     REAL NOT NULL,
    min_lat     REAL NOT NULL,
    max_lat     REAL NOT NULL
);
`

const indexesSQL = `
CREATE INDEX IF NOT EXISTS idx_ways_curvature       ON ways(curvature);
CREATE INDEX IF NOT EXISTS idx_ways_sinuosity       ON ways(sinuosity);
CREATE INDEX IF NOT EXISTS idx_ways_heading_change  ON ways(heading_change_deg_per_km);
CREATE INDEX IF NOT EXISTS idx_ways_mean_inv_radius ON ways(mean_inv_radius);
CREATE INDEX IF NOT EXISTS idx_ways_max_inv_radius  ON ways(max_inv_radius);
CREATE INDEX IF NOT EXISTS idx_ways_highway         ON ways(highway);
`

// addedColumns lists columns introduced after the original schema. Each
// entry is appended to existing DBs via ALTER TABLE if absent.
var addedColumns = []struct{ name, ddl string }{
	{"mean_inv_radius", "REAL NOT NULL DEFAULT 0"},
	{"max_inv_radius", "REAL NOT NULL DEFAULT 0"},
}

func dsn(path string) string {
	return fmt.Sprintf(
		"file:%s?_pragma=journal_mode(wal)&_pragma=synchronous(normal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)",
		path,
	)
}

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// WAL handles writer/reader concurrency; busy_timeout serializes writers.
	// Keeping MaxOpenConns at 1 deadlocked when an iterator held the pool's
	// only connection while a callback tried to BeginTx.
	if _, err := db.Exec(tablesSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema init: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema migrate: %w", err)
	}
	if _, err := db.Exec(indexesSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("index init: %w", err)
	}
	return db, nil
}

// migrate brings an older `ways` table up to the current schema by adding any
// columns introduced after launch. Idempotent: a no-op on fresh DBs.
func migrate(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(ways)`)
	if err != nil {
		return err
	}
	have := map[string]struct{}{}
	for rows.Next() {
		var (
			cid              int
			name, ctype      string
			notnull, pk      int
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, c := range addedColumns {
		if _, ok := have[c.name]; ok {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE ways ADD COLUMN %s %s`, c.name, c.ddl)); err != nil {
			return fmt.Errorf("add column %s: %w", c.name, err)
		}
	}
	return nil
}

// OpenReadOnly opens an existing db for query-only work (the server).
func OpenReadOnly(path string) (*sql.DB, error) {
	d := fmt.Sprintf(
		"file:%s?mode=ro&_pragma=busy_timeout(5000)",
		path,
	)
	db, err := sql.Open("sqlite", d)
	if err != nil {
		return nil, err
	}
	// sql.Open is lazy. Force a real open now so a missing or unreadable
	// db fails at startup rather than on the first tile request.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

// DropStaging removes the staging table after a successful pipeline run.
func DropStaging(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS ways_staging`)
	return err
}
