package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func TestNewValidatesConfig(t *testing.T) {
	valid := Config{
		Address:  "gateway:8999",
		ClientID: "client-1",
		DeviceID: "device-1",
		Token:    "token-1",
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing address", mutate: func(config *Config) { config.Address = "" }},
		{name: "missing client ID", mutate: func(config *Config) { config.ClientID = "" }},
		{name: "missing device ID", mutate: func(config *Config) { config.DeviceID = "" }},
		{name: "missing credential", mutate: func(config *Config) { config.Token = "" }},
		{name: "conflicting credentials", mutate: func(config *Config) {
			config.TokenProvider = func(context.Context) (string, error) { return "other", nil }
		}},
		{name: "negative connect timeout", mutate: func(config *Config) { config.ConnectTimeout = -time.Second }},
		{name: "negative bind timeout", mutate: func(config *Config) { config.BindTimeout = -time.Second }},
		{name: "negative pending limit", mutate: func(config *Config) { config.MaxPendingBeforeReady = -1 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
			}
		})
	}

	client, err := New(valid)
	if err != nil {
		t.Fatalf("New(valid) error = %v", err)
	}
	if client.State() != StateDisconnected || client.Ready() {
		t.Fatalf("new client state = %s ready=%t", client.State(), client.Ready())
	}
}

