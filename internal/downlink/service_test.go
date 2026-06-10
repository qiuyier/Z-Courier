package downlink

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/session"
)

func TestServicePushSendsEncodedPacket(t *testing.T) {
	sessions := fakeSessions{
		session: &session.Session{
			SessionID: "session-1",
			ConnID:    7,
			ClientID:  "client-1",
			DeviceID:  "device-1",
		},
	}
	conn := &fakeConnection{}
	connections := fakeConnections{conn: conn}
	service := NewService(sessions, connections)
	service.now = func() time.Time { return time.UnixMilli(1760000000000) }

	resp, err := service.Push(context.Background(), PushRequest{
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		MessageID:   "message-1",
		TraceID:     "trace-1",
		AckRequired: true,
		Body:        []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	if resp.Code != "ok" {
		t.Fatalf("Code = %q, want ok", resp.Code)
	}
	if conn.msgID != 2001 {
		t.Fatalf("sent msgID = %d, want 2001", conn.msgID)
	}

	packet, err := protocol.Decode(conn.data)
	if err != nil {
		t.Fatalf("Decode sent packet error = %v", err)
	}

	if packet.ClientID != "client-1" || packet.DeviceID != "device-1" || packet.SessionID != "session-1" {
		t.Fatalf("packet identity = %q/%q/%q", packet.ClientID, packet.DeviceID, packet.SessionID)
	}
	if packet.MessageID != "message-1" || packet.TraceID != "trace-1" {
		t.Fatalf("packet message metadata = %q/%q", packet.MessageID, packet.TraceID)
	}
	if packet.Flags&protocol.FlagAckRequired == 0 {
		t.Fatal("packet missing FlagAckRequired")
	}
	if string(packet.Body) != "hello" {
		t.Fatalf("packet body = %q, want hello", packet.Body)
	}
}

func TestServiceReliablePushStoresAndSendsOnlineMessage(t *testing.T) {
	sessions := fakeSessions{
		session: &session.Session{
			SessionID: "session-1",
			ConnID:    7,
			ClientID:  "client-1",
			DeviceID:  "device-1",
		},
	}
	conn := &fakeConnection{}
	store := NewMemoryStore()
	service := NewService(sessions, fakeConnections{conn: conn}, WithStore(store))
	service.now = func() time.Time { return time.UnixMilli(1760000000000) }
	store.now = service.now

	resp, err := service.Push(context.Background(), PushRequest{
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		MessageID: "message-1",
		TraceID:   "trace-1",
		Body:      []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if resp.DeliveryState != DeliveryStateSent {
		t.Fatalf("DeliveryState = %q, want %q", resp.DeliveryState, DeliveryStateSent)
	}
	if len(conn.data) == 0 {
		t.Fatal("connection did not receive data")
	}

	stored, ok, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if !ok {
		t.Fatal("stored message not found")
	}
	if stored.Status != MessageStatusSent {
		t.Fatalf("stored Status = %q, want %q", stored.Status, MessageStatusSent)
	}
	if stored.Attempts != 1 {
		t.Fatalf("stored Attempts = %d, want 1", stored.Attempts)
	}
	if stored.SessionID != "session-1" {
		t.Fatalf("stored SessionID = %q, want session-1", stored.SessionID)
	}
}

func TestServiceReliablePushQueuesOfflineMessage(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(fakeSessions{}, fakeConnections{}, WithStore(store))
	service.now = func() time.Time { return time.UnixMilli(1760000000000) }
	store.now = service.now

	resp, err := service.Push(context.Background(), PushRequest{
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		MessageID: "message-1",
		Body:      []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if resp.DeliveryState != DeliveryStateQueued {
		t.Fatalf("DeliveryState = %q, want %q", resp.DeliveryState, DeliveryStateQueued)
	}

	stored, ok, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if !ok {
		t.Fatal("stored message not found")
	}
	if stored.Status != MessageStatusPending {
		t.Fatalf("stored Status = %q, want %q", stored.Status, MessageStatusPending)
	}
	if stored.Attempts != 1 {
		t.Fatalf("stored Attempts = %d, want 1", stored.Attempts)
	}
	if stored.LastError == "" {
		t.Fatal("stored LastError is empty")
	}
	if !stored.NextRetryAt.Equal(time.UnixMilli(1760000000000).Add(30 * time.Second)) {
		t.Fatalf("stored NextRetryAt = %v", stored.NextRetryAt)
	}
}

func TestServiceRetryDueSendsPendingMessage(t *testing.T) {
	now := time.UnixMilli(1760000000000)
	store := NewMemoryStore()
	store.now = func() time.Time { return now }
	if _, err := store.Save(context.Background(), Message{
		MessageID:   "message-1",
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		Body:        []byte("hello"),
		Status:      MessageStatusPending,
		NextRetryAt: now,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	conn := &fakeConnection{}
	service := NewService(
		fakeSessions{session: &session.Session{SessionID: "session-1", ConnID: 7, ClientID: "client-1", DeviceID: "device-1"}},
		fakeConnections{conn: conn},
		WithStore(store),
	)
	service.now = func() time.Time { return now }

	result, err := service.RetryDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("RetryDue() error = %v", err)
	}
	if result.Scanned != 1 || result.Sent != 1 || result.Queued != 0 || result.Failed != 0 {
		t.Fatalf("RetryDue() result = %+v, want one sent", result)
	}
	if len(conn.data) == 0 {
		t.Fatal("connection did not receive data")
	}

	stored, ok, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if !ok {
		t.Fatal("stored message not found")
	}
	if stored.Status != MessageStatusSent {
		t.Fatalf("stored Status = %q, want sent", stored.Status)
	}
}

func TestServiceRetryDueKeepsOfflineMessageQueued(t *testing.T) {
	now := time.UnixMilli(1760000000000)
	store := NewMemoryStore()
	store.now = func() time.Time { return now }
	if _, err := store.Save(context.Background(), Message{
		MessageID:   "message-1",
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		Status:      MessageStatusPending,
		Attempts:    1,
		NextRetryAt: now,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	service := NewService(fakeSessions{}, fakeConnections{}, WithStore(store), WithMaxAttempts(3))
	service.now = func() time.Time { return now }

	result, err := service.RetryDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("RetryDue() error = %v", err)
	}
	if result.Scanned != 1 || result.Sent != 0 || result.Queued != 1 || result.Failed != 0 {
		t.Fatalf("RetryDue() result = %+v, want one queued", result)
	}

	stored, _, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if stored.Status != MessageStatusPending {
		t.Fatalf("stored Status = %q, want pending", stored.Status)
	}
	if stored.Attempts != 2 {
		t.Fatalf("stored Attempts = %d, want 2", stored.Attempts)
	}
}

func TestServiceRetryDueMarksFailedAfterMaxAttempts(t *testing.T) {
	now := time.UnixMilli(1760000000000)
	store := NewMemoryStore()
	store.now = func() time.Time { return now }
	if _, err := store.Save(context.Background(), Message{
		MessageID:   "message-1",
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		Status:      MessageStatusPending,
		Attempts:    1,
		NextRetryAt: now,
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	service := NewService(fakeSessions{}, fakeConnections{}, WithStore(store), WithMaxAttempts(2))
	service.now = func() time.Time { return now }

	result, err := service.RetryDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("RetryDue() error = %v", err)
	}
	if result.Scanned != 1 || result.Failed != 1 {
		t.Fatalf("RetryDue() result = %+v, want one failed", result)
	}

	stored, _, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if stored.Status != MessageStatusFailed {
		t.Fatalf("stored Status = %q, want failed", stored.Status)
	}
	if stored.Attempts != 2 {
		t.Fatalf("stored Attempts = %d, want 2", stored.Attempts)
	}
}

func TestServiceFlushClientDeviceSendsPendingMessageBeforeRetryTime(t *testing.T) {
	now := time.UnixMilli(1760000000000)
	store := NewMemoryStore()
	store.now = func() time.Time { return now }
	if _, err := store.Save(context.Background(), Message{
		MessageID:   "message-1",
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		Body:        []byte("hello"),
		Status:      MessageStatusPending,
		NextRetryAt: now.Add(time.Minute),
		CreatedAt:   now,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	conn := &fakeConnection{}
	service := NewService(
		fakeSessions{session: &session.Session{SessionID: "session-1", ConnID: 7, ClientID: "client-1", DeviceID: "device-1"}},
		fakeConnections{conn: conn},
		WithStore(store),
	)
	service.now = func() time.Time { return now }

	result, err := service.FlushClientDevice(context.Background(), "client-1", "device-1", 10)
	if err != nil {
		t.Fatalf("FlushClientDevice() error = %v", err)
	}
	if result.Scanned != 1 || result.Sent != 1 {
		t.Fatalf("FlushClientDevice() result = %+v, want one sent", result)
	}
	if len(conn.data) == 0 {
		t.Fatal("connection did not receive data")
	}
}

func TestServiceAckMarksMessageDelivered(t *testing.T) {
	now := time.UnixMilli(1760000000000)
	store := NewMemoryStore()
	store.now = func() time.Time { return now }
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusSent,
		SentAt:    now,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	service := NewService(fakeSessions{}, fakeConnections{}, WithStore(store))
	service.now = func() time.Time { return now.Add(time.Second) }

	message, err := service.Ack(context.Background(), "client-1", "device-1", ClientAckRequest{
		MessageID: "message-1",
		Code:      ClientAckCodeDelivered,
	})
	if err != nil {
		t.Fatalf("Ack() error = %v", err)
	}
	if message.Status != MessageStatusDelivered {
		t.Fatalf("message Status = %q, want delivered", message.Status)
	}
	if !message.DeliveredAt.Equal(now.Add(time.Second)) {
		t.Fatalf("DeliveredAt = %v", message.DeliveredAt)
	}

	stored, _, err := store.Get(context.Background(), "message-1")
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if stored.Status != MessageStatusDelivered {
		t.Fatalf("stored Status = %q, want delivered", stored.Status)
	}
}

func TestServiceAckRejectsWrongClientDevice(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusSent,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	service := NewService(fakeSessions{}, fakeConnections{}, WithStore(store))
	_, err := service.Ack(context.Background(), "client-1", "other-device", ClientAckRequest{
		MessageID: "message-1",
		Code:      ClientAckCodeDelivered,
	})
	if !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("Ack() error = %v, want %v", err, ErrMessageNotFound)
	}
}

func TestServiceAckValidation(t *testing.T) {
	service := NewService(fakeSessions{}, fakeConnections{}, WithStore(NewMemoryStore()))

	tests := []struct {
		name string
		req  ClientAckRequest
		want error
	}{
		{name: "missing message id", req: ClientAckRequest{Code: ClientAckCodeDelivered}, want: ErrMissingMessageID},
		{name: "invalid code", req: ClientAckRequest{MessageID: "m1", Code: "failed"}, want: ErrInvalidAckCode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.Ack(context.Background(), "client-1", "device-1", tt.req)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Ack() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServicePushPeerRequiresMatchingSession(t *testing.T) {
	conn := &fakeConnection{}
	service := NewService(
		fakeSessions{session: &session.Session{SessionID: "session-1", ConnID: 7, ClientID: "client-1", DeviceID: "device-1"}},
		fakeConnections{conn: conn},
	)
	service.now = func() time.Time { return time.UnixMilli(1760000000000) }

	resp, err := service.PushPeer(context.Background(), PeerPushRequest{
		OriginNode: "gateway-b",
		ClientID:   "client-1",
		DeviceID:   "device-1",
		SessionID:  "session-1",
		MsgID:      2001,
		MessageID:  "message-1",
		TraceID:    "trace-1",
		Body:       []byte("hello"),
	}, "gateway-a")
	if err != nil {
		t.Fatalf("PushPeer() error = %v", err)
	}
	if resp.GatewayNode != "gateway-a" || resp.SessionID != "session-1" {
		t.Fatalf("PushPeer() response = %+v", resp)
	}
	if conn.msgID != 2001 || len(conn.data) == 0 {
		t.Fatalf("connection send = msgID:%d data:%d", conn.msgID, len(conn.data))
	}
}

func TestServicePushPeerRejectsSessionMismatch(t *testing.T) {
	service := NewService(
		fakeSessions{session: &session.Session{SessionID: "session-new", ConnID: 7, ClientID: "client-1", DeviceID: "device-1"}},
		fakeConnections{conn: &fakeConnection{}},
	)

	_, err := service.PushPeer(context.Background(), PeerPushRequest{
		ClientID:  "client-1",
		DeviceID:  "device-1",
		SessionID: "session-old",
		MsgID:     2001,
	}, "gateway-a")
	if !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("PushPeer() error = %v, want %v", err, ErrSessionMismatch)
	}
}

func TestServicePushPeerValidation(t *testing.T) {
	service := NewService(fakeSessions{}, fakeConnections{})

	tests := []struct {
		name string
		req  PeerPushRequest
		want error
	}{
		{name: "missing client", req: PeerPushRequest{DeviceID: "d1", SessionID: "s1", MsgID: 1}, want: ErrMissingClientID},
		{name: "missing device", req: PeerPushRequest{ClientID: "c1", SessionID: "s1", MsgID: 1}, want: ErrMissingDeviceID},
		{name: "missing session", req: PeerPushRequest{ClientID: "c1", DeviceID: "d1", MsgID: 1}, want: ErrMissingSessionID},
		{name: "missing msg id", req: PeerPushRequest{ClientID: "c1", DeviceID: "d1", SessionID: "s1"}, want: ErrInvalidMsgID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.PushPeer(context.Background(), tt.req, "gateway-a")
			if !errors.Is(err, tt.want) {
				t.Fatalf("PushPeer() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServicePushValidation(t *testing.T) {
	service := NewService(fakeSessions{}, fakeConnections{})

	tests := []struct {
		name string
		req  PushRequest
		want error
	}{
		{name: "missing client", req: PushRequest{DeviceID: "d1", MsgID: 1}, want: ErrMissingClientID},
		{name: "missing device", req: PushRequest{ClientID: "c1", MsgID: 1}, want: ErrMissingDeviceID},
		{name: "missing msg id", req: PushRequest{ClientID: "c1", DeviceID: "d1"}, want: ErrInvalidMsgID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.Push(context.Background(), tt.req)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Push() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServicePushSessionNotFound(t *testing.T) {
	service := NewService(fakeSessions{}, fakeConnections{})

	_, err := service.Push(context.Background(), PushRequest{ClientID: "c1", DeviceID: "d1", MsgID: 1})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Push() error = %v, want %v", err, ErrSessionNotFound)
	}
}

func TestServicePushConnectionNotFound(t *testing.T) {
	service := NewService(
		fakeSessions{session: &session.Session{ConnID: 7, ClientID: "c1", DeviceID: "d1"}},
		fakeConnections{err: errors.New("missing")},
	)

	_, err := service.Push(context.Background(), PushRequest{ClientID: "c1", DeviceID: "d1", MsgID: 1})
	if !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("Push() error = %v, want %v", err, ErrConnectionNotFound)
	}
}

type fakeSessions struct {
	session *session.Session
}

func (f fakeSessions) GetByClientDevice(clientID, deviceID string) (*session.Session, bool) {
	if f.session == nil || f.session.ClientID != clientID || f.session.DeviceID != deviceID {
		return nil, false
	}

	return f.session.Clone(), true
}

type fakeConnections struct {
	conn *fakeConnection
	err  error
}

func (f fakeConnections) Get(_ uint64) (Connection, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.conn == nil {
		return nil, errors.New("missing")
	}

	return f.conn, nil
}

type fakeConnection struct {
	msgID uint32
	data  []byte
	err   error
}

func (f *fakeConnection) SendMsg(msgID uint32, data []byte) error {
	if f.err != nil {
		return f.err
	}

	f.msgID = msgID
	f.data = append([]byte(nil), data...)
	return nil
}
