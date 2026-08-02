package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"go.uber.org/zap"
)

func TestRouteControlDryRunKeepsActiveGenerationAndAuditsValidation(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	audit := adminaudit.NewStore(adminaudit.StoreConfig{})
	control := newRouteControl(Config{
		GatewayNode: "gateway-a",
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Loader: testRouteSnapshotLoader("candidate", "http://candidate.internal/upstream"),
		},
	}, manager, audit, zap.NewNop())

	outcome, err := control.Execute(context.Background(), routeReloadOptions{
		DryRun:             true,
		ExpectedGeneration: 1,
		Trigger:            routeReloadTriggerAdminAPI,
	}, routeReloadActor{AuthMode: "token"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Code != "ok" || outcome.Result != "validated" || !outcome.Changed || outcome.Candidate.RouteCount != 1 {
		t.Fatalf("outcome = %+v, want changed validated candidate", outcome)
	}
	active := manager.Snapshot().Active
	if active == nil || active.Number != 1 || active.Fingerprint != "active-fingerprint" {
		t.Fatalf("active generation after dry-run = %+v", active)
	}
	events := audit.List(adminaudit.Query{Limit: 10}).Entries
	if len(events) != 1 || events[0].Action != "route_reload_validate" || events[0].Result != "validated" {
		t.Fatalf("audit events = %+v", events)
	}
	if events[0].Details["expected_generation"] != "1" {
		t.Fatalf("audit expected_generation = %q, want 1", events[0].Details["expected_generation"])
	}
}

func TestRouteControlReloadActivatesCandidate(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	audit := adminaudit.NewStore(adminaudit.StoreConfig{})
	control := newRouteControl(Config{
		GatewayNode: "gateway-a",
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Loader: testRouteSnapshotLoader("candidate", "http://candidate.internal/upstream"),
		},
	}, manager, audit, zap.NewNop())

	outcome, err := control.Execute(context.Background(), routeReloadOptions{
		ExpectedGeneration: 1,
	}, routeReloadActor{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if outcome.Result != "reloaded" || outcome.Active.Number != 2 || outcome.Active.Fingerprint == "active-fingerprint" {
		t.Fatalf("outcome = %+v, want active generation 2", outcome)
	}
	activeRoutes := control.ActiveRoutes()
	if len(activeRoutes) != 1 || activeRoutes[0].Name != "candidate" {
		t.Fatalf("active routes = %+v, want candidate", activeRoutes)
	}
}

func TestRouteControlGenerationConflictDoesNotLoadCandidate(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	var loads atomic.Int64
	control := newRouteControl(Config{
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Loader: func(context.Context) (UpstreamRouteSnapshot, error) {
				loads.Add(1)
				return UpstreamRouteSnapshot{}, nil
			},
		},
	}, manager, nil, zap.NewNop())

	outcome, err := control.Execute(context.Background(), routeReloadOptions{
		DryRun:             true,
		ExpectedGeneration: 9,
	}, routeReloadActor{})
	if !errors.Is(err, errRouteGenerationConflict) || outcome.Code != "generation_conflict" {
		t.Fatalf("Execute() outcome/error = %+v/%v", outcome, err)
	}
	if loads.Load() != 0 {
		t.Fatalf("loader calls = %d, want 0", loads.Load())
	}
}

func TestRouteControlSanitizesCandidateLoadFailure(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	audit := adminaudit.NewStore(adminaudit.StoreConfig{})
	control := newRouteControl(Config{
		GatewayNode: "gateway-a",
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Loader: func(context.Context) (UpstreamRouteSnapshot, error) {
				return UpstreamRouteSnapshot{}, NewUpstreamRouteLoadError(
					UpstreamRouteLoadStageValidation,
					fmt.Errorf("read /secret/routes.yaml with token super-secret"),
				)
			},
		},
	}, manager, audit, zap.NewNop())

	outcome, err := control.Execute(context.Background(), routeReloadOptions{DryRun: true}, routeReloadActor{})
	if !errors.Is(err, errRouteCandidateLoadFailed) || outcome.Code != "validation_failed" || outcome.Stage != routeReloadStageValidation {
		t.Fatalf("Execute() outcome/error = %+v/%v", outcome, err)
	}
	events := audit.List(adminaudit.Query{Limit: 10}).Entries
	combined := fmt.Sprintf("%+v %+v", outcome, events)
	for _, secret := range []string{"/secret/routes.yaml", "super-secret"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("control response or audit leaked %q: %s", secret, combined)
		}
	}
}

