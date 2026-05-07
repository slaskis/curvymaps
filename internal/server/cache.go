package server

import (
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"
)

// tileCache is a byte-budgeted LRU keyed by "z/x/y". Each entry's cost is its
// byte length. When the running total exceeds maxBytes we evict.
type tileCache struct {
	c        *lru.Cache[string, []byte]
	maxBytes int64
	bytes    atomic.Int64
}

func newTileCache(maxBytes int64) (*tileCache, error) {
	tc := &tileCache{maxBytes: maxBytes}
	c, err := lru.NewWithEvict[string, []byte](100_000, func(k string, v []byte) {
		tc.bytes.Add(-int64(len(v)))
	})
	if err != nil {
		return nil, err
	}
	tc.c = c
	return tc, nil
}

func (t *tileCache) get(key string) ([]byte, bool) {
	if t == nil {
		return nil, false
	}
	return t.c.Get(key)
}

func (t *tileCache) put(key string, v []byte) {
	if t == nil {
		return
	}
	t.c.Add(key, v)
	t.bytes.Add(int64(len(v)))
	for t.bytes.Load() > t.maxBytes {
		_, _, ok := t.c.RemoveOldest()
		if !ok {
			return
		}
	}
}
