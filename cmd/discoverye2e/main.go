package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/qiuyier/Z-Courier/internal/resilience"
	sdkclient "github.com/qiuyier/Z-Courier/pkg/sdk/client"
	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

const (
	defaultGatewayAddress  = "127.0.0.1:9931"
	defaultGatewayReadyURL = "http://127.0.0.1:18191/readyz"
	defaultBackendAAddress = "127.0.0.1:18192"
	defaultBackendBAddress = "127.0.0.1:18193"
	defaultUpstreamToken   = "discovery-e2e-upstream-token"
	defaultClientID        = "discovery-e2e-client"
	defaultDeviceID        = "discovery-e2e-device"
	defaultClientToken     = "discovery-e2e-token"
	defaultUpstreamMsgID   = uint32(1101)
	defaultCooldown        = time.Second
)

type config struct {
	GatewayBin    string
	GatewayConfig string
	ZinxConfig    string
	GatewayLog    string
	Timeout       time.Duration
}

type verifier struct {
	client   *sdkclient.Client
	backendA *controlledBackend
	backendB *controlledBackend
}

func main() {
	configuration, err := parseConfig(os.Args[1:], os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discovery e2e configuration failed: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), configuration.Timeout)
	defer cancel()
	if err := run(ctx, configuration); err != nil {
		fmt.Fprintf(os.Stderr, "discovery e2e failed: %v\n", err)
		printGatewayLogTail(configuration.GatewayLog, 32<<10)
		os.Exit(1)
	}
	fmt.Println("discovery e2e passed")
}

func parseConfig(arguments []string, output io.Writer) (config, error) {
	var configuration config
	flags := flag.NewFlagSet("discoverye2e", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&configuration.GatewayBin, "gateway-bin", "", "path to a built gateway binary")
	flags.StringVar(
		&configuration.GatewayConfig,
		"gateway-config",
		"configs/z-courier.discovery-e2e.yaml",
		"path to the discovery E2E gateway config",
	)
	flags.StringVar(
		&configuration.ZinxConfig,
		"zinx-config",
		"conf/zinx.discovery-e2e.json",
		"path to the discovery E2E Zinx config",
	)
	flags.StringVar(
		&configuration.GatewayLog,
		"gateway-log",
		"log/e2e-discovery-gateway.log",
		"path for captured gateway process output",
	)
	flags.DurationVar(&configuration.Timeout, "timeout", 30*time.Second, "overall verification timeout")
	if err := flags.Parse(arguments); err != nil {
		return config{}, err
	}
	if configuration.GatewayBin == "" {
		return config{}, errors.New("-gateway-bin is required")
	}
	if configuration.Timeout <= 0 {
		return config{}, errors.New("-timeout must be greater than zero")
	}
	return configuration, nil
}

