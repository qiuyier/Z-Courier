package server

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/bytedance/sonic"
	"github.com/gorilla/websocket"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/resilience"
	"github.com/qiuyier/Z-Courier/internal/router"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestIngressRouterHandlesDownlinkAckWithoutForwarding(t *testing.T) {
	now := time.UnixMilli(1760000000000)
	store := downlink.NewMemoryStore()
	if _, err := store.Save(context.Background(), downlink.Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    downlink.MessageStatusSent,
		SentAt:    now,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	service := downlink.NewService(testSessionFinder{}, testConnectionFinder{}, downlink.WithStore(store))
	forwarder := &testForwarder{}
	upstream := router.NewEngine([]router.Route{{
		Name:      "should-not-see-ack",
		MsgIDMin:  protocol.MsgIDDownlinkAck,
		Forwarder: forwarder,
	}})

	chain := pipeline.NewChain(pipeline.HandlerFunc(func(ctx *pipeline.Context) error {
		ctx.BindResult = &session.BindResult{
			Session: &session.Session{
				SessionID: "session-1",
				ConnID:    ctx.ConnID(),
				ClientID:  "client-1",
				DeviceID:  "device-1",
			},
		}
		ctx.Packet.ClientID = "client-1"
		ctx.Packet.DeviceID = "device-1"
		ctx.Packet.SessionID = "session-1"
		return nil
	}))
	ingress := NewIngressRouter(zap.NewNop(), nil, chain, nil, newTestRouteManager(t, upstream), service, 100)

	body, err := sonic.Marshal(downlink.ClientAckRequest{
		MessageID: "message-1",
		Code:      downlink.ClientAckCodeDelivered,
	})
	if err != nil {
		t.Fatalf("Marshal ack error = %v", err)
	}
	packet := protocol.NewPacket(protocol.MsgIDDownlinkAck, body)
	packet.Token = "token"
	packet.DeviceID = "device-1"
	encoded, err := protocol.Encode(packet)
	if err != nil {
		t.Fatalf("Encode packet error = %v", err)
	}

	conn := &testZinxConnection{connID: 7}
	request := &testZinxRequest{
		conn:  conn,
		msgID: protocol.MsgIDDownlinkAck,
		data:  encoded,
	}

	ingress.Handle(request)

	if forwarder.packet != nil {
		t.Fatalf("downlink ack was forwarded upstream: %+v", forwarder.packet)
	}
	if conn.sentMsgID != protocol.MsgIDAck {
		t.Fatalf("sent msg id = %d, want gateway ack %d", conn.sentMsgID, protocol.MsgIDAck)
	}

	stored, ok, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if !ok {
		t.Fatal("stored message not found")
	}
	if stored.Status != downlink.MessageStatusDelivered {
		t.Fatalf("stored Status = %q, want delivered", stored.Status)
	}
}

func TestIngressRouterHandlesBindWithoutForwarding(t *testing.T) {
	forwarder := &testForwarder{}
	upstream := router.NewEngine([]router.Route{{
		Name:      "should-not-see-bind",
		MsgIDMin:  protocol.MsgIDBind,
		Forwarder: forwarder,
	}})

	chain := pipeline.NewChain(pipeline.HandlerFunc(func(ctx *pipeline.Context) error {
		ctx.BindResult = &session.BindResult{
			Session: &session.Session{
				SessionID: "session-1",
				ConnID:    ctx.ConnID(),
				ClientID:  "client-1",
				DeviceID:  "device-1",
			},
			Created: true,
		}
		ctx.Session = ctx.BindResult.Session
		ctx.Packet.ClientID = "client-1"
		ctx.Packet.DeviceID = "device-1"
		ctx.Packet.SessionID = "session-1"
		return nil
	}))
	ingress := NewIngressRouter(zap.NewNop(), nil, chain, upstream, nil, nil, 100)

	packet := protocol.NewPacket(protocol.MsgIDBind, []byte("bind"))
	packet.Token = "token"
	packet.DeviceID = "device-1"
	encoded, err := protocol.Encode(packet)
	if err != nil {
		t.Fatalf("Encode packet error = %v", err)
	}

	conn := &testZinxConnection{connID: 7}
	request := &testZinxRequest{
		conn:  conn,
		msgID: protocol.MsgIDBind,
		data:  encoded,
	}

	ingress.Handle(request)

	if forwarder.packet != nil {
		t.Fatalf("bind packet was forwarded upstream: %+v", forwarder.packet)
	}
	if conn.sentMsgID != protocol.MsgIDAck {
		t.Fatalf("sent msg id = %d, want gateway ack %d", conn.sentMsgID, protocol.MsgIDAck)
	}
}

func TestIngressRouterReturnsStableOverloadReason(t *testing.T) {
	forwarder := &testForwarder{
		result: &router.ForwardResult{RouteName: "chat", TargetType: "http", Status: resilience.ReasonOverloaded},
		err:    router.ErrOverloaded,
	}
	upstream := router.NewEngine([]router.Route{{
		Name:      "chat",
		MsgIDMin:  1001,
		MsgIDMax:  1001,
		Forwarder: forwarder,
	}})
	chain := pipeline.NewChain(pipeline.HandlerFunc(func(ctx *pipeline.Context) error {
		ctx.Session = &session.Session{
			SessionID: "session-1",
			ConnID:    ctx.ConnID(),
			ClientID:  "client-1",
			DeviceID:  "device-1",
		}
		return nil
	}))
	ingress := NewIngressRouter(zap.NewNop(), nil, chain, upstream, nil, nil, 100)

	packet := protocol.NewPacket(1001, []byte("hello"))
	packet.ClientID = "client-1"
	packet.DeviceID = "device-1"
	packet.SessionID = "session-1"
	packet.MessageID = "message-1"
	encoded, err := protocol.Encode(packet)
	if err != nil {
		t.Fatalf("Encode packet error = %v", err)
	}

	conn := &testZinxConnection{connID: 7}
	request := &testZinxRequest{
		conn:  conn,
		msgID: 1001,
		data:  encoded,
	}

	ingress.Handle(request)

	ack := decodeSentAck(t, conn)
	if ack.Code != protocol.AckRejected {
		t.Fatalf("Ack code = %s, want %s", ack.Code, protocol.AckRejected)
	}
	if ack.Reason != resilience.ReasonOverloaded {
		t.Fatalf("Ack reason = %q, want %q", ack.Reason, resilience.ReasonOverloaded)
	}
}

func TestIngressRouterPinsGenerationRouteBeforePipeline(t *testing.T) {
	forwarder := &testForwarder{}
	upstream := router.NewEngine([]router.Route{{
		Name:      "generation-route",
		MsgIDMin:  1001,
		MsgIDMax:  1001,
		Forwarder: forwarder,
	}})
	manager := newTestRouteManager(t, upstream)
	chain := pipeline.NewChain(pipeline.HandlerFunc(func(ctx *pipeline.Context) error {
		if !ctx.RouteResolutionSet || !ctx.RouteFound || ctx.RouteName != "generation-route" {
			t.Fatalf("pipeline route resolution = %q/%v/%v", ctx.RouteName, ctx.RouteFound, ctx.RouteResolutionSet)
		}
		ctx.Session = &session.Session{
			SessionID: "session-1",
			ConnID:    ctx.ConnID(),
			ClientID:  "client-1",
			DeviceID:  "device-1",
		}
		return nil
	}))
	ingress := NewIngressRouter(zap.NewNop(), nil, chain, nil, manager, nil, 100)

	packet := protocol.NewPacket(1001, []byte("hello"))
	packet.ClientID = "client-1"
	packet.DeviceID = "device-1"
	packet.SessionID = "session-1"
	packet.MessageID = "message-1"
	encoded, err := protocol.Encode(packet)
	if err != nil {
		t.Fatalf("Encode packet error = %v", err)
	}
	conn := &testZinxConnection{connID: 7}
	ingress.Handle(&testZinxRequest{conn: conn, msgID: 1001, data: encoded})

	if forwarder.packet == nil || forwarder.packet.MessageID != "message-1" {
		t.Fatalf("forwarded packet = %+v", forwarder.packet)
	}
	if snapshot := manager.Snapshot(); snapshot.Active == nil || snapshot.Active.InFlight != 0 {
		t.Fatalf("route manager after request = %+v, want no in-flight leases", snapshot)
	}
}

func TestUpstreamForwardMetadataIsStructuredAndAckSafe(t *testing.T) {
	cause := errors.New("dial http://10.0.0.2:8080/gateway/upstream?token=secret failed")
	forwardErr := &router.ForwardError{
		Class:       router.FailureClassTransport,
		Endpoint:    "http://10.0.0.2:8080/gateway/upstream",
		Attempts:    2,
		MaxAttempts: 2,
		Retryable:   true,
		Decision:    router.FailoverDecisionExhausted,
		Cause:       cause,
	}
	result := &router.ForwardResult{
		RouteName:   "orders",
		TargetType:  "http",
		Endpoint:    forwardErr.Endpoint,
		Attempts:    forwardErr.Attempts,
		MaxAttempts: forwardErr.MaxAttempts,
	}

	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)
	fields := upstreamForwardMetadataFields(result, forwardErr)
	fields = append(fields, zap.Error(forwardErr))
	logger.Warn("failed to forward upstream packet", fields...)

	if logs.Len() != 1 {
		t.Fatalf("logs = %d, want 1", logs.Len())
	}
	contextMap := logs.All()[0].ContextMap()
	for key, want := range map[string]any{
		"endpoint":           forwardErr.Endpoint,
		"attempt_count":      int64(2),
		"max_attempts":       int64(2),
		"failover_attempted": true,
		"failure_class":      "transport",
		"retryable":          true,
		"failover_decision":  "exhausted",
	} {
		if got := contextMap[key]; got != want {
			t.Fatalf("log field %q = %#v, want %#v", key, got, want)
		}
	}
	if got := contextMap["error"]; strings.Contains(got.(string), "token=secret") ||
		strings.Contains(got.(string), "10.0.0.2") {
		t.Fatalf("error log field exposes cause or endpoint: %q", got)
	}
	if got := upstreamAckReason(forwardErr); got != resilience.ReasonUpstreamFailed {
		t.Fatalf("upstreamAckReason() = %q, want %q", got, resilience.ReasonUpstreamFailed)
	}
	if got := safeUpstreamFailureReason(result, forwardErr); got != "transport" {
		t.Fatalf("safeUpstreamFailureReason() = %q, want transport", got)
	}
}

