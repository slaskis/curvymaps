package server

import (
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/slaskis/curvymaps/internal/curvature"
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
	r.Get("/algorithms", handleAlgorithms)
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

// algorithmDTO is the JSON shape returned by /algorithms. It mirrors
// curvature.Algorithm minus the unmarshalable Get func.
type algorithmDTO struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	Unit         string    `json:"unit"`
	DefaultStops []float64 `json:"default_stops"`
	SliderMin    float64   `json:"slider_min"`
	SliderMax    float64   `json:"slider_max"`
	SliderStep   float64   `json:"slider_step"`
	Colors       []string  `json:"colors"`
}

func handleAlgorithms(w http.ResponseWriter, r *http.Request) {
	out := make([]algorithmDTO, 0, len(curvature.Algorithms))
	for _, a := range curvature.Algorithms {
		out = append(out, algorithmDTO{
			ID:           a.ID,
			Label:        a.Label,
			Unit:         a.Unit,
			DefaultStops: a.DefaultStops,
			SliderMin:    a.SliderMin,
			SliderMax:    a.SliderMax,
			SliderStep:   a.SliderStep,
			Colors:       a.Colors,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(out)
}