func TestRouteControlClassifiesRouteLoadStages(t *testing.T) {
	tests := []struct {
		stage UpstreamRouteLoadStage
		code  string
	}{
		{stage: UpstreamRouteLoadStageSourceRead, code: "source_read_failed"},
		{stage: UpstreamRouteLoadStageParse, code: "parse_failed"},
		{stage: UpstreamRouteLoadStageValidation, code: "validation_failed"},
	}

	for _, test := range tests {
		t.Run(string(test.stage), func(t *testing.T) {
			manager := mustRouteManager(
				t,
				controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
				0,
				zap.NewNop(),
			)
			control := newRouteControl(Config{
				UpstreamRoutesFile: UpstreamRoutesFileConfig{
					Loader: func(context.Context) (UpstreamRouteSnapshot, error) {
						return UpstreamRouteSnapshot{}, NewUpstreamRouteLoadError(test.stage, errors.New("sensitive detail"))
					},
				},
			}, manager, nil, zap.NewNop())

			outcome, err := control.Execute(context.Background(), routeReloadOptions{DryRun: true}, routeReloadActor{})
			if !errors.Is(err, errRouteCandidateLoadFailed) || outcome.Code != test.code || outcome.Stage != string(test.stage) {
				t.Fatalf("Execute() outcome/error = %+v/%v", outcome, err)
			}
		})
	}
}

func TestRouteControlKeepsBoundedNewestFirstHistory(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	control := newRouteControl(Config{
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Loader: testRouteSnapshotLoader("candidate", "http://candidate.internal/upstream"),
		},
	}, manager, nil, zap.NewNop())

	for index := 0; index < routeReloadHistoryLimit+5; index++ {
		_, err := control.Execute(context.Background(), routeReloadOptions{
			DryRun:             true,
			ExpectedGeneration: uint64(index + 1),
		}, routeReloadActor{})
		if index == 0 && err != nil {
			t.Fatalf("first Execute() error = %v", err)
		}
	}

	runtime := control.Status().Runtime
	if runtime.OperationsInFlight != 0 || len(runtime.RecentAttempts) != routeReloadHistoryLimit {
		t.Fatalf("runtime = %+v, want no in-flight and %d attempts", runtime, routeReloadHistoryLimit)
	}
	if runtime.RecentAttempts[0].ExpectedGeneration != routeReloadHistoryLimit+5 {
		t.Fatalf("newest attempt = %+v", runtime.RecentAttempts[0])
	}
	if runtime.LastSuccessAt.IsZero() || runtime.LastFailureAt.IsZero() {
		t.Fatalf("runtime timestamps = %+v, want success and failure", runtime)
	}
}

func TestRouteControlReportsDisabledWithoutManager(t *testing.T) {
	control := newRouteControl(Config{GatewayNode: "gateway-a"}, nil, nil, zap.NewNop())
	outcome, err := control.Execute(context.Background(), routeReloadOptions{}, routeReloadActor{})
	if !errors.Is(err, errRouteReloadDisabled) || outcome.Code != "reload_disabled" || outcome.HTTPStatus != 409 {
		t.Fatalf("Execute() outcome/error = %+v/%v", outcome, err)
	}
	if control.Status().Enabled {
		t.Fatal("disabled route control reported enabled")
	}
}

func TestGatewaySIGHUPWorkerReloadsConfiguredRouteFile(t *testing.T) {
	manager := mustRouteManager(
		t,
		controlledRouteGeneration("active", newControlledGenerationForwarder(false), "active-fingerprint"),
		0,
		zap.NewNop(),
	)
	audit := adminaudit.NewStore(adminaudit.StoreConfig{})
	control := newRouteControl(Config{
		GatewayNode: "gateway-a",
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Loader: testRouteSnapshotLoader("candidate", "http://candidate.internal/upstream"),
		},
	}, manager, audit, zap.NewNop())
	gateway := &Gateway{routeControl: control}
	gateway.startRouteReloadSignalWorker()
	t.Cleanup(gateway.shutdownRouteReloadSignalWorker)

	gateway.routeReloadSignal <- syscall.SIGHUP
	deadline := time.Now().Add(time.Second)
	var lastEvents []adminaudit.Entry
	for time.Now().Before(deadline) {
		active := manager.Snapshot().Active
		lastEvents = audit.List(adminaudit.Query{Limit: 10}).Entries
		if active != nil && active.Number == 2 && len(lastEvents) > 0 {
			if routes := control.ActiveRoutes(); len(routes) != 1 || routes[0].Name != "candidate" {
				t.Fatalf("active routes = %+v, want SIGHUP candidate", routes)
			}
			if len(lastEvents) != 1 || lastEvents[0].Details["trigger"] != routeReloadTriggerSIGHUP || lastEvents[0].AuthMode != "system" {
				t.Fatalf("SIGHUP audit events = %+v", lastEvents)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("SIGHUP reload incomplete: active generation = %+v audit events = %+v", manager.Snapshot().Active, lastEvents)
}

func testRouteSnapshotLoader(name string, targetURL string) UpstreamRouteLoader {
	return func(context.Context) (UpstreamRouteSnapshot, error) {
		return UpstreamRouteSnapshot{Routes: []UpstreamRouteConfig{{
			Name:     name,
			MsgIDMin: 1001,
			MsgIDMax: 1001,
			HTTP: &HTTPUpstreamConfig{
				URL: targetURL,
			},
		}}}, nil
	}
}
