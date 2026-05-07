package curvature

// Algorithm describes one selectable curviness metric. The ID is stable: it
// doubles as the MVT feature property name and the `?algo=` query parameter
// value, so the frontend, server and store all agree on a single string.
type Algorithm struct {
	ID    string
	Label string
	Unit  string
	// Get extracts this algorithm's score from a fully-populated Scores.
	Get func(Scores) float64
	// Column is the SQLite column that stores this score on the `ways` table.
	Column string
	// DefaultStops are 4 thresholds dividing the value range into 5 bands,
	// matching the 5-color paint ramp on the frontend.
	DefaultStops []float64
	// SliderMax is the upper bound for the UI slider; SliderStep is the step.
	SliderMin, SliderMax, SliderStep float64
	// Colors is the 5-color ramp, low → high.
	Colors []string
}

// Algorithms is the canonical list of selectable curviness metrics. Order
// matters: the first entry is the default both server-side and in the UI.
var Algorithms = []Algorithm{
	{
		ID:           "franco",
		Label:        "Franco weighted",
		Unit:         "weighted m",
		Column:       "curvature",
		Get:          func(s Scores) float64 { return s.Curvature },
		DefaultStops: []float64{50, 200, 500, 1000},
		SliderMin:    0, SliderMax: 2000, SliderStep: 10,
		Colors: defaultColors,
	},
	{
		ID:           "sinuosity",
		Label:        "Sinuosity (L/chord)",
		Unit:         "ratio",
		Column:       "sinuosity",
		Get:          func(s Scores) float64 { return s.Sinuosity },
		DefaultStops: []float64{1.05, 1.15, 1.30, 1.60},
		SliderMin:    1.0, SliderMax: 2.0, SliderStep: 0.01,
		Colors: defaultColors,
	},
	{
		ID:           "heading_change",
		Label:        "Heading change",
		Unit:         "deg/km",
		Column:       "heading_change_deg_per_km",
		Get:          func(s Scores) float64 { return s.HeadingChangeDegPerKm },
		DefaultStops: []float64{50, 150, 400, 800},
		SliderMin:    0, SliderMax: 1500, SliderStep: 5,
		Colors: defaultColors,
	},
	{
		ID:           "mean_inv_radius",
		Label:        "Mean 1/radius",
		Unit:         "1/m",
		Column:       "mean_inv_radius",
		Get:          func(s Scores) float64 { return s.MeanInvRadius },
		DefaultStops: []float64{0.002, 0.005, 0.012, 0.025},
		SliderMin:    0, SliderMax: 0.05, SliderStep: 0.0005,
		Colors: defaultColors,
	},
	{
		ID:           "max_inv_radius",
		Label:        "Max 1/radius (tightest turn)",
		Unit:         "1/m",
		Column:       "max_inv_radius",
		Get:          func(s Scores) float64 { return s.MaxInvRadius },
		DefaultStops: []float64{0.005, 0.012, 0.025, 0.05},
		SliderMin:    0, SliderMax: 0.1, SliderStep: 0.001,
		Colors: defaultColors,
	},
}

var defaultColors = []string{"#666", "#e6e600", "#ff8c00", "#e63946", "#8b0000"}

// ByID returns the Algorithm with the given ID, or false.
func ByID(id string) (Algorithm, bool) {
	for _, a := range Algorithms {
		if a.ID == id {
			return a, true
		}
	}
	return Algorithm{}, false
}

// Default returns the default algorithm (the first registered one).
func Default() Algorithm { return Algorithms[0] }
