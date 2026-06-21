package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func TestSendReceivesImmediateAck(t *testing.T) {
	client, serverDone := newBoundSendTestClient(t, Config{}, func(connection net.Conn) error {
		packet, err := readPacketFrame(connection, 0, 0)
		if err != nil {
			return err
		}
		if err := validateBusinessPacket(packet, "message-1"); err != nil {
			return err
		}
		return writeGatewayAck(connection, packet, protocol.AckAccepted, "")
	})

	result, err := client.Send(context.Background(), SendRequest{
		MsgID:       2001,
		Body:        []byte("hello"),
		MessageID:   "message-1",
		AckRequired: true,
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.MessageID != "message-1" || result.Ack == nil || result.Ack.Code != protocol.AckAccepted {
		t.Fatalf("Send() result = %+v", result)
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestSendWaitsWhenAckRequiredFlagIsSet(t *testing.T) {
	client, serverDone := newBoundSendTestClient(t, Config{}, func(connection net.Conn) error {
		packet, err := readPacketFrame(connection, 0, 0)
		if err != nil {
			return err
		}
		return writeGatewayAck(connection, packet, protocol.AckAccepted, "")
	})

	result, err := client.Send(context.Background(), SendRequest{
		MsgID:     2001,
		MessageID: "flag-ack",
		Flags:     protocol.FlagAckRequired,
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.Ack == nil || result.Ack.MessageID != "flag-ack" {
		t.Fatalf("Send() result = %+v", result)
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestConcurrentSendCorrelatesOutOfOrderAcks(t *testing.T) {
	const messageCount = 24
	client, serverDone := newBoundSendTestClient(t, Config{}, func(connection net.Conn) error {
		packets := make([]*protocol.Packet, 0, messageCount)
		for range messageCount {
			packet, err := readPacketFrame(connection, 0, 0)
			if err != nil {
				return err
			}
			if err := validateBusinessPacket(packet, packet.MessageID); err != nil {
				return err
			}
			packets = append(packets, packet)
		}
		for index := len(packets) - 1; index >= 0; index-- {
			if err := writeGatewayAck(connection, packets[index], protocol.AckAccepted, ""); err != nil {
				return err
			}
		}
		return nil
	})

	type sendOutcome struct {
		messageID string
		result    SendResult
		err       error
	}
	outcomes := make(chan sendOutcome, messageCount)
	var waitGroup sync.WaitGroup
	for index := range messageCount {
		messageID := fmt.Sprintf("message-%02d", index)
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := client.Send(context.Background(), SendRequest{
				MsgID:       2001,
				Body:        []byte(messageID),
				MessageID:   messageID,
				AckRequired: true,
			})
			outcomes <- sendOutcome{messageID: messageID, result: result, err: err}
		}()
	}
	waitGroup.Wait()
	close(outcomes)

	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("Send(%s) error = %v", outcome.messageID, outcome.err)
		}
		if outcome.result.MessageID != outcome.messageID || outcome.result.Ack == nil || outcome.result.Ack.MessageID != outcome.messageID {
			t.Fatalf("Send(%s) result = %+v", outcome.messageID, outcome.result)
		}
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestSendMapsRejectedAck(t *testing.T) {
	client, serverDone := newBoundSendTestClient(t, Config{}, func(connection net.Conn) error {
		packet, err := readPacketFrame(connection, 0, 0)
		if err != nil {
			return err
		}
		return writeGatewayAck(connection, packet, protocol.AckRejected, "route overloaded")
	})

	_, err := client.Send(context.Background(), SendRequest{
		MsgID:       2001,
		MessageID:   "rejected-1",
		AckRequired: true,
	})
	if !errors.Is(err, ErrAckRejected) {
		t.Fatalf("Send() error = %v, want ErrAckRejected", err)
	}
	var ackError *AckError
	if !errors.As(err, &ackError) || ackError.Ack.Reason != "route overloaded" {
		t.Fatalf("AckError = %+v", ackError)
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestSendRejectsDuplicatePendingMessageID(t *testing.T) {
	firstReceived := make(chan *protocol.Packet, 1)
	releaseAck := make(chan struct{})
	client, serverDone := newBoundSendTestClient(t, Config{}, func(connection net.Conn) error {
		packet, err := readPacketFrame(connection, 0, 0)
		if err != nil {
			return err
		}
		firstReceived <- packet
		<-releaseAck
		return writeGatewayAck(connection, packet, protocol.AckAccepted, "")
	})

	firstDone := make(chan error, 1)
	go func() {
		_, err := client.Send(context.Background(), SendRequest{
			MsgID:       2001,
			MessageID:   "duplicate-1",
			AckRequired: true,
		})
		firstDone <- err
	}()
	select {
	case <-firstReceived:
	case <-time.After(time.Second):
		t.Fatal("gateway did not receive first message")
	}

	_, err := client.Send(context.Background(), SendRequest{
		MsgID:       2001,
		MessageID:   "duplicate-1",
		AckRequired: true,
	})
	if !errors.Is(err, ErrDuplicateMessageID) {
		t.Fatalf("second Send() error = %v, want ErrDuplicateMessageID", err)
	}
	close(releaseAck)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestSendAckTimeoutRemovesWaiter(t *testing.T) {
	messageReceived := make(chan struct{})
	client, serverDone := newBoundSendTestClient(t, Config{AckTimeout: 25 * time.Millisecond}, func(connection net.Conn) error {
		if _, err := readPacketFrame(connection, 0, 0); err != nil {
			return err
		}
		close(messageReceived)
		var data [1]byte
		_, err := connection.Read(data[:])
		return normalizeTestConnectionClose(err)
	})

	_, err := client.Send(context.Background(), SendRequest{
		MsgID:       2001,
		MessageID:   "timeout-1",
		AckRequired: true,
	})
	if !errors.Is(err, ErrAckTimeout) {
		t.Fatalf("Send() error = %v, want ErrAckTimeout", err)
	}
	<-messageReceived
	client.waitersMu.Lock()
	waiterCount := len(client.waiters)
	client.waitersMu.Unlock()
	if waiterCount != 0 {
		t.Fatalf("pending ACK waiters = %d, want 0", waiterCount)
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestConnectionCloseFailsPendingSend(t *testing.T) {
	client, serverDone := newBoundSendTestClient(t, Config{}, func(connection net.Conn) error {
		if _, err := readPacketFrame(connection, 0, 0); err != nil {
			return err
		}
		return connection.Close()
	})

	_, err := client.Send(context.Background(), SendRequest{
		MsgID:       2001,
		MessageID:   "disconnect-1",
		AckRequired: true,
	})
	if !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("Send() error = %v, want ErrConnectionClosed", err)
	}
	if err := <-serverDone; err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestSendValidation(t *testing.T) {
	client := newTestClient(t, dialerFunc(func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("not used")
	}), Config{})
	if _, err := client.Send(context.Background(), SendRequest{MsgID: 2001}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Send() before Connect error = %v, want ErrNotReady", err)
	}
	if _, err := client.Send(context.Background(), SendRequest{MsgID: protocol.MsgIDBind}); !errors.Is(err, ErrReservedMsgID) {
		t.Fatalf("Send(reserved) error = %v, want ErrReservedMsgID", err)
	}
}

func TestSendWithoutAckReturnsAfterWrite(t *testing.T) {
	received := make(chan *protocol.Packet, 1)
	client, serverDone := newBoundSendTestClient(t, Config{}, func(connection net.Conn) error {
		packet, err := readPacketFrame(connection, 0, 0)
		if err != nil {
			return err
		}
		received <- packet
		var data [1]byte
		_, err = connection.Read(data[:])
		return normalizeTestConnectionClose(err)
	})

	result, err := client.Send(context.Background(), SendRequest{MsgID: 2001, Body: []byte("fire-and-forget")})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.MessageID == "" || result.Ack != nil {
		t.Fatalf("Send() result = %+v", result)
	}
	packet := <-received
	if packet.MessageID != result.MessageID || packet.Flags&protocol.FlagAckRequired != 0 {
		t.Fatalf("sent packet = %+v", packet)
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestReceiveAfterReady(t *testing.T) {
	client, serverDone := newBoundSendTestClient(t, Config{}, func(connection net.Conn) error {
		downlink := protocol.NewPacket(2001, []byte("downlink"))
		downlink.ClientID = "canonical-client"
		downlink.DeviceID = "device-1"
		downlink.SessionID = "session-1"
		downlink.MessageID = "downlink-1"
		if err := writePacketFrame(connection, downlink, 0); err != nil {
			return err
		}
		var data [1]byte
		_, err := connection.Read(data[:])
		return normalizeTestConnectionClose(err)
	})

	receiveCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	packet, err := client.Receive(receiveCtx)
	cancel()
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if packet.MessageID != "downlink-1" || string(packet.Body) != "downlink" {
		t.Fatalf("Receive() packet = %+v", packet)
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestInboundOverflowDisconnectsClient(t *testing.T) {
	client, serverDone := newBoundSendTestClient(t, Config{InboundBuffer: 1}, func(connection net.Conn) error {
		for index := range 2 {
			packet := protocol.NewPacket(2001, []byte("downlink"))
			packet.MessageID = fmt.Sprintf("overflow-%d", index)
			if err := writePacketFrame(connection, packet, 0); err != nil {
				return err
			}
		}
		return nil
	})

	deadline := time.Now().Add(time.Second)
	for client.State() != StateDisconnected && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if client.State() != StateDisconnected {
		t.Fatalf("state = %s, want disconnected", client.State())
	}
	if !errors.Is(client.LastError(), ErrInboundOverflow) || !errors.Is(client.LastError(), ErrConnectionClosed) {
		t.Fatalf("LastError() = %v", client.LastError())
	}
	if err := <-serverDone; err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("gateway server error = %v", err)
	}
}

func newBoundSendTestClient(t *testing.T, overrides Config, handler func(net.Conn) error) (*Client, <-chan error) {
	t.Helper()
	serverDone := make(chan error, 1)
	dialer := oneShotPipeDialer(serverDone, func(connection net.Conn) error {
		defer connection.Close()
		bind, err := readPacketFrame(connection, 0, 0)
		if err != nil {
			return err
		}
		if err := writeBindAck(connection, bind, protocol.AckAccepted, "", "session-1"); err != nil {
			return err
		}
		return handler(connection)
	})
	client := newTestClient(t, dialer, overrides)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	return client, serverDone
}

func validateBusinessPacket(packet *protocol.Packet, messageID string) error {
	if packet.MsgID != 2001 || packet.MessageID != messageID {
		return fmt.Errorf("unexpected business packet: %+v", packet)
	}
	if packet.ClientID != "canonical-client" || packet.DeviceID != "device-1" || packet.SessionID != "session-1" || packet.Token != "token-1" {
		return fmt.Errorf("unexpected business identity: %+v", packet)
	}
	if packet.Flags&protocol.FlagAckRequired == 0 || packet.TraceID == "" || packet.Seq == 0 || packet.Timestamp == 0 {
		return fmt.Errorf("incomplete business metadata: %+v", packet)
	}
	return nil
}

func writeGatewayAck(connection net.Conn, origin *protocol.Packet, code protocol.AckCode, reason string) error {
	ack, err := protocol.NewAckPacket(origin, code, reason)
	if err != nil {
		return err
	}
	return writePacketFrame(connection, ack, 0)
}

func normalizeTestConnectionClose(err error) error {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
