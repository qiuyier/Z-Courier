package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func TestDownlinkHandlerAutomaticallyAcknowledgesSuccess(t *testing.T) {
	handled := make(chan *protocol.Packet, 1)
	ackSent := make(chan struct{}, 1)
	client, serverDone := newBoundSendTestClient(t, Config{
		DownlinkHandler: func(_ context.Context, packet *protocol.Packet) error {
			handled <- packet
			return nil
		},
	}, func(connection net.Conn) error {
		if err := writePacketFrame(connection, testDownlink("auto-1"), 0); err != nil {
			return err
		}
		if err := readAndAcceptDeliveryAck(connection, "auto-1"); err != nil {
			return err
		}
		ackSent <- struct{}{}
		return waitForPeerClose(connection)
	})

	select {
	case packet := <-handled:
		if string(packet.Body) != "downlink" {
			t.Fatalf("handler body = %q", packet.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("downlink handler was not called")
	}
	<-ackSent
	waitForNoAckWaiters(t, client)
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestDownlinkHandlerFailureDoesNotAcknowledge(t *testing.T) {
	handlerFailure := errors.New("database unavailable")
	reported := make(chan error, 1)
	client, serverDone := newBoundSendTestClient(t, Config{
		DownlinkHandler: func(context.Context, *protocol.Packet) error {
			return handlerFailure
		},
		OnDownlinkError: func(err error) {
			reported <- err
		},
	}, func(connection net.Conn) error {
		if err := writePacketFrame(connection, testDownlink("failed-1"), 0); err != nil {
			return err
		}
		if err := connection.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			return err
		}
		_, err := readPacketFrame(connection, 0, 0)
		var networkError net.Error
		if !errors.As(err, &networkError) || !networkError.Timeout() {
			return fmt.Errorf("delivery ACK error = %v, want timeout", err)
		}
		return nil
	})

	select {
	case err := <-reported:
		if !errors.Is(err, ErrDownlinkHandler) || !errors.Is(err, handlerFailure) {
			t.Fatalf("OnDownlinkError() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler failure was not reported")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
	_ = client.Close()
}

func TestManualDownlinkAckWithRawReceive(t *testing.T) {
	ackSent := make(chan struct{}, 1)
	client, serverDone := newBoundSendTestClient(t, Config{}, func(connection net.Conn) error {
		if err := writePacketFrame(connection, testDownlink("manual-1"), 0); err != nil {
			return err
		}
		if err := readAndAcceptDeliveryAck(connection, "manual-1"); err != nil {
			return err
		}
		ackSent <- struct{}{}
		return waitForPeerClose(connection)
	})

	receiveContext, cancelReceive := context.WithTimeout(context.Background(), time.Second)
	packet, err := client.Receive(receiveContext)
	cancelReceive()
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	ack, err := client.AcknowledgeDownlink(context.Background(), packet)
	if err != nil {
		t.Fatalf("AcknowledgeDownlink() error = %v", err)
	}
	if ack.Code != protocol.AckAccepted || ack.MsgID != protocol.MsgIDDownlinkAck {
		t.Fatalf("AcknowledgeDownlink() ACK = %+v", ack)
	}
	<-ackSent
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestManualHandlerModeDoesNotAutomaticallyAcknowledge(t *testing.T) {
	handled := make(chan struct{}, 1)
	client, serverDone := newBoundSendTestClient(t, Config{
		ManualDownlinkAck: true,
		DownlinkHandler: func(context.Context, *protocol.Packet) error {
			handled <- struct{}{}
			return nil
		},
	}, func(connection net.Conn) error {
		if err := writePacketFrame(connection, testDownlink("manual-handler-1"), 0); err != nil {
			return err
		}
		if err := connection.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			return err
		}
		_, err := readPacketFrame(connection, 0, 0)
		var networkError net.Error
		if !errors.As(err, &networkError) || !networkError.Timeout() {
			return fmt.Errorf("delivery ACK error = %v, want timeout", err)
		}
		return nil
	})

	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("downlink handler was not called")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
	_ = client.Close()
}

func TestDownlinkDedupSkipsHandlerAndReacknowledges(t *testing.T) {
	var handlerCalls atomic.Int32
	ackSent := make(chan struct{}, 1)
	client, serverDone := newBoundSendTestClient(t, Config{
		DownlinkHandler: func(context.Context, *protocol.Packet) error {
			handlerCalls.Add(1)
			return nil
		},
	}, func(connection net.Conn) error {
		for range 2 {
			if err := writePacketFrame(connection, testDownlink("duplicate-1"), 0); err != nil {
				return err
			}
			if err := readAndAcceptDeliveryAck(connection, "duplicate-1"); err != nil {
				return err
			}
		}
		ackSent <- struct{}{}
		return waitForPeerClose(connection)
	})

	<-ackSent
	waitForNoAckWaiters(t, client)
	if calls := handlerCalls.Load(); calls != 1 {
		t.Fatalf("handler calls = %d, want 1", calls)
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestReceiveUnavailableWithDownlinkHandler(t *testing.T) {
	client, serverDone := newBoundSendTestClient(t, Config{
		DownlinkHandler: func(context.Context, *protocol.Packet) error { return nil },
	}, func(connection net.Conn) error {
		var buffer [1]byte
		_, err := connection.Read(buffer[:])
		return normalizeTestConnectionClose(err)
	})

	if _, err := client.Receive(context.Background()); !errors.Is(err, ErrReceiveUnavailable) {
		t.Fatalf("Receive() error = %v, want ErrReceiveUnavailable", err)
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestCloseCancelsDownlinkHandlerContext(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan error, 1)
	client, serverDone := newBoundSendTestClient(t, Config{
		DownlinkHandler: func(ctx context.Context, _ *protocol.Packet) error {
			started <- struct{}{}
			<-ctx.Done()
			canceled <- ctx.Err()
			return ctx.Err()
		},
	}, func(connection net.Conn) error {
		if err := writePacketFrame(connection, testDownlink("cancel-1"), 0); err != nil {
			return err
		}
		var buffer [1]byte
		_, err := connection.Read(buffer[:])
		return normalizeTestConnectionClose(err)
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("downlink handler was not called")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handler context error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("downlink handler context was not canceled")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestMessageDeduperEvictsLeastRecentlyUsed(t *testing.T) {
	deduper := newMessageDeduper(2)
	deduper.mark("one")
	deduper.mark("two")
	if !deduper.contains("one") {
		t.Fatal("deduper does not contain one")
	}
	deduper.mark("three")
	if deduper.contains("two") {
		t.Fatal("deduper retained least recently used entry")
	}
	if !deduper.contains("one") || !deduper.contains("three") {
		t.Fatal("deduper evicted a recent entry")
	}
}

func testDownlink(messageID string) *protocol.Packet {
	packet := protocol.NewPacket(2001, []byte("downlink"))
	packet.Flags = protocol.FlagAckRequired
	packet.ClientID = "canonical-client"
	packet.DeviceID = "device-1"
	packet.SessionID = "session-1"
	packet.MessageID = messageID
	packet.TraceID = "trace-" + messageID
	return packet
}

func readAndAcceptDeliveryAck(connection net.Conn, messageID string) error {
	packet, err := readPacketFrame(connection, 0, 0)
	if err != nil {
		return err
	}
	if packet.MsgID != protocol.MsgIDDownlinkAck || packet.MessageID == "" || packet.MessageID == messageID {
		return fmt.Errorf("unexpected delivery ACK packet: %+v", packet)
	}
	if packet.ClientID != "canonical-client" || packet.DeviceID != "device-1" || packet.SessionID != "session-1" || packet.Token != "token-1" {
		return fmt.Errorf("unexpected delivery ACK identity: %+v", packet)
	}
	deliveryAck, err := protocol.DecodeDeliveryAck(packet)
	if err != nil {
		return err
	}
	if deliveryAck.MessageID != messageID || deliveryAck.Code != protocol.DeliveryAckDelivered {
		return fmt.Errorf("unexpected delivery ACK body: %+v", deliveryAck)
	}
	return writeGatewayAck(connection, packet, protocol.AckAccepted, "")
}

func waitForPeerClose(connection net.Conn) error {
	var buffer [1]byte
	_, err := connection.Read(buffer[:])
	return normalizeTestConnectionClose(err)
}

func waitForNoAckWaiters(t *testing.T, client *Client) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		client.waitersMu.Lock()
		count := len(client.waiters)
		client.waitersMu.Unlock()
		if count == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("ACK waiter did not complete")
}
