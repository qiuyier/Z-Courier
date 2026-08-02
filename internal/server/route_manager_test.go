package server

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRouteManagerPinsRequestsAcrossActivation(t *testing.T) {
	oldForwarder := newControlledGenerationForwarder(true)
	manager := mustRouteManager(t, controlledRouteGeneration("orders-v1", oldForwarder, "fingerprint-v1"), 0, zap.NewNop())

	oldLease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire() old error = %v", err)
	}
	oldResult := make(chan *router.ForwardResult, 1)
	oldErr := make(chan error, 1)
	go func() {
		result, forwardErr := oldLease.Forward(context.Background(), protocol.NewPacket(1001, []byte("old")))
		oldResult <- result
		oldErr <- forwardErr
	}()
	<-oldForwarder.entered

	newForwarder := newControlledGenerationForwarder(false)
	snapshot, err := manager.Reload(context.Background(), 1, func(context.Context) (*routeGeneration, error) {
		return controlledRouteGeneration("orders-v2", newForwarder, "fingerprint-v2"), nil
	})
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if snapshot.Number != 2 || snapshot.Fingerprint != "fingerprint-v2" || snapshot.State != "active" {
		t.Fatalf("Reload() snapshot = %+v, want active generation 2", snapshot)
	}
	if oldForwarder.closeCalls.Load() != 0 {
		t.Fatal("old forwarder closed while its request still held a lease")
	}

	newLease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire() new error = %v", err)
	}
	if newLease.Generation() != 2 {
		t.Fatalf("new lease generation = %d, want 2", newLease.Generation())
	}
	if routeName, found := newLease.ResolveRoute(1001); !found || routeName != "orders-v2" {
		t.Fatalf("new lease route = %q/%v, want orders-v2/true", routeName, found)
	}
	if _, err := newLease.Forward(context.Background(), protocol.NewPacket(1001, []byte("new"))); err != nil {
		t.Fatalf("new lease Forward() error = %v", err)
	}
	newLease.Release()
	if newForwarder.calls.Load() != 1 || oldForwarder.calls.Load() != 1 {
		t.Fatalf("forward calls old=%d new=%d, want 1/1", oldForwarder.calls.Load(), newForwarder.calls.Load())
	}

	close(oldForwarder.release)
	if err := <-oldErr; err != nil {
		t.Fatalf("old lease Forward() error = %v", err)
	}
	if result := <-oldResult; result == nil || result.RouteName != "orders-v1" {
		t.Fatalf("old result = %+v, want orders-v1", result)
	}
	oldLease.Release()
	waitForGenerationClose(t, oldForwarder)
	if oldForwarder.closeCalls.Load() != 1 {
		t.Fatalf("old forwarder close calls = %d, want 1", oldForwarder.closeCalls.Load())
	}
}

func TestRouteManagerRejectsReloadWhileGenerationRetires(t *testing.T) {
	oldForwarder := newControlledGenerationForwarder(false)
	manager := mustRouteManager(t, controlledRouteGeneration("route-v1", oldForwarder, "fingerprint-v1"), 0, zap.NewNop())

	lease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	newForwarder := newControlledGenerationForwarder(false)
	if _, err := manager.Reload(context.Background(), 1, func(context.Context) (*routeGeneration, error) {
		return controlledRouteGeneration("route-v2", newForwarder, "fingerprint-v2"), nil
	}); err != nil {
		t.Fatalf("first Reload() error = %v", err)
	}

	var built atomic.Bool
	_, err = manager.Reload(context.Background(), 2, func(context.Context) (*routeGeneration, error) {
		built.Store(true)
		return controlledRouteGeneration("route-v3", newControlledGenerationForwarder(false), "fingerprint-v3"), nil
	})
	if !errors.Is(err, errRouteReloadBusy) {
		t.Fatalf("second Reload() error = %v, want %v", err, errRouteReloadBusy)
	}
	if built.Load() {
		t.Fatal("busy reload constructed another candidate")
	}

	lease.Release()
	waitForGenerationClose(t, oldForwarder)
	waitForRetirement(t, manager)
	thirdForwarder := newControlledGenerationForwarder(false)
	snapshot, err := manager.Reload(context.Background(), 2, func(context.Context) (*routeGeneration, error) {
		return controlledRouteGeneration("route-v3", thirdForwarder, "fingerprint-v3"), nil
	})
	if err != nil {
		t.Fatalf("third Reload() error = %v", err)
	}
	if snapshot.Number != 3 {
		t.Fatalf("third Reload() generation = %d, want 3", snapshot.Number)
	}
}

