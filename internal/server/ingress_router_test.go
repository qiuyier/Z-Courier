package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/bytedance/sonic"
	"github.com/gorilla/websocket"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
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
	ingress := NewIngressRouter(zap.NewNop(), nil, chain, upstream, service, 100)

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
}

func (f *testForwarder) Forward(_ context.Context, packet *protocol.Packet) (*router.ForwardResult, error) {
	f.packet = packet
	return &router.ForwardResult{RouteName: "test", TargetType: "test", Status: "ok"}, nil
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
