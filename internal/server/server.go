package server

import (
	"database/sql"
	"embed"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Static frontend, embedded at build time.
//
//go:embed static/*
var staticFS embed.FS

type Opts struct {
	NoCache bool
}

type Server struct {
	db    *sql.DB
	cache *tileCache
	mux   http.Handler
}

func New(db *sql.DB, opts Opts) (*Server, error) {
	s := &Server{db: db}
	if !opts.NoCache {
		c, err := newTileCache(256 * 1024 * 1024) // 256MB
		if err != nil {
			return nil, err
		}
		s.cache = c
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)

	r.Get("/tiles/{z}/{x}/{y}.mvt", s.handleTile)
	r.Get("/api/ways", s.handleGeoJSON)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Static frontend at /
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	r.Handle("/*", http.FileServer(http.FS(sub)))

	s.mux = r
	return s, nil
}

func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.mux}
	return srv.ListenAndServe()
}

// Handler exposes the http.Handler for testing.
func (s *Server) Handler() http.Handler { return s.mux }