func TestRouteManagerBuildFailureKeepsActiveGeneration(t *testing.T) {
	activeForwarder := newControlledGenerationForwarder(false)
	manager := mustRouteManager(t, controlledRouteGeneration("active", activeForwarder, "active-fingerprint"), 0, zap.NewNop())
	activeRuntime := manager.activeRuntime()
	failedForwarder := newControlledGenerationForwarder(false)
	buildErr := errors.New("candidate build failed")

	snapshot, err := manager.Reload(context.Background(), 1, func(context.Context) (*routeGeneration, error) {
		return controlledRouteGeneration("failed", failedForwarder, "failed-fingerprint"), buildErr
	})
	if !errors.Is(err, buildErr) {
		t.Fatalf("Reload() snapshot/error = %+v/%v, want generation 1 build failure", snapshot, err)
	}
	if snapshot.Number != 1 || snapshot.Fingerprint != "active-fingerprint" {
		t.Fatalf("active snapshot after failure = %+v", snapshot)
	}
	if manager.activeRuntime() != activeRuntime {
		t.Fatal("failed candidate replaced active runtime evidence")
	}
	if failedForwarder.closeCalls.Load() != 1 {
		t.Fatalf("failed candidate close calls = %d, want 1", failedForwarder.closeCalls.Load())
	}

	lease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire() after failure error = %v", err)
	}
	defer lease.Release()
	if routeName, found := lease.ResolveRoute(1001); !found || routeName != "active" {
		t.Fatalf("route after failed candidate = %q/%v, want active/true", routeName, found)
	}
	if _, err := lease.Forward(context.Background(), protocol.NewPacket(1001, nil)); err != nil {
		t.Fatalf("Forward() after failure error = %v", err)
	}
}

func TestRouteManagerRejectsStaleExpectedGenerationBeforeBuild(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	var built atomic.Bool
	snapshot, err := manager.Reload(context.Background(), 9, func(context.Context) (*routeGeneration, error) {
		built.Store(true)
		return controlledRouteGeneration("candidate", newControlledGenerationForwarder(false), "candidate-fingerprint"), nil
	})
	if !errors.Is(err, errRouteGenerationConflict) {
		t.Fatalf("Reload() error = %v, want %v", err, errRouteGenerationConflict)
	}
	if snapshot.Number != 1 || built.Load() {
		t.Fatalf("Reload() snapshot/built = %+v/%v, want generation 1 and no build", snapshot, built.Load())
	}
}

func TestRouteManagerDryRunBuildsAndClosesCandidateWithoutActivation(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	candidateForwarder := newControlledGenerationForwarder(false)
	snapshot, err := manager.DryRun(context.Background(), 1, func(context.Context) (*routeGeneration, error) {
		return controlledRouteGeneration("candidate", candidateForwarder, "candidate-fingerprint"), nil
	})
	if err != nil {
		t.Fatalf("DryRun() error = %v", err)
	}
	if snapshot.Number != 0 || snapshot.Fingerprint != "candidate-fingerprint" || snapshot.State != "candidate" {
		t.Fatalf("DryRun() snapshot = %+v, want unnumbered candidate", snapshot)
	}
	waitForGenerationClose(t, candidateForwarder)
	active := manager.Snapshot().Active
	if active == nil || active.Number != 1 || active.Fingerprint != "active-fingerprint" {
		t.Fatalf("active generation after dry-run = %+v", active)
	}
}

func TestRouteManagerDryRunRejectsStaleGenerationBeforeBuild(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	var built atomic.Bool
	_, err := manager.DryRun(context.Background(), 9, func(context.Context) (*routeGeneration, error) {
		built.Store(true)
		return controlledRouteGeneration("candidate", newControlledGenerationForwarder(false), "candidate-fingerprint"), nil
	})
	if !errors.Is(err, errRouteGenerationConflict) {
		t.Fatalf("DryRun() error = %v, want %v", err, errRouteGenerationConflict)
	}
	if built.Load() {
		t.Fatal("DryRun() built a candidate after generation conflict")
	}
}

