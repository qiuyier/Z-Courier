package server

import (
	"context"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/protocol"
	"go.uber.org/zap"
)

func TestRegisteredMsgIDsIncludesUpstreamRanges(t *testing.T) {
	msgIDs, err := registeredMsgIDs(Config{
		RouteMsgIDs: []uint32{1000},
		UpstreamRoutes: []UpstreamRouteConfig{
			{Name: "http", MsgIDMin: 1000, MsgIDMax: 1002},
			{Name: "nsq", MsgIDMin: 2000, MsgIDMax: 2001},
		},
	})
	if err != nil {
		t.Fatalf("registeredMsgIDs() error = %v", err)
	}

	want := []uint32{2, 1000, 1001, 1002, 2000, 2001}
	if len(msgIDs) != len(want) {
		t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
	}
	for i := range want {
		if msgIDs[i] != want[i] {
			t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
		}
	}
}

func TestRegisteredMsgIDsAlwaysIncludesControlMessages(t *testing.T) {
	msgIDs, err := registeredMsgIDs(Config{})
	if err != nil {
		t.Fatalf("registeredMsgIDs() error = %v", err)
	}

	want := []uint32{protocol.MsgIDDownlinkAck, protocol.MsgIDBind}
	if len(msgIDs) != len(want) {
		t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
	}
	for i := range want {
		if msgIDs[i] != want[i] {
			t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
		}
	}
}

func TestRegisteredMsgIDsIncludesReloadAdmissionRanges(t *testing.T) {
	msgIDs, err := registeredMsgIDs(Config{
		UpstreamRoutes: []UpstreamRouteConfig{
			{Name: "current", MsgIDMin: 1001, MsgIDMax: 1002},
		},
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Reload: UpstreamRouteReloadConfig{
				Enabled: true,
				AcceptedMsgIDRanges: []MsgIDRange{
					{Min: 1001, Max: 1003},
					{Min: 2001, Max: 2002},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("registeredMsgIDs() error = %v", err)
	}

	want := []uint32{2, 1000, 1001, 1002, 1003, 2001, 2002}
	if len(msgIDs) != len(want) {
		t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
	}
	for index := range want {
		if msgIDs[index] != want[index] {
			t.Fatalf("registered msg IDs = %v, want %v", msgIDs, want)
		}
	}
}

func TestRegisteredMsgIDsRejectsReloadWithoutAdmissionRanges(t *testing.T) {
	_, err := registeredMsgIDs(Config{
		UpstreamRoutesFile: UpstreamRoutesFileConfig{
			Reload: UpstreamRouteReloadConfig{Enabled: true},
		},
	})
	if err == nil {
		t.Fatal("registeredMsgIDs() error = nil, want error")
	}
}

func TestRegisteredMsgIDsRejectsInvalidRange(t *testing.T) {
	_, err := registeredMsgIDs(Config{
		UpstreamRoutes: []UpstreamRouteConfig{
			{Name: "broken", MsgIDMin: 2000, MsgIDMax: 1000},
		},
	})
	if err == nil {
		t.Fatal("registeredMsgIDs() error = nil, want error")
	}
}

func TestCompactMsgIDRanges(t *testing.T) {
	got := compactMsgIDRanges([]uint32{2, 1000, 1001, 1002, 2000, 2001, 3000})
	want := []string{"2", "1000-1002", "2000-2001", "3000"}
	if len(got) != len(want) {
		t.Fatalf("compactMsgIDRanges() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("compactMsgIDRanges() = %v, want %v", got, want)
		}
	}
}

func TestNewGatewayKeepsStaticFastPathWhenReloadDisabled(t *testing.T) {
	config := testGatewayRouteLifecycleConfig(false)
	gateway, err := New(config, zap.NewNop())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() {
		if err := gateway.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}()

	if gateway.upstream == nil || gateway.routes != nil {
		t.Fatalf("static upstream/manager = %T/%T, want engine/non-manager", gateway.upstream, gateway.routes)
	}
}

func TestNewGatewayUsesGenerationManagerWhenReloadEnabled(t *testing.T) {
	config := testGatewayRouteLifecycleConfig(true)
	gateway, err := New(config, zap.NewNop())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() {
		if err := gateway.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	}()

	if gateway.upstream != nil || gateway.routes == nil {
		t.Fatalf("reload upstream/manager = %T/%T, want nil engine/generation manager", gateway.upstream, gateway.routes)
	}
	snapshot := gateway.routes.Snapshot()
	if snapshot.Active == nil || snapshot.Active.Number != 1 || snapshot.Active.RouteCount != 1 {
		t.Fatalf("initial route generation = %+v", snapshot)
	}
}

func TestNewGatewayRejectsReloadWithoutRouteLoader(t *testing.T) {
	config := testGatewayRouteLifecycleConfig(true)
	config.UpstreamRoutesFile.Loader = nil
	if _, err := New(config, zap.NewNop()); err == nil {
		t.Fatal("New() error = nil, want missing route loader error")
	}
}

func testGatewayRouteLifecycleConfig(reload bool) Config {
	config := DefaultConfig()
	config.DisableInternalHTTP = true
	config.UpstreamRoutes = []UpstreamRouteConfig{{
		Name:        "orders",
		MsgIDMin:    1001,
		MsgIDMax:    1001,
		MaxInFlight: 10,
		HTTP: &HTTPUpstreamConfig{
			URL:     "http://127.0.0.1:1/upstream",
			Timeout: time.Second,
		},
	}}
	if reload {
		routes := cloneUpstreamRoutes(config.UpstreamRoutes)
		config.UpstreamRoutesFile.Loader = func(context.Context) (UpstreamRouteSnapshot, error) {
			return UpstreamRouteSnapshot{Routes: cloneUpstreamRoutes(routes)}, nil
		}
		config.UpstreamRoutesFile.Reload = UpstreamRouteReloadConfig{
			Enabled:      true,
			DrainTimeout: time.Second,
			AcceptedMsgIDRanges: []MsgIDRange{{
				Min: 1001,
				Max: 1001,
			}},
		}
	}
	return config
}