func run(ctx context.Context, configuration config) (runErr error) {
	backendA := newControlledBackend("backend-a", defaultBackendAAddress, defaultUpstreamToken)
	if err := backendA.Start(); err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, backendA.Close())
	}()

	backendB := newControlledBackend("backend-b", defaultBackendBAddress, defaultUpstreamToken)
	if err := backendB.Start(); err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, backendB.Close())
	}()

	gateway, err := startGatewayProcess(configuration)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, gateway.Stop())
	}()

	if err := waitForGatewayReady(ctx, gateway, defaultGatewayReadyURL); err != nil {
		return err
	}

	client, err := sdkclient.New(sdkclient.Config{
		Address:    defaultGatewayAddress,
		ClientID:   defaultClientID,
		DeviceID:   defaultDeviceID,
		Token:      defaultClientToken,
		AckTimeout: 3 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("create gateway client: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, client.Close())
	}()
	if err := client.Connect(ctx); err != nil {
		return fmt.Errorf("connect gateway client: %w", err)
	}

	check := &verifier{
		client:   client,
		backendA: backendA,
		backendB: backendB,
	}
	return check.Run(ctx)
}

func (check *verifier) Run(ctx context.Context) error {
	if err := check.verifyInitialRoundRobin(ctx); err != nil {
		return err
	}
	if err := check.verifyTransportFailover(ctx); err != nil {
		return err
	}
	if err := check.verifyCooldown(ctx); err != nil {
		return err
	}
	if err := waitFor(ctx, defaultCooldown+250*time.Millisecond); err != nil {
		return err
	}
	if err := check.verifyRecovery(ctx); err != nil {
		return err
	}
	if err := check.verifyResponseIsNotReplayed(ctx); err != nil {
		return err
	}
	return nil
}

func (check *verifier) verifyInitialRoundRobin(ctx context.Context) error {
	fmt.Println("checking initial round-robin selection")
	if err := check.sendAccepted(ctx, "discovery-e2e-round-robin-a", []byte("round-robin-a")); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, 1, check.backendB, 0); err != nil {
		return fmt.Errorf("initial endpoint A selection: %w", err)
	}
	if err := expectRecord(check.backendA, 0, "discovery-e2e-round-robin-a", []byte("round-robin-a")); err != nil {
		return err
	}

	if err := check.sendAccepted(ctx, "discovery-e2e-round-robin-b", []byte("round-robin-b")); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, 1, check.backendB, 1); err != nil {
		return fmt.Errorf("initial endpoint B selection: %w", err)
	}
	return expectRecord(check.backendB, 0, "discovery-e2e-round-robin-b", []byte("round-robin-b"))
}

func (check *verifier) verifyTransportFailover(ctx context.Context) error {
	fmt.Println("checking bounded transport failover and stable message identity")
	check.backendA.SetDropResponse()
	aBefore := check.backendA.RecordCount()
	bBefore := check.backendB.RecordCount()
	messageID := "discovery-e2e-transport-failover"
	body := []byte("same-body-across-attempts")

	if err := check.sendAccepted(ctx, messageID, body); err != nil {
		return err
	}
	check.backendA.SetHealthy()
	if err := expectCounts(check.backendA, aBefore+1, check.backendB, bBefore+1); err != nil {
		return fmt.Errorf("transport failover requests: %w", err)
	}

	firstAttempt, err := check.backendA.Record(aBefore)
	if err != nil {
		return err
	}
	secondAttempt, err := check.backendB.Record(bBefore)
	if err != nil {
		return err
	}
	if !bytes.Equal(firstAttempt.RawBody, secondAttempt.RawBody) {
		return errors.New("transport failover changed the serialized upstream request")
	}
	if err := validateRecord(firstAttempt, messageID, body); err != nil {
		return fmt.Errorf("first transport attempt: %w", err)
	}
	if err := validateRecord(secondAttempt, messageID, body); err != nil {
		return fmt.Errorf("second transport attempt: %w", err)
	}
	return nil
}

func (check *verifier) verifyCooldown(ctx context.Context) error {
	fmt.Println("checking failed endpoint cooldown")
	aBefore := check.backendA.RecordCount()
	bBefore := check.backendB.RecordCount()
	messageID := "discovery-e2e-cooldown"
	body := []byte("cooldown-skips-failed-endpoint")

	if err := check.sendAccepted(ctx, messageID, body); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, aBefore, check.backendB, bBefore+1); err != nil {
		return fmt.Errorf("endpoint cooldown: %w", err)
	}
	return expectRecord(check.backendB, bBefore, messageID, body)
}

func (check *verifier) verifyRecovery(ctx context.Context) error {
	fmt.Println("checking endpoint recovery after cooldown")
	aBefore := check.backendA.RecordCount()
	bBefore := check.backendB.RecordCount()
	messageID := "discovery-e2e-recovery"
	body := []byte("recovered-endpoint")

	if err := check.sendAccepted(ctx, messageID, body); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, aBefore+1, check.backendB, bBefore); err != nil {
		return fmt.Errorf("endpoint recovery: %w", err)
	}
	return expectRecord(check.backendA, aBefore, messageID, body)
}