func TestRouteManagerCanceledReloadClosesCandidateWithoutActivation(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	candidateForwarder := newControlledGenerationForwarder(false)
	ctx, cancel := context.WithCancel(context.Background())
	snapshot, err := manager.Reload(ctx, 1, func(context.Context) (*routeGeneration, error) {
		candidate := controlledRouteGeneration("candidate", candidateForwarder, "candidate-fingerprint")
		cancel()
		return candidate, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Reload() error = %v, want context canceled", err)
	}
	if snapshot.Number != 1 || snapshot.Fingerprint != "active-fingerprint" {
		t.Fatalf("active snapshot after cancellation = %+v", snapshot)
	}
	if candidateForwarder.closeCalls.Load() != 1 {
		t.Fatalf("canceled candidate close calls = %d, want 1", candidateForwarder.closeCalls.Load())
	}
}

func TestRouteManagerDrainTimeoutWarnsWithoutForceClose(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	oldForwarder := newControlledGenerationForwarder(false)
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("route-v1", oldForwarder, "fingerprint-v1"),
		15*time.Millisecond,
		zap.New(core),
	)
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if _, err := manager.Reload(context.Background(), 1, func(context.Context) (*routeGeneration, error) {
		return controlledRouteGeneration("route-v2", newControlledGenerationForwarder(false), "fingerprint-v2"), nil
	}); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	retiring := manager.Snapshot().Retiring
	if retiring == nil || retiring.RetiringAt.IsZero() {
		t.Fatalf("retiring generation = %+v, want retirement timestamp", retiring)
	}

	deadline := time.Now().Add(time.Second)
	for logs.FilterMessage("upstream route generation is still draining").Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if logs.FilterMessage("upstream route generation is still draining").Len() != 1 {
		t.Fatal("drain timeout warning was not recorded")
	}
	if oldForwarder.closeCalls.Load() != 0 {
		t.Fatal("drain timeout force-closed a leased generation")
	}

	lease.Release()
	waitForGenerationClose(t, oldForwarder)
}

func TestRouteManagerShutdownWaitsForLeaseWithoutForcingIt(t *testing.T) {
	forwarder := newControlledGenerationForwarder(false)
	manager := mustRouteManager(t, controlledRouteGeneration("active", forwarder, "active-fingerprint"), 0, zap.NewNop())
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	if err := manager.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want deadline exceeded", err)
	}
	if forwarder.closeCalls.Load() != 0 {
		t.Fatal("Shutdown() force-closed a leased generation")
	}
	if _, err := manager.Acquire(); !errors.Is(err, errRouteManagerClosed) {
		t.Fatalf("Acquire() after shutdown error = %v, want %v", err, errRouteManagerClosed)
	}

	lease.Release()
	waitForGenerationClose(t, forwarder)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
	if forwarder.closeCalls.Load() != 1 {
		t.Fatalf("forwarder close calls = %d, want 1", forwarder.closeCalls.Load())
	}
}

func TestRouteManagerConcurrentAcquireResolveReloadAndClose(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("route", newControlledGenerationForwarder(false), "fingerprint-1"),
		0,
		zap.NewNop(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				lease, err := manager.Acquire()
				if err != nil {
					return
				}
				lease.ResolveRoute(1001)
				_, _ = lease.Forward(ctx, protocol.NewPacket(1001, nil))
				lease.Release()
			}
		}()
	}

	for generation := uint64(2); generation <= 12; generation++ {
		waitForRetirement(t, manager)
		fingerprint := "fingerprint-" + strconv.FormatUint(generation, 10)
		_, err := manager.Reload(context.Background(), generation-1, func(context.Context) (*routeGeneration, error) {
			return controlledRouteGeneration("route", newControlledGenerationForwarder(false), fingerprint), nil
		})
		if err != nil {
			cancel()
			workers.Wait()
			t.Fatalf("Reload() generation %d error = %v", generation, err)
		}
		runtime.Gosched()
	}
	cancel()
	workers.Wait()
	waitForRetirement(t, manager)
}

