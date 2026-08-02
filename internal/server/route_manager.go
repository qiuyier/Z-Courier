package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
	"go.uber.org/zap"
)

var (
	errRouteManagerClosed      = errors.New("route manager is closed")
	errRouteReloadBusy         = errors.New("route reload is busy")
	errRouteGenerationConflict = errors.New("route generation conflict")
	errRouteCandidateInvalid   = errors.New("route candidate is invalid")
)

const (
	routeGenerationCandidate uint32 = iota
	routeGenerationActive
	routeGenerationRetiring
	routeGenerationClosed
)

type routeGenerationSnapshot struct {
	Number      uint64
	Fingerprint string
	ActivatedAt time.Time
	RetiringAt  time.Time
	RouteCount  int
	InFlight    int64
	State       string
}

type routeManagerSnapshot struct {
	Closed   bool
	Active   *routeGenerationSnapshot
	Retiring *routeGenerationSnapshot
}

type routeTargetMetric struct {
	routeName  string
	targetType string
}

type routeDiscoveryMetric struct {
	routeName     string
	discoveryType string
}

type routeMutableMetrics struct {
	routes      map[routeTargetMetric]struct{}
	discoveries map[routeDiscoveryMetric]struct{}
}

type routeGeneration struct {
	number      uint64
	fingerprint string
	activatedAt time.Time
	retiringAt  time.Time
	routes      []UpstreamRouteConfig
	engine      *router.Engine
	runtime     *UpstreamRuntime
	metrics     routeMutableMetrics
	cleanup     routeMutableMetrics

	state          atomic.Uint32
	inFlight       atomic.Int64
	closeScheduled atomic.Bool
	closeOnce      sync.Once
	closeDone      chan struct{}
	closeErr       error
}

type routeLease struct {
	generation *routeGeneration
	released   atomic.Bool
}

type routeGenerationBuilder func(context.Context) (*routeGeneration, error)

type routeManager struct {
	mu           sync.RWMutex
	active       *routeGeneration
	retiring     *routeGeneration
	nextNumber   uint64
	closed       bool
	reloadDone   chan struct{}
	reloadGate   chan struct{}
	drainTimeout time.Duration
	logger       *zap.Logger
	now          func() time.Time

	shutdownOnce        sync.Once
	shutdownGenerations []*routeGeneration
	shutdownReloadDone  <-chan struct{}
}

func buildRouteGeneration(routes []UpstreamRouteConfig) (*routeGeneration, error) {
	routes = cloneUpstreamRoutes(routes)
	runtime := newUpstreamRuntime(routes)
	engine, err := newUpstreamEngine(Config{
		UpstreamRoutes:  routes,
		UpstreamRuntime: runtime,
	})
	if err != nil {
		return nil, err
	}
	fingerprint, err := upstreamRoutesFingerprint(routes)
	if err != nil {
		if engine != nil {
			_ = engine.Close()
		}
		return nil, err
	}
	return makeRouteGeneration(routes, engine, runtime, fingerprint), nil
}

func makeRouteGeneration(
	routes []UpstreamRouteConfig,
	engine *router.Engine,
	runtime *UpstreamRuntime,
	fingerprint string,
) *routeGeneration {
	return &routeGeneration{
		fingerprint: fingerprint,
		routes:      cloneUpstreamRoutes(routes),
		engine:      engine,
		runtime:     runtime,
		metrics:     mutableMetricsForRoutes(routes),
		closeDone:   make(chan struct{}),
	}
}

func newRouteManager(
	initial *routeGeneration,
	drainTimeout time.Duration,
	logger *zap.Logger,
) (*routeManager, error) {
	return newRouteManagerWithClock(initial, drainTimeout, logger, time.Now)
}

