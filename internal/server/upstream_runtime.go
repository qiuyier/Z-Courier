package server

import (
	"sort"
	"sync"

	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/resilience"
)

type UpstreamRuntime struct {
	mu       sync.RWMutex
	trackers map[string]*upstreamRouteTracker

	metricsMu     sync.RWMutex
	metricsActive bool
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

func (r *UpstreamRuntime) activateMetrics() {
	if r == nil {
		return
	}

	r.metricsMu.Lock()
	defer r.metricsMu.Unlock()
	r.metricsActive = true
	for _, snapshot := range r.snapshots() {
		metrics.SetUpstreamRouteDegraded(
			snapshot.RouteName,
			snapshot.TargetType,
			snapshot.Snapshot.Status != resilience.DependencyStatusHealthy,
		)
		if snapshot.Discovery != nil {
			metrics.SetUpstreamDiscoveryResolvedEndpoints(
				snapshot.RouteName,
				snapshot.Discovery.Type,
				snapshot.Discovery.ResolvedEndpoints,
			)
			metrics.SetUpstreamEndpointUnhealthy(
				snapshot.RouteName,
				snapshot.Discovery.Type,
				snapshot.Discovery.UnhealthyEndpoints,
			)
		}
	}
}

func (r *UpstreamRuntime) deactivateMetrics() {
	if r == nil {
		return
	}
	r.metricsMu.Lock()
	r.metricsActive = false
	r.metricsMu.Unlock()
}

func (r *UpstreamRuntime) recordActiveMetrics(record func()) {
	if record == nil {
		return
	}
	if r == nil {
		record()
		return
	}

	r.metricsMu.RLock()
	defer r.metricsMu.RUnlock()
	if r.metricsActive {
		record()
	}
}

type upstreamRouteRuntimeSnapshot struct {
	RouteName  string
	TargetType string
	Snapshot   resilience.DependencySnapshot
	Discovery  *upstreamDiscoveryRuntimeSnapshot
}