func TestConnectBindsAndBuffersPacketsArrivingBeforeAck(t *testing.T) {
	var dialCount atomic.Int32
	serverDone := make(chan error, 1)
	dialer := dialerFunc(func(context.Context, string, string) (net.Conn, error) {
		dialCount.Add(1)
		clientConnection, serverConnection := net.Pipe()
		go func() {
			serverDone <- serveAcceptedBind(serverConnection, true)
		}()
		return clientConnection, nil
	})

	var tokenCalls atomic.Int32
	client, err := New(Config{
		Address:  "pipe:8999",
		ClientID: "claimed-client",
		DeviceID: "device-1",
		TokenProvider: func(context.Context) (string, error) {
			tokenCalls.Add(1)
			return "token-1", nil
		},
		Dialer: dialer,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if !client.Ready() || client.State() != StateReady {
		t.Fatalf("connected client state = %s ready=%t", client.State(), client.Ready())
	}
	if binding := client.Binding(); binding != (Binding{ClientID: "canonical-client", DeviceID: "device-1", SessionID: "session-1"}) {
		t.Fatalf("Binding() = %+v", binding)
	}
	client.mu.RLock()
	pendingCount := len(client.pendingBeforeReady)
	client.mu.RUnlock()
	if pendingCount != 1 {
		t.Fatalf("pending before ready = %d, want 1", pendingCount)
	}

	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("second Connect() error = %v", err)
	}
	if dialCount.Load() != 1 || tokenCalls.Load() != 1 {
		t.Fatalf("dial calls = %d token calls = %d, want 1 and 1", dialCount.Load(), tokenCalls.Load())
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if client.State() != StateClosed {
		t.Fatalf("closed state = %s", client.State())
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := client.Connect(context.Background()); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("Connect() after Close error = %v, want ErrClientClosed", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("bind server error = %v", err)
	}
}

func TestConnectMapsBindRejections(t *testing.T) {
	tests := []struct {
		name string
		code protocol.AckCode
		want error
	}{
		{name: "unauthorized", code: protocol.AckUnauthorized, want: ErrAuthenticationFailed},
		{name: "auth unavailable", code: protocol.AckAuthUnavailable, want: ErrAuthenticationUnavailable},
		{name: "rejected", code: protocol.AckRejected, want: ErrBindRejected},
		{name: "decode failed", code: protocol.AckDecodeFailed, want: ErrBindRejected},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serverDone := make(chan error, 1)
			dialer := oneShotPipeDialer(serverDone, func(connection net.Conn) error {
				defer connection.Close()
				packet, err := readPacketFrame(connection, 0, 0)
				if err != nil {
					return err
				}
				return writeBindAck(connection, packet, test.code, "gateway reason", "session-1")
			})
			client := newTestClient(t, dialer, Config{})

			err := client.Connect(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("Connect() error = %v, want %v", err, test.want)
			}
			var bindError *BindError
			if !errors.As(err, &bindError) || bindError.Code != test.code || bindError.Reason != "gateway reason" {
				t.Fatalf("BindError = %+v", bindError)
			}
			if client.State() != StateDisconnected {
				t.Fatalf("failed bind state = %s", client.State())
			}
			if serverErr := <-serverDone; serverErr != nil {
				t.Fatalf("bind server error = %v", serverErr)
			}
		})
	}
}

func TestConnectRejectsUnexpectedBindAck(t *testing.T) {
	tests := []struct {
		name    string
		respond func(net.Conn, *protocol.Packet) error
	}{
		{
			name: "accepted without session",
			respond: func(connection net.Conn, packet *protocol.Packet) error {
				return writeBindAck(connection, packet, protocol.AckAccepted, "", "")
			},
		},
		{
			name: "wrong acknowledged msg ID",
			respond: func(connection net.Conn, packet *protocol.Packet) error {
				origin := *packet
				origin.MsgID = 2001
				return writeBindAck(connection, &origin, protocol.AckAccepted, "", "session-1")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serverDone := make(chan error, 1)
			dialer := oneShotPipeDialer(serverDone, func(connection net.Conn) error {
				defer connection.Close()
				packet, err := readPacketFrame(connection, 0, 0)
				if err != nil {
					return err
				}
				return test.respond(connection, packet)
			})
			client := newTestClient(t, dialer, Config{})

			if err := client.Connect(context.Background()); !errors.Is(err, ErrUnexpectedBindAck) {
				t.Fatalf("Connect() error = %v, want ErrUnexpectedBindAck", err)
			}
			if serverErr := <-serverDone; serverErr != nil {
				t.Fatalf("bind server error = %v", serverErr)
			}
		})
	}
}

func TestConnectBindTimeout(t *testing.T) {
	serverDone := make(chan error, 1)
	dialer := oneShotPipeDialer(serverDone, func(connection net.Conn) error {
		defer connection.Close()
		if _, err := readPacketFrame(connection, 0, 0); err != nil {
			return err
		}
		var data [1]byte
		_, err := connection.Read(data[:])
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	})
	client := newTestClient(t, dialer, Config{BindTimeout: 30 * time.Millisecond})

	if err := client.Connect(context.Background()); !errors.Is(err, ErrBindTimeout) {
		t.Fatalf("Connect() error = %v, want ErrBindTimeout", err)
	}
	if client.State() != StateDisconnected {
		t.Fatalf("timeout state = %s", client.State())
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("bind server error = %v", serverErr)
	}
}

func TestConnectTimeout(t *testing.T) {
	dialer := dialerFunc(func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	client := newTestClient(t, dialer, Config{ConnectTimeout: 20 * time.Millisecond})

	if err := client.Connect(context.Background()); !errors.Is(err, ErrConnectTimeout) {
		t.Fatalf("Connect() error = %v, want ErrConnectTimeout", err)
	}
	if client.State() != StateDisconnected {
		t.Fatalf("connect timeout state = %s", client.State())
	}
}

func TestCloseInterruptsActiveBind(t *testing.T) {
	bindReceived := make(chan struct{})
	serverDone := make(chan error, 1)
	dialer := oneShotPipeDialer(serverDone, func(connection net.Conn) error {
		defer connection.Close()
		if _, err := readPacketFrame(connection, 0, 0); err != nil {
			return err
		}
		close(bindReceived)
		var data [1]byte
		_, err := connection.Read(data[:])
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	})
	client := newTestClient(t, dialer, Config{})
	connectDone := make(chan error, 1)
	go func() {
		connectDone <- client.Connect(context.Background())
	}()

	select {
	case <-bindReceived:
	case <-time.After(time.Second):
		t.Fatal("server did not receive bind")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-connectDone:
		if !errors.Is(err, ErrClientClosed) {
			t.Fatalf("Connect() error = %v, want ErrClientClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Connect() was not interrupted")
	}
	if client.State() != StateClosed {
		t.Fatalf("closed state = %s", client.State())
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("bind server error = %v", serverErr)
	}
}

func TestConnectTokenProviderFailure(t *testing.T) {
	providerError := errors.New("identity provider failed")
	var dialed atomic.Bool
	client, err := New(Config{
		Address:  "gateway:8999",
		ClientID: "client-1",
		DeviceID: "device-1",
		TokenProvider: func(context.Context) (string, error) {
			return "", providerError
		},
		Dialer: dialerFunc(func(context.Context, string, string) (net.Conn, error) {
			dialed.Store(true)
			return nil, errors.New("unexpected dial")
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	connectErr := client.Connect(context.Background())
	if !errors.Is(connectErr, ErrTokenUnavailable) || !errors.Is(connectErr, providerError) {
		t.Fatalf("Connect() error = %v", connectErr)
	}
	if dialed.Load() {
		t.Fatal("dialer called after token provider failure")
	}
}

func TestConnectPendingBeforeReadyOverflow(t *testing.T) {
	serverDone := make(chan error, 1)
	dialer := oneShotPipeDialer(serverDone, func(connection net.Conn) error {
		defer connection.Close()
		bind, err := readPacketFrame(connection, 0, 0)
		if err != nil {
			return err
		}
		for index := range 2 {
			packet := protocol.NewPacket(2001, []byte("pending"))
			packet.MessageID = fmt.Sprintf("pending-%d", index)
			if err := writePacketFrame(connection, packet, 0); err != nil {
				return err
			}
		}
		return writeBindAck(connection, bind, protocol.AckAccepted, "", "session-1")
	})
	client := newTestClient(t, dialer, Config{MaxPendingBeforeReady: 1})

	if err := client.Connect(context.Background()); !errors.Is(err, ErrPendingBeforeReadyOverflow) {
		t.Fatalf("Connect() error = %v, want ErrPendingBeforeReadyOverflow", err)
	}
	serverErr := <-serverDone
	if serverErr != nil && !errors.Is(serverErr, io.ErrClosedPipe) {
		t.Fatalf("bind server error = %v", serverErr)
	}
}

type dialerFunc func(context.Context, string, string) (net.Conn, error)

func (dialer dialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialer(ctx, network, address)
}

func oneShotPipeDialer(serverDone chan<- error, handler func(net.Conn) error) Dialer {
	return dialerFunc(func(context.Context, string, string) (net.Conn, error) {
		clientConnection, serverConnection := net.Pipe()
		go func() {
			serverDone <- handler(serverConnection)
		}()
		return clientConnection, nil
	})
}

func newTestClient(t *testing.T, dialer Dialer, overrides Config) *Client {
	t.Helper()
	config := Config{
		Address:  "pipe:8999",
		ClientID: "client-1",
		DeviceID: "device-1",
		Token:    "token-1",
		Dialer:   dialer,
	}
	if overrides.ConnectTimeout != 0 {
		config.ConnectTimeout = overrides.ConnectTimeout
	}
	if overrides.BindTimeout != 0 {
		config.BindTimeout = overrides.BindTimeout
	}
	if overrides.MaxPendingBeforeReady != 0 {
		config.MaxPendingBeforeReady = overrides.MaxPendingBeforeReady
	}
	client, err := New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func serveAcceptedBind(connection net.Conn, sendPending bool) error {
	defer connection.Close()
	bind, err := readPacketFrame(connection, 0, 0)
	if err != nil {
		return err
	}
	if bind.MsgID != protocol.MsgIDBind || bind.ClientID != "claimed-client" || bind.DeviceID != "device-1" || bind.Token != "token-1" {
		return fmt.Errorf("unexpected bind packet: %+v", bind)
	}
	if bind.Flags&protocol.FlagAckRequired == 0 || bind.MessageID == "" || bind.TraceID != bind.MessageID {
		return fmt.Errorf("incomplete bind metadata: %+v", bind)
	}
	if sendPending {
		pending := protocol.NewPacket(2001, []byte("pending-before-ack"))
		pending.ClientID = "canonical-client"
		pending.DeviceID = "device-1"
		pending.SessionID = "session-1"
		pending.MessageID = "pending-1"
		if err := writePacketFrame(connection, pending, 0); err != nil {
			return err
		}
	}
	if err := writeBindAck(connection, bind, protocol.AckAccepted, "", "session-1"); err != nil {
		return err
	}
	var data [1]byte
	_, err = connection.Read(data[:])
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func writeBindAck(connection net.Conn, bind *protocol.Packet, code protocol.AckCode, reason, sessionID string) error {
	ack, err := protocol.NewAckPacket(bind, code, reason)
	if err != nil {
		return err
	}
	ack.ClientID = "canonical-client"
	ack.DeviceID = bind.DeviceID
	ack.SessionID = sessionID
	return writePacketFrame(connection, ack, 0)
}
