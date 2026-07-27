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
	discovery  *upstreamDiscoveryRuntime
}

func newUpstreamRuntime(routes []UpstreamRouteConfig) *UpstreamRuntime {
	runtime := &UpstreamRuntime{trackers: make(map[string]*upstreamRouteTracker)}
	for _, route := range routes {
		if route.HTTP == nil {
			continue
		}
		runtime.ensureRoute(route.Name, "http")
		if route.HTTP.Discovery.Type != "" {
			runtime.ensureDiscovery(route.Name, route.HTTP.Discovery.Type)
		}
	}
	return runtime
}

func (r *UpstreamRuntime) ensureRoute(routeName, targetType string) *resilience.DependencyTracker {
	if r == nil || routeName == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return r.ensureRouteLocked(routeName, targetType).tracker
}

func (r *UpstreamRuntime) ensureRouteLocked(routeName, targetType string) *upstreamRouteTracker {
	if existing := r.trackers[routeName]; existing != nil {
		return existing
	}
	tracker := resilience.NewDependencyTracker(resilience.DependencyTrackerConfig{
		Name: "http_upstream:" + routeName,
	})
	entry := &upstreamRouteTracker{
		routeName:  routeName,
		targetType: targetType,
		tracker:    tracker,
	}
	r.trackers[routeName] = entry
	return entry
}

func (r *UpstreamRuntime) ensureDiscovery(routeName, discoveryType string) *upstreamDiscoveryRuntime {
	if r == nil || routeName == "" || discoveryType == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry := r.ensureRouteLocked(routeName, "http")
	if entry.discovery == nil {
		entry.discovery = newUpstreamDiscoveryRuntime(discoveryType, nil)
	}
	return entry.discovery
}

func (r *UpstreamRuntime) snapshot(routeName string) (upstreamRouteRuntimeSnapshot, bool) {
	if r == nil || routeName == "" {
		return upstreamRouteRuntimeSnapshot{}, false
	}

	r.mu.RLock()
	entry := r.trackers[routeName]
	if entry == nil || entry.tracker == nil {
		r.mu.RUnlock()
		return upstreamRouteRuntimeSnapshot{}, false
	}
	routeName = entry.routeName
	targetType := entry.targetType
	tracker := entry.tracker
	discovery := entry.discovery
	r.mu.RUnlock()

	snapshot := upstreamRouteRuntimeSnapshot{
		RouteName:  routeName,
		TargetType: targetType,
		Snapshot:   tracker.Snapshot(),
	}
	if discovery != nil {
		discoverySnapshot := discovery.snapshot()
		snapshot.Discovery = &discoverySnapshot
	}
	return snapshot, true
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
	Discovery  *upstreamDiscoveryRuntimeSnapshot
}
