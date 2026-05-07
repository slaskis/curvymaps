package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS ways (
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

CREATE INDEX IF NOT EXISTS idx_ways_curvature ON ways(curvature);
CREATE INDEX IF NOT EXISTS idx_ways_highway   ON ways(highway);

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
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema init: %w", err)
	}
	return db, nil
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
