package server

import (
	"sort"
	"sync"

	"github.com/qiuyier/Z-Courier/internal/resilience"
)

type UpstreamRuntime struct {
	mu       sync.RWMutex
	trackers map[string]*upstreamRouteTracker
}

type upstreamRouteTracker struct {
	routeName  string
	targetType string
	tracker    *resilience.DependencyTracker
}

func newUpstreamRuntime(routes []UpstreamRouteConfig) *UpstreamRuntime {
	runtime := &UpstreamRuntime{trackers: make(map[string]*upstreamRouteTracker)}
	for _, route := range routes {
		if route.HTTP == nil {
			continue
		}
		runtime.ensureRoute(route.Name, "http")
	}
	return runtime
}

func (r *UpstreamRuntime) ensureRoute(routeName, targetType string) *resilience.DependencyTracker {
	if r == nil || routeName == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing := r.trackers[routeName]; existing != nil {
		return existing.tracker
	}
	tracker := resilience.NewDependencyTracker(resilience.DependencyTrackerConfig{
		Name: "http_upstream:" + routeName,
	})
	r.trackers[routeName] = &upstreamRouteTracker{
		routeName:  routeName,
		targetType: targetType,
		tracker:    tracker,
	}
	return tracker
}

func (r *UpstreamRuntime) snapshot(routeName string) (upstreamRouteRuntimeSnapshot, bool) {
	if r == nil || routeName == "" {
		return upstreamRouteRuntimeSnapshot{}, false
	}

	r.mu.RLock()
	entry := r.trackers[routeName]
	r.mu.RUnlock()
	if entry == nil || entry.tracker == nil {
		return upstreamRouteRuntimeSnapshot{}, false
	}
	return upstreamRouteRuntimeSnapshot{
		RouteName:  entry.routeName,
		TargetType: entry.targetType,
		Snapshot:   entry.tracker.Snapshot(),
	}, true
}

func (r *UpstreamRuntime) snapshots() []upstreamRouteRuntimeSnapshot {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	names := make([]string, 0, len(r.trackers))
	for name := range r.trackers {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)

	out := make([]upstreamRouteRuntimeSnapshot, 0, len(names))
	for _, name := range names {
		snapshot, ok := r.snapshot(name)
		if ok {
			out = append(out, snapshot)
		}
	}
	return out
}

type upstreamRouteRuntimeSnapshot struct {
	RouteName  string
	TargetType string
	Snapshot   resilience.DependencySnapshot
}
