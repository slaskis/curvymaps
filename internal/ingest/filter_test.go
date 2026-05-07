package ingest

import (
	"testing"

	"github.com/paulmach/osm"
)

func tags(kv ...string) osm.Tags {
	if len(kv)%2 != 0 {
		panic("tags: odd args")
	}
	t := make(osm.Tags, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		t = append(t, osm.Tag{Key: kv[i], Value: kv[i+1]})
	}
	return t
}

func TestAccept(t *testing.T) {
	cases := []struct {
		name string
		tags osm.Tags
		opts FilterOpts
		want bool
	}{
		{"primary asphalt", tags("highway", "primary", "surface", "asphalt"), FilterOpts{}, true},
		{"residential", tags("highway", "residential"), FilterOpts{}, true},
		{"motorway dropped", tags("highway", "motorway"), FilterOpts{}, false},
		{"trunk dropped", tags("highway", "trunk"), FilterOpts{}, false},
		{"footway dropped", tags("highway", "footway"), FilterOpts{}, false},
		{"missing highway dropped", tags("name", "Foo"), FilterOpts{}, false},
		{"area=yes dropped", tags("highway", "primary", "area", "yes"), FilterOpts{}, false},
		{"access=no dropped", tags("highway", "primary", "access", "no"), FilterOpts{}, false},
		{"access=private dropped", tags("highway", "primary", "access", "private"), FilterOpts{}, false},
		{"gravel dropped by default", tags("highway", "tertiary", "surface", "gravel"), FilterOpts{}, false},
		{"gravel kept with unpaved", tags("highway", "tertiary", "surface", "gravel"), FilterOpts{IncludeUnpaved: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Accept(tc.tags, tc.opts); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
