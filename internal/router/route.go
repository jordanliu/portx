package router

import (
	"net/url"
	"sync"
	"time"
)

type OriginTLS struct {
	InsecureSkipVerify bool
}

type Route struct {
	ID         string
	Hostname   string
	PathPrefix string
	Target     *url.URL
	HostHeader string
	TLS        OriginTLS
	CreatedAt  time.Time
}

type Registry struct {
	mu     sync.RWMutex
	routes map[string]Route
}

func NewRegistry() *Registry {
	return &Registry{routes: make(map[string]Route)}
}

func (r *Registry) Add(route Route) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[route.ID] = cloneRoute(route)
	return nil
}

func cloneRoute(route Route) Route {
	if route.Target != nil {
		u := *route.Target
		route.Target = &u
	}
	return route
}

func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.routes, id)
	return nil
}

func (r *Registry) Get(id string) (Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.routes[id]
	if !ok {
		return Route{}, false
	}
	return cloneRoute(rt), true
}

func (r *Registry) List() []Route {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Route, 0, len(r.routes))
	for _, rt := range r.routes {
		out = append(out, cloneRoute(rt))
	}
	return out
}

func (r *Registry) Match(host, path string) (Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var best Route
	bestLen := -1
	for _, rt := range r.routes {
		if rt.Hostname != host {
			continue
		}
		prefix := rt.PathPrefix
		if prefix == "" {
			prefix = "/"
		}
		if pathMatches(path, prefix) && len(prefix) > bestLen {
			best = rt
			bestLen = len(prefix)
		}
	}
	if bestLen < 0 {
		return Route{}, false
	}
	return cloneRoute(best), true
}

func pathMatches(path, prefix string) bool {
	if prefix == "/" || path == prefix {
		return true
	}
	hasBoundary := len(path) > len(prefix) &&
		path[:len(prefix)] == prefix &&
		path[len(prefix)] == '/'
	return hasBoundary
}
