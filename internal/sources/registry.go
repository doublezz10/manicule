package sources

// Registry wires concrete adapters to the app with fleet-selected endpoints
// and user credentials applied.

import (
	"net/http"
	"sort"
	"sync"
)

type Registry struct {
	mu      sync.RWMutex
	client  *http.Client
	sources map[string]Source
	order   []string // stable display order
}

func NewRegistry(client *http.Client) *Registry {
	if client == nil {
		client = NewHTTPClient()
	}
	g := NewGutendex(client)
	se := NewStandardEbooks(client)
	zl := NewZLibrary(client)
	r := &Registry{
		client: client,
		sources: map[string]Source{
			g.ID():  g,
			se.ID(): se,
			zl.ID(): zl,
		},
		order: []string{"gutendex", "standardebooks", "z-library"},
	}
	return r
}

func (r *Registry) All() []Source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Source, 0, len(r.order))
	for _, id := range r.order {
		if s, ok := r.sources[id]; ok {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tier() < out[j].Tier() })
	return out
}

func (r *Registry) Get(id string) (Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sources[id]
	return s, ok
}

func (r *Registry) ApplyBaseURL(id, base string) {
	if s, ok := r.Get(id); ok {
		s.SetBaseURL(base)
	}
}

func (r *Registry) ApplyCredentials(id string, c Credentials) {
	if s, ok := r.Get(id); ok {
		s.SetCredentials(c)
	}
}
