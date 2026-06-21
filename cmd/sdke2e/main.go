package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	sdkbackend "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
	sdkclient "github.com/qiuyier/Z-Courier/pkg/sdk/client"
	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

type config struct {
	TCPAddress    string
	InternalURL   string
	InternalToken string
	ClientID      string
	DeviceID      string
	Token         string
	UpstreamMsgID uint32
	Timeout       time.Duration
}

func main() {
	configuration := parseConfig()
	ctx, cancel := context.WithTimeout(context.Background(), configuration.Timeout)
	defer cancel()

	if err := run(ctx, configuration); err != nil {
		fmt.Fprintf(os.Stderr, "sdk e2e failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("sdk e2e passed")
}

func parseConfig() config {
	var configuration config
	flag.StringVar(&configuration.TCPAddress, "tcp-address", "127.0.0.1:9899", "gateway TCP address")
	flag.StringVar(&configuration.InternalURL, "internal-url", "http://127.0.0.1:18082", "gateway internal HTTP URL")
	flag.StringVar(&configuration.InternalToken, "internal-token", "dev-internal-token", "gateway internal API token")
	flag.StringVar(&configuration.ClientID, "client-id", "e2e-client", "authenticated client ID")
	flag.StringVar(&configuration.DeviceID, "device-id", "sdk-e2e-device", "device ID")
	flag.StringVar(&configuration.Token, "token", "e2e-token", "client authentication token")
	upstreamMsgID := flag.Uint("upstream-msg-id", 2001, "business upstream MsgID")
	flag.DurationVar(&configuration.Timeout, "timeout", 30*time.Second, "overall verification timeout")
	flag.Parse()
	if uint64(*upstreamMsgID) > uint64(^uint32(0)) {
		fmt.Fprintf(os.Stderr, "invalid -upstream-msg-id %d: exceeds uint32\n", *upstreamMsgID)
		os.Exit(2)
	}
	configuration.UpstreamMsgID = uint32(*upstreamMsgID)
	return configuration
}

func run(ctx context.Context, configuration config) error {
	downlinks := make(chan *protocol.Packet, 8)
	downlinkErrors := make(chan error, 8)
	gateway, err := newGatewayClient(configuration, downlinks, downlinkErrors, true)
	if err != nil {
		return fmt.Errorf("create gateway client: %w", err)
	}
	defer gateway.Close()

	backend, err := sdkbackend.NewClient(sdkbackend.Config{
		BaseURL:       configuration.InternalURL,
		InternalToken: configuration.InternalToken,
	})
	if err != nil {
		return fmt.Errorf("create backend client: %w", err)
	}

	if err := gateway.Connect(ctx); err != nil {
		return fmt.Errorf("connect gateway client: %w", err)
	}
	initialBinding := gateway.Binding()
	fmt.Printf("sdk client bound: session_id=%s\n", initialBinding.SessionID)

	if err := verifyUpstream(ctx, gateway, configuration.UpstreamMsgID, "before-reconnect"); err != nil {
		return err
	}
	if err := verifyDownlink(ctx, backend, downlinks, downlinkErrors, configuration, "before-reconnect"); err != nil {
		return err
	}

	if err := verifyReconnect(ctx, gateway, configuration, initialBinding.SessionID); err != nil {
		return err
	}

	if err := verifyUpstream(ctx, gateway, configuration.UpstreamMsgID, "after-reconnect"); err != nil {
		return err
	}
	if err := verifyDownlink(ctx, backend, downlinks, downlinkErrors, configuration, "after-reconnect"); err != nil {
		return err
	}
	return nil
}

func newGatewayClient(
	configuration config,
	downlinks chan<- *protocol.Packet,
	downlinkErrors chan<- error,
	reconnect bool,
) (*sdkclient.Client, error) {
	clientConfig := sdkclient.Config{
		Address:  configuration.TCPAddress,
		ClientID: configuration.ClientID,
		DeviceID: configuration.DeviceID,
		Token:    configuration.Token,
	}
	if downlinks != nil {
		clientConfig.DownlinkHandler = func(ctx context.Context, packet *protocol.Packet) error {
			select {
			case downlinks <- packet:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if downlinkErrors != nil {
		clientConfig.OnDownlinkError = func(err error) {
			select {
			case downlinkErrors <- err:
			default:
			}
		}
	}
	if reconnect {
		clientConfig.Reconnect = &sdkclient.ReconnectConfig{
			InitialDelay: 500 * time.Millisecond,
			MaxDelay:     time.Second,
			Multiplier:   2,
			MaxAttempts:  10,
		}
	}
	return sdkclient.New(clientConfig)
}

func verifyUpstream(ctx context.Context, gateway *sdkclient.Client, msgID uint32, phase string) error {
	messageID := fmt.Sprintf("sdk-e2e-upstream-%s-%d", phase, time.Now().UnixNano())
	result, err := gateway.Send(ctx, sdkclient.SendRequest{
		MsgID:       msgID,
		Body:        []byte("sdk-e2e-" + phase),
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: true,
	})
	if err != nil {
		return fmt.Errorf("%s upstream: %w", phase, err)
	}
	if result.Ack == nil || result.Ack.Code != protocol.AckAccepted {
		return fmt.Errorf("%s upstream: unexpected ACK %#v", phase, result.Ack)
	}
	fmt.Printf("sdk upstream accepted: phase=%s message_id=%s\n", phase, messageID)
	return nil
}

func verifyDownlink(
	ctx context.Context,
	backend *sdkbackend.Client,
	downlinks <-chan *protocol.Packet,
	downlinkErrors <-chan error,
	configuration config,
	phase string,
) error {
	messageID := fmt.Sprintf("sdk-e2e-downlink-%s-%d", phase, time.Now().UnixNano())
	body := []byte("sdk-e2e-downlink-" + phase)
	response, err := backend.Push(ctx, sdkbackend.PushRequest{
		ClientID:    configuration.ClientID,
		DeviceID:    configuration.DeviceID,
		MsgID:       2001,
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: true,
		Body:        body,
	})
	if err != nil {
		return fmt.Errorf("%s downlink push: %w", phase, err)
	}
	if response.MessageID != messageID {
		return fmt.Errorf("%s downlink push: message ID = %q, want %q", phase, response.MessageID, messageID)
	}

	for {
		select {
		case packet := <-downlinks:
			if packet.MessageID != messageID {
				return fmt.Errorf("%s downlink: message ID = %q, want %q", phase, packet.MessageID, messageID)
			}
			if !bytes.Equal(packet.Body, body) {
				return fmt.Errorf("%s downlink: body = %q, want %q", phase, packet.Body, body)
			}
			if err := waitDelivered(ctx, backend, messageID); err != nil {
				return fmt.Errorf("%s downlink ACK: %w", phase, err)
			}
			fmt.Printf("sdk downlink delivered: phase=%s message_id=%s\n", phase, messageID)
			return nil
		case err := <-downlinkErrors:
			return fmt.Errorf("%s downlink handler: %w", phase, err)
		case <-ctx.Done():
			return fmt.Errorf("%s downlink: %w", phase, ctx.Err())
		}
	}
}

func waitDelivered(ctx context.Context, backend *sdkbackend.Client, messageID string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := backend.GetMessage(ctx, messageID)
		if err != nil {
			return err
		}
		switch status.Status {
		case sdkbackend.MessageStatusDelivered:
			return nil
		case sdkbackend.MessageStatusFailed, sdkbackend.MessageStatusDiscarded:
			return fmt.Errorf("message entered terminal status %q: %s", status.Status, status.LastError)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func verifyReconnect(ctx context.Context, gateway *sdkclient.Client, configuration config, initialSessionID string) error {
	replacement, err := newGatewayClient(configuration, nil, nil, false)
	if err != nil {
		return fmt.Errorf("create replacement client: %w", err)
	}
	defer replacement.Close()
	if err := replacement.Connect(ctx); err != nil {
		return fmt.Errorf("connect replacement client: %w", err)
	}

	if err := waitNotReady(ctx, gateway); err != nil {
		return fmt.Errorf("wait for replaced connection: %w", err)
	}
	if err := replacement.Close(); err != nil {
		return fmt.Errorf("close replacement client: %w", err)
	}
	if err := gateway.WaitReady(ctx); err != nil {
		return fmt.Errorf("wait for reconnect: %w", err)
	}

	reconnectedBinding := gateway.Binding()
	if reconnectedBinding.SessionID == "" || reconnectedBinding.SessionID == initialSessionID {
		return fmt.Errorf(
			"reconnect session ID = %q, want a value different from %q",
			reconnectedBinding.SessionID,
			initialSessionID,
		)
	}
	fmt.Printf(
		"sdk client reconnected: old_session_id=%s new_session_id=%s\n",
		initialSessionID,
		reconnectedBinding.SessionID,
	)
	return nil
}

func waitNotReady(ctx context.Context, gateway *sdkclient.Client) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for gateway.Ready() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}