type testSessionFinder struct{}

func (testSessionFinder) GetByClientDevice(string, string) (*session.Session, bool) {
	return nil, false
}

type testConnectionFinder struct{}

func (testConnectionFinder) Get(uint64) (downlink.Connection, error) {
	return nil, errors.New("not implemented")
}

type testForwarder struct {
	packet *protocol.Packet
	result *router.ForwardResult
	err    error
}

func newTestRouteManager(t *testing.T, engine *router.Engine) *routeManager {
	t.Helper()
	manager, err := newRouteManager(
		makeRouteGeneration(nil, engine, nil, "test-generation"),
		0,
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("newRouteManager() error = %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("route manager Close() error = %v", err)
		}
	})
	return manager
}

func (f *testForwarder) Forward(_ context.Context, packet *protocol.Packet) (*router.ForwardResult, error) {
	f.packet = packet
	if f.result != nil || f.err != nil {
		return f.result, f.err
	}
	return &router.ForwardResult{RouteName: "test", TargetType: "test", Status: "ok"}, nil
}

func decodeSentAck(t *testing.T, conn *testZinxConnection) protocol.Ack {
	t.Helper()

	ackPacket, err := protocol.Decode(conn.sentData)
	if err != nil {
		t.Fatalf("Decode sent ack packet error = %v", err)
	}
	ack, err := protocol.DecodeAck(ackPacket)
	if err != nil {
		t.Fatalf("DecodeAck() error = %v", err)
	}
	return ack
}