func newRouteManagerWithClock(
	initial *routeGeneration,
	drainTimeout time.Duration,
	logger *zap.Logger,
	now func() time.Time,
) (*routeManager, error) {
	if initial == nil || initial.fingerprint == "" || initial.state.Load() != routeGenerationCandidate {
		if initial != nil {
			initial.discard()
		}
		return nil, errRouteCandidateInvalid
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	if now == nil {
		now = time.Now
	}
	if drainTimeout < 0 {
		drainTimeout = 0
	}

	manager := &routeManager{
		active:       initial,
		nextNumber:   1,
		reloadGate:   make(chan struct{}, 1),
		drainTimeout: drainTimeout,
		logger:       logger,
		now:          now,
	}
	manager.reloadGate <- struct{}{}
	if !initial.prepareActive(1, now()) {
		initial.discard()
		return nil, errRouteCandidateInvalid
	}
	initial.runtime.activateMetrics()
	metrics.SetRouteGeneration(1)
	metrics.SetRouteRetiringGenerations(0)
	metrics.SetRouteRetirement(time.Time{}, drainTimeout)
	return manager, nil
}

func (m *routeManager) Acquire() (*routeLease, error) {
	if m == nil {
		return nil, errRouteManagerClosed
	}

	m.mu.RLock()
	if m.closed || m.active == nil {
		m.mu.RUnlock()
		return nil, errRouteManagerClosed
	}
	generation := m.active
	generation.inFlight.Add(1)
	m.mu.RUnlock()

	return &routeLease{generation: generation}, nil
}

func (l *routeLease) ResolveRoute(msgID uint32) (string, bool) {
	if l == nil || l.generation == nil || l.released.Load() || l.generation.engine == nil {
		return "", false
	}
	return l.generation.engine.ResolveRoute(msgID)
}

func (l *routeLease) Forward(ctx context.Context, packet *protocol.Packet) (*router.ForwardResult, error) {
	if l == nil || l.generation == nil || l.released.Load() || l.generation.engine == nil {
		return nil, router.ErrRouteNotFound
	}
	return l.generation.engine.Forward(ctx, packet)
}

func (l *routeLease) Generation() uint64 {
	if l == nil || l.generation == nil {
		return 0
	}
	return l.generation.number
}

func (l *routeLease) Release() {
	if l == nil || l.generation == nil || l.released.Swap(true) {
		return
	}
	remaining := l.generation.inFlight.Add(-1)
	if remaining == 0 {
		l.generation.closeIfDrained()
	}
}

func (m *routeManager) Reload(
	ctx context.Context,
	expectedGeneration uint64,
	build routeGenerationBuilder,
) (routeGenerationSnapshot, error) {
	if m == nil {
		return routeGenerationSnapshot{}, errRouteManagerClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if build == nil {
		return m.activeSnapshot(), errRouteCandidateInvalid
	}

	select {
	case <-ctx.Done():
		return m.activeSnapshot(), ctx.Err()
	case <-m.reloadGate:
	}
	defer func() { m.reloadGate <- struct{}{} }()

	reloadDone, snapshot, err := m.beginReload(expectedGeneration)
	if err != nil {
		return snapshot, err
	}
	defer m.finishReload(reloadDone)

	candidate, buildErr := build(ctx)
	if buildErr != nil {
		if candidate != nil {
			candidate.discard()
		}
		return m.activeSnapshot(), fmt.Errorf("build route generation: %w", buildErr)
	}
	if candidate == nil {
		return m.activeSnapshot(), errRouteCandidateInvalid
	}
	if err := ctx.Err(); err != nil {
		candidate.discard()
		return m.activeSnapshot(), err
	}

	snapshot, err = m.activateCandidate(expectedGeneration, candidate)
	if err != nil {
		candidate.discard()
		return snapshot, err
	}
	return snapshot, nil
}

func (m *routeManager) DryRun(
	ctx context.Context,
	expectedGeneration uint64,
	build routeGenerationBuilder,
) (routeGenerationSnapshot, error) {
	if m == nil {
		return routeGenerationSnapshot{}, errRouteManagerClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if build == nil {
		return routeGenerationSnapshot{}, errRouteCandidateInvalid
	}

	select {
	case <-ctx.Done():
		return routeGenerationSnapshot{}, ctx.Err()
	case <-m.reloadGate:
	}
	defer func() { m.reloadGate <- struct{}{} }()

	reloadDone, _, err := m.beginReload(expectedGeneration)
	if err != nil {
		return routeGenerationSnapshot{}, err
	}
	defer m.finishReload(reloadDone)

	candidate, buildErr := build(ctx)
	if buildErr != nil {
		if candidate != nil {
			candidate.discard()
		}
		return routeGenerationSnapshot{}, fmt.Errorf("build route generation: %w", buildErr)
	}
	if candidate == nil || candidate.fingerprint == "" || candidate.state.Load() != routeGenerationCandidate {
		if candidate != nil {
			candidate.discard()
		}
		return routeGenerationSnapshot{}, errRouteCandidateInvalid
	}
	if err := ctx.Err(); err != nil {
		candidate.discard()
		return routeGenerationSnapshot{}, err
	}

	snapshot := snapshotRouteGeneration(candidate)
	candidate.discard()
	return snapshot, nil
}

func (m *routeManager) beginReload(expectedGeneration uint64) (chan struct{}, routeGenerationSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearRetiringLocked()

	snapshot := snapshotRouteGeneration(m.active)
	if m.closed || m.active == nil {
		return nil, snapshot, errRouteManagerClosed
	}
	if m.retiring != nil {
		return nil, snapshot, errRouteReloadBusy
	}
	if expectedGeneration != 0 && m.active.number != expectedGeneration {
		return nil, snapshot, fmt.Errorf(
			"%w: active generation is %d, expected %d",
			errRouteGenerationConflict,
			m.active.number,
			expectedGeneration,
		)
	}

	done := make(chan struct{})
	m.reloadDone = done
	return done, snapshot, nil
}

func (m *routeManager) finishReload(done chan struct{}) {
	m.mu.Lock()
	if done != nil && m.reloadDone == done {
		m.reloadDone = nil
		close(done)
	}
	m.mu.Unlock()
}

func (m *routeManager) activateCandidate(
	expectedGeneration uint64,
	candidate *routeGeneration,
) (routeGenerationSnapshot, error) {
	m.mu.Lock()
	m.clearRetiringLocked()
	if m.closed || m.active == nil {
		snapshot := snapshotRouteGeneration(m.active)
		m.mu.Unlock()
		return snapshot, errRouteManagerClosed
	}
	if m.retiring != nil {
		snapshot := snapshotRouteGeneration(m.active)
		m.mu.Unlock()
		return snapshot, errRouteReloadBusy
	}
	if expectedGeneration != 0 && m.active.number != expectedGeneration {
		snapshot := snapshotRouteGeneration(m.active)
		m.mu.Unlock()
		return snapshot, fmt.Errorf(
			"%w: active generation is %d, expected %d",
			errRouteGenerationConflict,
			m.active.number,
			expectedGeneration,
		)
	}
	if candidate == nil || candidate.fingerprint == "" || candidate.state.Load() != routeGenerationCandidate {
		snapshot := snapshotRouteGeneration(m.active)
		m.mu.Unlock()
		return snapshot, errRouteCandidateInvalid
	}

	old := m.active
	nextNumber := m.nextNumber + 1
	if !candidate.prepareActive(nextNumber, m.now()) {
		snapshot := snapshotRouteGeneration(m.active)
		m.mu.Unlock()
		return snapshot, errRouteCandidateInvalid
	}

	old.runtime.deactivateMetrics()
	candidate.runtime.activateMetrics()
	old.cleanup = old.metrics.difference(candidate.metrics)
	old.retiringAt = m.now().UTC()
	old.state.Store(routeGenerationRetiring)
	m.active = candidate
	m.retiring = old
	m.nextNumber = nextNumber
	metrics.SetRouteGeneration(nextNumber)
	metrics.SetRouteRetiringGenerations(1)
	metrics.SetRouteRetirement(old.retiringAt, m.drainTimeout)
	snapshot := snapshotRouteGeneration(candidate)
	m.mu.Unlock()

	old.closeIfDrained()
	m.watchDrain(old)
	return snapshot, nil
}

func (m *routeManager) Snapshot() routeManagerSnapshot {
	if m == nil {
		return routeManagerSnapshot{Closed: true}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearRetiringLocked()
	out := routeManagerSnapshot{Closed: m.closed}
	if m.active != nil {
		active := snapshotRouteGeneration(m.active)
		out.Active = &active
	}
	if m.retiring != nil {
		retiring := snapshotRouteGeneration(m.retiring)
		out.Retiring = &retiring
	}
	return out
}

func (m *routeManager) activeSnapshot() routeGenerationSnapshot {
	if m == nil {
		return routeGenerationSnapshot{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return snapshotRouteGeneration(m.active)
}

func (m *routeManager) activeRuntime() *UpstreamRuntime {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return nil
	}
	return m.active.runtime
}

func (m *routeManager) activeRoutes() []UpstreamRouteConfig {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return nil
	}
	return cloneUpstreamRoutes(m.active.routes)
}

func (m *routeManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.shutdownOnce.Do(m.beginShutdown)

	var joined error
	if m.shutdownReloadDone != nil {
		select {
		case <-m.shutdownReloadDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, generation := range m.shutdownGenerations {
		select {
		case <-generation.closeDone:
			joined = errors.Join(joined, generation.closeErr)
		case <-ctx.Done():
			return errors.Join(joined, ctx.Err())
		}
	}
	return joined
}

func (m *routeManager) Close() error {
	return m.Shutdown(context.Background())
}

func (m *routeManager) beginShutdown() {
	m.mu.Lock()
	m.closed = true
	metrics.SetRouteGeneration(0)
	metrics.SetRouteRetiringGenerations(0)
	metrics.ClearRouteRetirement()
	m.clearRetiringLocked()
	m.shutdownReloadDone = m.reloadDone

	seen := make(map[*routeGeneration]struct{}, 2)
	add := func(generation *routeGeneration) {
		if generation == nil {
			return
		}
		if _, exists := seen[generation]; exists {
			return
		}
		seen[generation] = struct{}{}
		m.shutdownGenerations = append(m.shutdownGenerations, generation)
	}
	add(m.retiring)
	add(m.active)

	if m.active != nil {
		m.active.runtime.deactivateMetrics()
		m.active.cleanup = m.active.metrics
		m.active.state.Store(routeGenerationRetiring)
	}
	m.active = nil
	m.mu.Unlock()

	for _, generation := range m.shutdownGenerations {
		generation.closeIfDrained()
	}
}

func (m *routeManager) clearRetiringLocked() {
	if m.retiring != nil && m.retiring.closed() {
		m.retiring = nil
	}
}

func (m *routeManager) watchDrain(generation *routeGeneration) {
	if m == nil || generation == nil || m.drainTimeout <= 0 {
		return
	}
	timeout := m.drainTimeout
	logger := m.logger
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-generation.closeDone:
			return
		case <-timer.C:
			logger.Warn(
				"upstream route generation is still draining",
				zap.Uint64("generation", generation.number),
				zap.Int64("in_flight", generation.inFlight.Load()),
				zap.Duration("drain_timeout", timeout),
			)
		}
	}()
}

func (g *routeGeneration) prepareActive(number uint64, activatedAt time.Time) bool {
	if g == nil || number == 0 || g.fingerprint == "" {
		return false
	}
	g.number = number
	g.activatedAt = activatedAt.UTC()
	return g.state.CompareAndSwap(routeGenerationCandidate, routeGenerationActive)
}

func (g *routeGeneration) closeIfDrained() {
	if g == nil || g.inFlight.Load() != 0 || g.state.Load() != routeGenerationRetiring {
		return
	}
	if g.closeScheduled.CompareAndSwap(false, true) {
		go g.closeNow()
	}
}

func (g *routeGeneration) discard() {
	if g == nil || g.state.Load() != routeGenerationCandidate {
		return
	}
	if g.closeScheduled.CompareAndSwap(false, true) {
		g.closeNow()
	}
}

func (g *routeGeneration) closeNow() {
	if g == nil {
		return
	}
	g.closeOnce.Do(func() {
		g.runtime.deactivateMetrics()
		if g.engine != nil {
			g.closeErr = g.engine.Close()
		}
		g.cleanup.delete()
		g.state.Store(routeGenerationClosed)
		if !g.retiringAt.IsZero() {
			metrics.ObserveRouteRetirementDuration(time.Since(g.retiringAt))
			metrics.SetRouteRetiringGenerations(0)
			metrics.ClearRouteRetirement()
		}
		close(g.closeDone)
	})
}

func (g *routeGeneration) closed() bool {
	if g == nil {
		return true
	}
	select {
	case <-g.closeDone:
		return true
	default:
		return false
	}
}

func snapshotRouteGeneration(generation *routeGeneration) routeGenerationSnapshot {
	if generation == nil {
		return routeGenerationSnapshot{}
	}
	return routeGenerationSnapshot{
		Number:      generation.number,
		Fingerprint: generation.fingerprint,
		ActivatedAt: generation.activatedAt,
		RetiringAt:  generation.retiringAt,
		RouteCount:  len(generation.routes),
		InFlight:    generation.inFlight.Load(),
		State:       routeGenerationStateName(generation.state.Load()),
	}
}

func routeGenerationStateName(state uint32) string {
	switch state {
	case routeGenerationCandidate:
		return "candidate"
	case routeGenerationActive:
		return "active"
	case routeGenerationRetiring:
		return "retiring"
	case routeGenerationClosed:
		return "closed"
	default:
		return "unknown"
	}
}

func cloneUpstreamRoutes(routes []UpstreamRouteConfig) []UpstreamRouteConfig {
	cloned := make([]UpstreamRouteConfig, len(routes))
	for index, route := range routes {
		cloned[index] = route
		if route.HTTP != nil {
			httpConfig := *route.HTTP
			httpConfig.Discovery.Endpoints = append([]string(nil), route.HTTP.Discovery.Endpoints...)
			cloned[index].HTTP = &httpConfig
		}
		if route.NSQ != nil {
			nsqConfig := *route.NSQ
			nsqConfig.Addresses = append([]string(nil), route.NSQ.Addresses...)
			cloned[index].NSQ = &nsqConfig
		}
	}
	return cloned
}

func upstreamRoutesFingerprint(routes []UpstreamRouteConfig) (string, error) {
	sanitized := cloneUpstreamRoutes(routes)
	for index := range sanitized {
		if sanitized[index].HTTP != nil {
			if sanitized[index].HTTP.Token != "" {
				sanitized[index].HTTP.Token = "configured"
			}
			sanitized[index].HTTP.Discovery.Lookup = nil
		}
		if sanitized[index].NSQ != nil {
			if sanitized[index].NSQ.AuthSecret != "" {
				sanitized[index].NSQ.AuthSecret = "configured"
			}
		}
	}
	encoded, err := sonic.Marshal(sanitized)
	if err != nil {
		return "", fmt.Errorf("fingerprint upstream routes: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func mutableMetricsForRoutes(routes []UpstreamRouteConfig) routeMutableMetrics {
	out := routeMutableMetrics{
		routes:      make(map[routeTargetMetric]struct{}),
		discoveries: make(map[routeDiscoveryMetric]struct{}),
	}
	for _, route := range routes {
		targetType := routeTargetType(route)
		if route.Name != "" && targetType != "unknown" {
			out.routes[routeTargetMetric{routeName: route.Name, targetType: targetType}] = struct{}{}
		}
		if route.HTTP != nil && route.HTTP.Discovery.Type != "" {
			out.discoveries[routeDiscoveryMetric{
				routeName:     route.Name,
				discoveryType: route.HTTP.Discovery.Type,
			}] = struct{}{}
		}
	}
	return out
}

func (m routeMutableMetrics) difference(next routeMutableMetrics) routeMutableMetrics {
	out := routeMutableMetrics{
		routes:      make(map[routeTargetMetric]struct{}),
		discoveries: make(map[routeDiscoveryMetric]struct{}),
	}
	for labels := range m.routes {
		if _, preserved := next.routes[labels]; !preserved {
			out.routes[labels] = struct{}{}
		}
	}
	for labels := range m.discoveries {
		if _, preserved := next.discoveries[labels]; !preserved {
			out.discoveries[labels] = struct{}{}
		}
	}
	return out
}

func (m routeMutableMetrics) delete() {
	for labels := range m.routes {
		metrics.DeleteUpstreamRouteMutableMetrics(labels.routeName, labels.targetType)
	}
	for labels := range m.discoveries {
		metrics.DeleteUpstreamDiscoveryMutableMetrics(labels.routeName, labels.discoveryType)
	}
}