func TestUpstreamRoutesFingerprintIsSanitizedAndStable(t *testing.T) {
	routesA := []UpstreamRouteConfig{{
		Name:     "orders",
		MsgIDMin: 1001,
		MsgIDMax: 1999,
		HTTP: &HTTPUpstreamConfig{
			URL:   "http://orders.internal/upstream",
			Token: "secret-a",
		},
	}}
	routesB := cloneUpstreamRoutes(routesA)
	routesB[0].HTTP.Token = "secret-b"

	fingerprintA, err := upstreamRoutesFingerprint(routesA)
	if err != nil {
		t.Fatalf("upstreamRoutesFingerprint() A error = %v", err)
	}
	fingerprintB, err := upstreamRoutesFingerprint(routesB)
	if err != nil {
		t.Fatalf("upstreamRoutesFingerprint() B error = %v", err)
	}
	if fingerprintA != fingerprintB {
		t.Fatalf("token value changed sanitized fingerprint: %q != %q", fingerprintA, fingerprintB)
	}
	routesWithoutToken := cloneUpstreamRoutes(routesA)
	routesWithoutToken[0].HTTP.Token = ""
	fingerprintWithoutToken, err := upstreamRoutesFingerprint(routesWithoutToken)
	if err != nil {
		t.Fatalf("upstreamRoutesFingerprint() without token error = %v", err)
	}
	if fingerprintA == fingerprintWithoutToken {
		t.Fatal("token presence did not change sanitized fingerprint")
	}
	routesB[0].HTTP.URL = "http://orders-v2.internal/upstream"
	fingerprintB, err = upstreamRoutesFingerprint(routesB)
	if err != nil {
		t.Fatalf("upstreamRoutesFingerprint() changed target error = %v", err)
	}
	if fingerprintA == fingerprintB {
		t.Fatal("route target change did not change fingerprint")
	}
}

type controlledGenerationForwarder struct {
	entered    chan struct{}
	release    chan struct{}
	closed     chan struct{}
	enterOnce  sync.Once
	closeOnce  sync.Once
	calls      atomic.Int64
	closeCalls atomic.Int64
}

func newControlledGenerationForwarder(block bool) *controlledGenerationForwarder {
	forwarder := &controlledGenerationForwarder{
		entered: make(chan struct{}),
		closed:  make(chan struct{}),
	}
	if block {
		forwarder.release = make(chan struct{})
	}
	return forwarder
}

func (f *controlledGenerationForwarder) Forward(ctx context.Context, _ *protocol.Packet) (*router.ForwardResult, error) {
	f.calls.Add(1)
	f.enterOnce.Do(func() { close(f.entered) })
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &router.ForwardResult{TargetType: "http", Status: "ok", StatusCode: 200}, nil
}

func (f *controlledGenerationForwarder) Close() error {
	f.closeCalls.Add(1)
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func controlledRouteGeneration(routeName string, forwarder router.Forwarder, fingerprint string) *routeGeneration {
	routes := []UpstreamRouteConfig{{
		Name:     routeName,
		MsgIDMin: 1001,
		MsgIDMax: 1001,
		HTTP:     &HTTPUpstreamConfig{URL: "http://backend.internal/upstream"},
	}}
	engine := router.NewEngine([]router.Route{{
		Name:      routeName,
		MsgIDMin:  1001,
		MsgIDMax:  1001,
		Forwarder: forwarder,
	}})
	return makeRouteGeneration(routes, engine, newUpstreamRuntime(routes), fingerprint)
}

func mustRouteManager(
	t *testing.T,
	initial *routeGeneration,
	drainTimeout time.Duration,
	logger *zap.Logger,
) *routeManager {
	t.Helper()
	manager, err := newRouteManager(initial, drainTimeout, logger)
	if err != nil {
		t.Fatalf("newRouteManager() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Errorf("route manager Shutdown() error = %v", err)
		}
	})
	return manager
}

func waitForGenerationClose(t *testing.T, forwarder *controlledGenerationForwarder) {
	t.Helper()
	select {
	case <-forwarder.closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for route generation close")
	}
}

func waitForRetirement(t *testing.T, manager *routeManager) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if manager.Snapshot().Retiring == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for retiring generation")
		}
		runtime.Gosched()
	}
}