type testZinxRequest struct {
	ziface.BaseRequest
	conn  ziface.IConnection
	msgID uint32
	data  []byte
}

func (r *testZinxRequest) GetConnection() ziface.IConnection {
	return r.conn
}

func (r *testZinxRequest) GetMsgID() uint32 {
	return r.msgID
}

func (r *testZinxRequest) GetData() []byte {
	return r.data
}

type testZinxConnection struct {
	ziface.IConnection
	connID    uint64
	sentMsgID uint32
	sentData  []byte
}

func (c *testZinxConnection) Context() context.Context {
	return context.Background()
}

func (c *testZinxConnection) GetConnID() uint64 {
	return c.connID
}

func (c *testZinxConnection) SendMsg(msgID uint32, data []byte) error {
	c.sentMsgID = msgID
	c.sentData = append([]byte(nil), data...)
	return nil
}

func (c *testZinxConnection) SetProperty(string, interface{}) {}

func (c *testZinxConnection) RemoveProperty(string) {}

func (c *testZinxConnection) RemoteAddrString() string {
	return ""
}

func (c *testZinxConnection) RemoteAddr() net.Addr {
	return nil
}

func (c *testZinxConnection) LocalAddr() net.Addr {
	return nil
}

func (c *testZinxConnection) GetConnection() net.Conn {
	return nil
}

func (c *testZinxConnection) GetTCPConnection() net.Conn {
	return nil
}

func (c *testZinxConnection) GetWsConn() *websocket.Conn {
	return nil
}
