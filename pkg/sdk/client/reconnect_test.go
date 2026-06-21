package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func TestReconnectConfigValidation(t *testing.T) {
	valid := Config{
		Address:  "gateway:8999",
		ClientID: "client-1",
		DeviceID: "device-1",
		Token:    "token-1",
	}
	tests := []struct {
		name      string
		reconnect ReconnectConfig
	}{
		{name: "negative initial delay", reconnect: ReconnectConfig{InitialDelay: -time.Second}},
		{name: "negative max delay", reconnect: ReconnectConfig{MaxDelay: -time.Second}},
		{name: "max below initial", reconnect: ReconnectConfig{InitialDelay: time.Second, MaxDelay: time.Millisecond}},
		{name: "multiplier below one", reconnect: ReconnectConfig{Multiplier: 0.5}},
		{name: "negative jitter", reconnect: ReconnectConfig{Jitter: -0.1}},
		{name: "jitter above one", reconnect: ReconnectConfig{Jitter: 1.1}},
		{name: "negative attempts", reconnect: ReconnectConfig{MaxAttempts: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			config.Reconnect = &test.reconnect
			if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestClientReconnectsAndWaitReady(t *testing.T) {
	var dialCalls atomic.Int32
	var tokenCalls atomic.Int32
	var bindMessageIDsMu sync.Mutex
	var bindMessageIDs []string
	firstDrop := make(chan struct{})
	secondBindReceived := make(chan struct{})
	releaseSecondBind := make(chan struct{})
	serverDone := make(chan error, 2)

	dialer := dialerFunc(func(context.Context, string, string) (net.Conn, error) {
		call := dialCalls.Add(1)
		clientConnection, serverConnection := net.Pipe()
		go func() {
			defer serverConnection.Close()
			bind, err := readPacketFrame(serverConnection, 0, 0)
			if err != nil {
				serverDone <- err
				return
			}
			bindMessageIDsMu.Lock()
			bindMessageIDs = append(bindMessageIDs, bind.MessageID)
			bindMessageIDsMu.Unlock()
			sessionID := fmt.Sprintf("session-%d", call)
			if call == 2 {
				close(secondBindReceived)
				<-releaseSecondBind
			}
			if err := writeBindAck(serverConnection, bind, protocol.AckAccepted, "", sessionID); err != nil {
				serverDone <- err
				return
			}
			if call == 1 {
				<-firstDrop
				serverDone <- nil
				return
			}
			serverDone <- waitForPeerClose(serverConnection)
		}()
		return clientConnection, nil
	})

	client, err := New(Config{
		Address:  "pipe:8999",
		ClientID: "client-1",
		DeviceID: "device-1",
		TokenProvider: func(context.Context) (string, error) {
			call := tokenCalls.Add(1)
			return fmt.Sprintf("token-%d", call), nil
		},
		Dialer: dialer,
		Reconnect: &ReconnectConfig{
			InitialDelay: time.Millisecond,
			MaxDelay:     2 * time.Millisecond,
			MaxAttempts:  3,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client.config.reconnect.jitter = 0
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	close(firstDrop)

	select {
	case <-secondBindReceived:
	case <-time.After(time.Second):
		t.Fatal("second bind did not start")
	}
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- client.WaitReady(context.Background())
	}()
	select {
	case err := <-waitDone:
		t.Fatalf("WaitReady() returned before bind completed: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(releaseSecondBind)
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("WaitReady() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitReady() did not observe reconnect")
	}
	if binding := client.Binding(); binding.SessionID != "session-2" {
		t.Fatalf("Binding() = %+v", binding)
	}
	if dialCalls.Load() != 2 || tokenCalls.Load() != 2 {
		t.Fatalf("dial calls = %d token calls = %d, want 2 and 2", dialCalls.Load(), tokenCalls.Load())
	}
	bindMessageIDsMu.Lock()
	if len(bindMessageIDs) != 2 || bindMessageIDs[0] == bindMessageIDs[1] {
		t.Fatalf("bind message IDs = %v", bindMessageIDs)
	}
	bindMessageIDsMu.Unlock()

	_ = client.Close()
	for range 2 {
		if err := <-serverDone; err != nil {
			t.Fatalf("gateway server error = %v", err)
		}
	}
}

func TestReconnectStopsAfterMaxAttempts(t *testing.T) {
	transientFailure := errors.New("gateway unavailable")
	var dialCalls atomic.Int32
	firstDrop := make(chan struct{})
	serverDone := make(chan error, 1)
	dialer := dialerFunc(func(context.Context, string, string) (net.Conn, error) {
		if dialCalls.Add(1) > 1 {
			return nil, transientFailure
		}
		clientConnection, serverConnection := net.Pipe()
		go func() {
			defer serverConnection.Close()
			bind, err := readPacketFrame(serverConnection, 0, 0)
			if err == nil {
				err = writeBindAck(serverConnection, bind, protocol.AckAccepted, "", "session-1")
			}
			if err == nil {
				<-firstDrop
			}
			serverDone <- err
		}()
		return clientConnection, nil
	})

	client := newReconnectTestClient(t, dialer, 3)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	close(firstDrop)
	waitForState(t, client, func(state State) bool { return state != StateReady })
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	err := client.WaitReady(waitContext)
	cancel()
	if !errors.Is(err, ErrReconnectExhausted) || !errors.Is(err, transientFailure) {
		t.Fatalf("WaitReady() error = %v", err)
	}
	var reconnectError *ReconnectError
	if !errors.As(err, &reconnectError) || reconnectError.Attempts != 3 {
		t.Fatalf("ReconnectError = %+v", reconnectError)
	}
	if dialCalls.Load() != 4 {
		t.Fatalf("dial calls = %d, want initial + 3 reconnects", dialCalls.Load())
	}
	if client.State() != StateDisconnected || !errors.Is(client.LastError(), ErrReconnectExhausted) {
		t.Fatalf("state = %s last error = %v", client.State(), client.LastError())
	}
	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestReconnectStopsAfterAuthenticationRejection(t *testing.T) {
	var dialCalls atomic.Int32
	firstDrop := make(chan struct{})
	serverDone := make(chan error, 2)
	dialer := dialerFunc(func(context.Context, string, string) (net.Conn, error) {
		call := dialCalls.Add(1)
		if call > 2 {
			return nil, errors.New("unexpected reconnect")
		}
		clientConnection, serverConnection := net.Pipe()
		go func() {
			defer serverConnection.Close()
			bind, err := readPacketFrame(serverConnection, 0, 0)
			if err != nil {
				serverDone <- err
				return
			}
			if call == 1 {
				if err := writeBindAck(serverConnection, bind, protocol.AckAccepted, "", "session-1"); err != nil {
					serverDone <- err
					return
				}
				<-firstDrop
				serverDone <- nil
				return
			}
			if err := writeBindAck(serverConnection, bind, protocol.AckUnauthorized, "expired token", ""); err != nil {
				serverDone <- err
				return
			}
			serverDone <- waitForPeerClose(serverConnection)
		}()
		return clientConnection, nil
	})

	client := newReconnectTestClient(t, dialer, 0)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	close(firstDrop)
	waitForState(t, client, func(state State) bool { return state != StateReady })
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	err := client.WaitReady(waitContext)
	cancel()
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("WaitReady() error = %v, want ErrAuthenticationFailed", err)
	}
	if dialCalls.Load() != 2 || client.State() != StateDisconnected {
		t.Fatalf("dial calls = %d state = %s", dialCalls.Load(), client.State())
	}
	_ = client.Close()
	for range 2 {
		if err := <-serverDone; err != nil {
			t.Fatalf("gateway server error = %v", err)
		}
	}
}

func TestCloseCancelsReconnectBackoff(t *testing.T) {
	var dialCalls atomic.Int32
	firstDrop := make(chan struct{})
	unexpectedDial := make(chan struct{}, 1)
	serverDone := make(chan error, 1)
	dialer := dialerFunc(func(context.Context, string, string) (net.Conn, error) {
		if dialCalls.Add(1) > 1 {
			unexpectedDial <- struct{}{}
			return nil, errors.New("unexpected reconnect")
		}
		clientConnection, serverConnection := net.Pipe()
		go func() {
			defer serverConnection.Close()
			bind, err := readPacketFrame(serverConnection, 0, 0)
			if err == nil {
				err = writeBindAck(serverConnection, bind, protocol.AckAccepted, "", "session-1")
			}
			if err == nil {
				<-firstDrop
			}
			serverDone <- err
		}()
		return clientConnection, nil
	})

	client, err := New(Config{
		Address:   "pipe:8999",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		Token:     "token-1",
		Dialer:    dialer,
		Reconnect: &ReconnectConfig{InitialDelay: time.Hour, MaxDelay: time.Hour},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	close(firstDrop)
	waitForState(t, client, func(state State) bool { return state == StateReconnectWait })
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-unexpectedDial:
		t.Fatal("reconnect dial occurred after Close")
	case <-time.After(25 * time.Millisecond):
	}
	if err := client.WaitReady(context.Background()); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("WaitReady() error = %v, want ErrClientClosed", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("gateway server error = %v", err)
	}
}

func TestReconnectDoesNotReplayFailedSend(t *testing.T) {
	var dialCalls atomic.Int32
	secondReady := make(chan struct{})
	secondObservedNoReplay := make(chan struct{})
	serverDone := make(chan error, 2)
	dialer := dialerFunc(func(context.Context, string, string) (net.Conn, error) {
		call := dialCalls.Add(1)
		clientConnection, serverConnection := net.Pipe()
		go func() {
			defer serverConnection.Close()
			bind, err := readPacketFrame(serverConnection, 0, 0)
			if err != nil {
				serverDone <- err
				return
			}
			if err := writeBindAck(serverConnection, bind, protocol.AckAccepted, "", fmt.Sprintf("session-%d", call)); err != nil {
				serverDone <- err
				return
			}
			if call == 1 {
				_, err = readPacketFrame(serverConnection, 0, 0)
				serverDone <- err
				return
			}
			close(secondReady)
			if err := serverConnection.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
				serverDone <- err
				return
			}
			_, err = readPacketFrame(serverConnection, 0, 0)
			var networkError net.Error
			if !errors.As(err, &networkError) || !networkError.Timeout() {
				serverDone <- fmt.Errorf("replayed packet read error = %v, want timeout", err)
				return
			}
			close(secondObservedNoReplay)
			_ = serverConnection.SetReadDeadline(time.Time{})
			serverDone <- waitForPeerClose(serverConnection)
		}()
		return clientConnection, nil
	})

	client := newReconnectTestClient(t, dialer, 3)
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	_, err := client.Send(context.Background(), SendRequest{
		MsgID:       2001,
		MessageID:   "do-not-replay",
		AckRequired: true,
	})
	if !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("Send() error = %v, want ErrConnectionClosed", err)
	}
	select {
	case <-secondReady:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not become ready")
	}
	if err := client.WaitReady(context.Background()); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	select {
	case <-secondObservedNoReplay:
	case <-time.After(time.Second):
		t.Fatal("gateway did not complete replay observation")
	}
	_ = client.Close()
	for range 2 {
		if err := <-serverDone; err != nil {
			t.Fatalf("gateway server error = %v", err)
		}
	}
}

func TestReconnectDelay(t *testing.T) {
	config := normalizedReconnectConfig{
		initialDelay: 100 * time.Millisecond,
		maxDelay:     500 * time.Millisecond,
		multiplier:   2,
		jitter:       0.2,
	}
	if delay := reconnectDelay(config, 1, 0.5); delay != 100*time.Millisecond {
		t.Fatalf("attempt 1 delay = %v", delay)
	}
	if delay := reconnectDelay(config, 2, 0.5); delay != 200*time.Millisecond {
		t.Fatalf("attempt 2 delay = %v", delay)
	}
	if delay := reconnectDelay(config, 4, 0.5); delay != 500*time.Millisecond {
		t.Fatalf("attempt 4 delay = %v", delay)
	}
	if delay := reconnectDelay(config, 1, 0); delay != 80*time.Millisecond {
		t.Fatalf("negative jitter delay = %v", delay)
	}
}

func newReconnectTestClient(t *testing.T, dialer Dialer, maxAttempts int) *Client {
	t.Helper()
	client, err := New(Config{
		Address:  "pipe:8999",
		ClientID: "client-1",
		DeviceID: "device-1",
		Token:    "token-1",
		Dialer:   dialer,
		Reconnect: &ReconnectConfig{
			InitialDelay: time.Millisecond,
			MaxDelay:     2 * time.Millisecond,
			MaxAttempts:  maxAttempts,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client.config.reconnect.jitter = 0
	return client
}

func waitForState(t *testing.T, client *Client, predicate func(State) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if predicate(client.State()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("client state = %s", client.State())
}