func (check *verifier) verifyResponseIsNotReplayed(ctx context.Context) error {
	fmt.Println("checking that HTTP 500 is not replayed")
	check.backendB.SetResponseStatus(http.StatusInternalServerError)
	defer check.backendB.SetHealthy()
	aBefore := check.backendA.RecordCount()
	bBefore := check.backendB.RecordCount()
	messageID := "discovery-e2e-http-500"
	body := []byte("do-not-replay-response")

	if err := check.sendRejected(ctx, messageID, body); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, aBefore, check.backendB, bBefore+1); err != nil {
		return fmt.Errorf("HTTP 500 non-replay: %w", err)
	}
	return expectRecord(check.backendB, bBefore, messageID, body)
}

func (check *verifier) sendAccepted(ctx context.Context, messageID string, body []byte) error {
	result, err := check.client.Send(ctx, sdkclient.SendRequest{
		MsgID:       defaultUpstreamMsgID,
		Body:        body,
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: true,
	})
	if err != nil {
		return fmt.Errorf("send %s: %w", messageID, err)
	}
	if result.Ack == nil || result.Ack.Code != protocol.AckAccepted {
		return fmt.Errorf("send %s returned unexpected ACK %#v", messageID, result.Ack)
	}
	return nil
}

func (check *verifier) sendRejected(ctx context.Context, messageID string, body []byte) error {
	_, err := check.client.Send(ctx, sdkclient.SendRequest{
		MsgID:       defaultUpstreamMsgID,
		Body:        body,
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: true,
	})
	if err == nil {
		return fmt.Errorf("send %s succeeded, want rejected ACK", messageID)
	}
	if !errors.Is(err, sdkclient.ErrAckRejected) {
		return fmt.Errorf("send %s returned %v, want ErrAckRejected", messageID, err)
	}
	var ackErr *sdkclient.AckError
	if !errors.As(err, &ackErr) {
		return fmt.Errorf("send %s returned %T, want *client.AckError", messageID, err)
	}
	if ackErr.Ack.Code != protocol.AckRejected || ackErr.Ack.Reason != resilience.ReasonUpstreamFailed {
		return fmt.Errorf("send %s returned unexpected rejected ACK %#v", messageID, ackErr.Ack)
	}
	return nil
}

func expectCounts(first *controlledBackend, firstWant int, second *controlledBackend, secondWant int) error {
	firstCount := first.RecordCount()
	secondCount := second.RecordCount()
	if firstCount != firstWant || secondCount != secondWant {
		return fmt.Errorf(
			"request counts %s=%d %s=%d, want %s=%d %s=%d",
			first.name,
			firstCount,
			second.name,
			secondCount,
			first.name,
			firstWant,
			second.name,
			secondWant,
		)
	}
	return nil
}

func expectRecord(backend *controlledBackend, index int, messageID string, body []byte) error {
	record, err := backend.Record(index)
	if err != nil {
		return err
	}
	if err := validateRecord(record, messageID, body); err != nil {
		return fmt.Errorf("backend %s record %d: %w", backend.name, index, err)
	}
	return nil
}

func validateRecord(record backendRecord, messageID string, body []byte) error {
	message := record.Message
	if message.MsgID != defaultUpstreamMsgID {
		return fmt.Errorf("msg_id=%d, want %d", message.MsgID, defaultUpstreamMsgID)
	}
	if message.ClientID != defaultClientID || message.DeviceID != defaultDeviceID {
		return fmt.Errorf(
			"identity=%s/%s, want %s/%s",
			message.ClientID,
			message.DeviceID,
			defaultClientID,
			defaultDeviceID,
		)
	}
	if message.SessionID == "" {
		return errors.New("session_id is empty")
	}
	if message.MessageID != messageID || message.TraceID != messageID {
		return fmt.Errorf(
			"message_id=%q trace_id=%q, want %q",
			message.MessageID,
			message.TraceID,
			messageID,
		)
	}
	if !bytes.Equal(message.Body, body) {
		return fmt.Errorf("body=%q, want %q", message.Body, body)
	}
	return nil
}

func waitFor(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
