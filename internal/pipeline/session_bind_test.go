package pipeline

import (
	"context"
	"testing"

	"github.com/aceld/zinx/ziface"
	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

func TestSessionBindHandlerBindsOnlyBindPacket(t *testing.T) {
	sessions := session.NewManager()
	handler := NewSessionBindHandler(sessions, "gateway-a", "session_id", zap.NewNop())
	ctx := sessionBindContext(7, protocol.MsgIDBind, "client-1", "device-1")

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if ctx.BindResult == nil || ctx.BindResult.Session == nil {
		t.Fatal("BindResult = nil, want session")
	}
	if ctx.Session == nil || ctx.Session.SessionID == "" {
		t.Fatal("Session = nil, want bound session")
	}
	if ctx.Packet.ClientID != "client-1" || ctx.Packet.DeviceID != "device-1" || ctx.Packet.SessionID == "" {
		t.Fatalf("packet identity = %+v", ctx.Packet)
	}
	if sessions.Len() != 1 {
		t.Fatalf("sessions.Len() = %d, want 1", sessions.Len())
	}
}

func TestSessionBindHandlerRejectsBusinessPacketBeforeBind(t *testing.T) {
	sessions := session.NewManager()
	handler := NewSessionBindHandler(sessions, "gateway-a", "session_id", zap.NewNop())
	ctx := sessionBindContext(7, 2001, "client-1", "device-1")

	err := handler.Handle(ctx)
	code, reason := AckError(err)
	if code != protocol.AckRejected {
		t.Fatalf("Ack code = %s, want %s", code, protocol.AckRejected)
	}
	if reason != "session is not bound; send AUTH/BIND first" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestSessionBindHandlerAcceptsBusinessPacketAfterBind(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{
		SessionID:   "session-1",
		ConnID:      7,
		ClientID:    "client-1",
		DeviceID:    "device-1",
		TokenID:     "token-1",
		GatewayNode: "gateway-a",
	}); err != nil {
		t.Fatalf("sessions.Bind() error = %v", err)
	}

	handler := NewSessionBindHandler(sessions, "gateway-a", "session_id", zap.NewNop())
	ctx := sessionBindContext(7, 2001, "client-1", "device-1")

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if ctx.BindResult != nil {
		t.Fatalf("BindResult = %+v, want nil for non-bind packet", ctx.BindResult)
	}
	if ctx.Session == nil || ctx.Session.SessionID != "session-1" {
		t.Fatalf("Session = %+v, want session-1", ctx.Session)
	}
	if ctx.Packet.SessionID != "session-1" {
		t.Fatalf("Packet SessionID = %q, want session-1", ctx.Packet.SessionID)
	}
}

func TestSessionBindHandlerRejectsMismatchedBoundDevice(t *testing.T) {
	sessions := session.NewManager()
	if _, err := sessions.Bind(session.BindInput{
		SessionID: "session-1",
		ConnID:    7,
		ClientID:  "client-1",
		DeviceID:  "device-1",
	}); err != nil {
		t.Fatalf("sessions.Bind() error = %v", err)
	}

	handler := NewSessionBindHandler(sessions, "gateway-a", "session_id", zap.NewNop())
	ctx := sessionBindContext(7, 2001, "client-1", "device-2")

	err := handler.Handle(ctx)
	code, reason := AckError(err)
	if code != protocol.AckRejected {
		t.Fatalf("Ack code = %s, want %s", code, protocol.AckRejected)
	}
	if reason != "packet device_id does not match bound session" {
		t.Fatalf("reason = %q", reason)
	}
}

func sessionBindContext(connID uint64, msgID uint32, clientID, deviceID string) *Context {
	packet := protocol.NewPacket(msgID, nil)
	packet.ClientID = clientID
	packet.DeviceID = deviceID

	return &Context{
		BaseContext: context.Background(),
		Request:     &sessionBindRequest{conn: &sessionBindConnection{connID: connID}},
		Packet:      packet,
		Principal: &auth.Principal{
			ClientID: clientID,
			TokenID:  "token-1",
		},
	}
}

type sessionBindRequest struct {
	ziface.BaseRequest
	conn ziface.IConnection
}

func (r *sessionBindRequest) GetConnection() ziface.IConnection {
	return r.conn
}

type sessionBindConnection struct {
	ziface.IConnection
	connID uint64
}

func (c *sessionBindConnection) Context() context.Context {
	return context.Background()
}

func (c *sessionBindConnection) GetConnID() uint64 {
	return c.connID
}

func (c *sessionBindConnection) SetProperty(string, interface{}) {}
