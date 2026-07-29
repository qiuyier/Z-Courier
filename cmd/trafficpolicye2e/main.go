package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adapter/httpforwarder"
	"github.com/qiuyier/Z-Courier/internal/adapter/upstream"
	"github.com/qiuyier/Z-Courier/internal/resilience"
	sdkclient "github.com/qiuyier/Z-Courier/pkg/sdk/client"
	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

const (
	defaultGatewayAddress = "127.0.0.1:9941"
	defaultBackendAddress = "127.0.0.1:18202"
	defaultUpstreamToken  = "traffic-policy-upstream-token"

	trafficPolicyE2EModeLocal            = "local"
	trafficPolicyE2EModeRedisShared      = "redis-shared"
	trafficPolicyE2EModeRedisUnavailable = "redis-unavailable"
	trafficPolicyE2EModeRedisRecovered   = "redis-recovered"

	standardMsgID    = uint32(2101)
	priorityMsgID    = uint32(2102)
	unlimitedMsgID   = uint32(2103)
	redisSharedMsgID = uint32(2201)

	refillWait = 250 * time.Millisecond
	idleWait   = 5250 * time.Millisecond
)

type config struct {
	GatewayAddress  string
	GatewayBAddress string
	BackendAddress  string
	Mode            string
	Timeout         time.Duration
}

type clientFixture struct {
	clientID string
	token    string
	client   *sdkclient.Client
}

type verifier struct {
	clients map[string]*clientFixture
	backend *recordingBackend
}

func main() {
	configuration, err := parseConfig(os.Args[1:], os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "traffic policy e2e configuration failed: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), configuration.Timeout)
	defer cancel()
	if err := run(ctx, configuration); err != nil {
		fmt.Fprintf(os.Stderr, "traffic policy e2e failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("traffic policy e2e passed")
}

func parseConfig(arguments []string, output io.Writer) (config, error) {
	var configuration config
	flags := flag.NewFlagSet("trafficpolicye2e", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&configuration.GatewayAddress, "gateway-address", defaultGatewayAddress, "gateway TCP address")
	flags.StringVar(&configuration.GatewayBAddress, "gateway-b-address", "", "second gateway TCP address for redis-shared mode")
	flags.StringVar(&configuration.BackendAddress, "backend-address", defaultBackendAddress, "fixture HTTP backend address")
	flags.StringVar(
		&configuration.Mode,
		"mode",
		trafficPolicyE2EModeLocal,
		"verification mode: local, redis-shared, redis-unavailable, or redis-recovered",
	)
	flags.DurationVar(&configuration.Timeout, "timeout", 20*time.Second, "overall verification timeout")
	if err := flags.Parse(arguments); err != nil {
		return config{}, err
	}
	configuration.Mode = strings.ToLower(strings.TrimSpace(configuration.Mode))
	if configuration.GatewayAddress == "" {
		return config{}, errors.New("-gateway-address is required")
	}
	if configuration.BackendAddress == "" {
		return config{}, errors.New("-backend-address is required")
	}
	if configuration.Timeout <= 0 {
		return config{}, errors.New("-timeout must be greater than zero")
	}
	switch configuration.Mode {
	case trafficPolicyE2EModeLocal,
		trafficPolicyE2EModeRedisUnavailable,
		trafficPolicyE2EModeRedisRecovered:
	case trafficPolicyE2EModeRedisShared:
		if strings.TrimSpace(configuration.GatewayBAddress) == "" {
			return config{}, errors.New("-gateway-b-address is required in redis-shared mode")
		}
	default:
		return config{}, fmt.Errorf("unsupported -mode %q", configuration.Mode)
	}
	return configuration, nil
}

func run(ctx context.Context, configuration config) (runErr error) {
	backend := newRecordingBackend(configuration.BackendAddress, defaultUpstreamToken)
	if err := backend.Start(); err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, backend.Close())
	}()

	switch configuration.Mode {
	case trafficPolicyE2EModeLocal:
		return runLocal(ctx, configuration, backend)
	case trafficPolicyE2EModeRedisShared:
		return runRedisShared(ctx, configuration, backend)
	case trafficPolicyE2EModeRedisUnavailable:
		return runRedisUnavailable(ctx, configuration, backend)
	case trafficPolicyE2EModeRedisRecovered:
		return runRedisRecovered(ctx, configuration, backend)
	default:
		return fmt.Errorf("unsupported traffic policy E2E mode %q", configuration.Mode)
	}
}

func runLocal(
	ctx context.Context,
	configuration config,
	backend *recordingBackend,
) (runErr error) {
	fixtures := []*clientFixture{
		{clientID: "traffic-policy-client-a", token: "traffic-policy-token-a"},
		{clientID: "traffic-policy-client-b", token: "traffic-policy-token-b"},
		{clientID: "traffic-policy-client-c", token: "traffic-policy-token-c"},
	}
	for _, fixture := range fixtures {
		client, err := connectClient(
			ctx,
			configuration.GatewayAddress,
			fixture.clientID,
			fixture.clientID+"-device",
			fixture.token,
		)
		if err != nil {
			return err
		}
		fixture.client = client
		defer func(client *sdkclient.Client) {
			runErr = errors.Join(runErr, client.Close())
		}(client)
	}

	check := &verifier{
		clients: make(map[string]*clientFixture, len(fixtures)),
		backend: backend,
	}
	for _, fixture := range fixtures {
		check.clients[fixture.clientID] = fixture
	}
	return check.Run(ctx)
}

func runRedisShared(
	ctx context.Context,
	configuration config,
	backend *recordingBackend,
) (runErr error) {
	const (
		clientID = "traffic-policy-redis-shared-client"
		token    = "traffic-policy-redis-shared-token"
	)
	clientA, err := connectClient(
		ctx,
		configuration.GatewayAddress,
		clientID,
		"traffic-policy-redis-shared-device-a",
		token,
	)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, clientA.Close())
	}()
	clientB, err := connectClient(
		ctx,
		configuration.GatewayBAddress,
		clientID,
		"traffic-policy-redis-shared-device-b",
		token,
	)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, clientB.Close())
	}()

	check := &verifier{backend: backend}
	fixtureA := &clientFixture{clientID: clientID, token: token, client: clientA}
	fixtureB := &clientFixture{clientID: clientID, token: token, client: clientB}

	fmt.Println("checking shared Redis quota across gateway-a and gateway-b")
	if err := check.sendAccepted(ctx, fixtureA, redisSharedMsgID, "traffic-redis-shared-1"); err != nil {
		return err
	}
	if err := check.sendAccepted(ctx, fixtureB, redisSharedMsgID, "traffic-redis-shared-2"); err != nil {
		return err
	}
	if err := check.sendAccepted(ctx, fixtureA, redisSharedMsgID, "traffic-redis-shared-3"); err != nil {
		return err
	}
	if err := check.sendRejected(
		ctx,
		fixtureB,
		redisSharedMsgID,
		"traffic-redis-shared-rate-limited",
		resilience.ReasonRateLimited,
	); err != nil {
		return err
	}
	if got := backend.RecordCount(); got != 3 {
		return fmt.Errorf("backend request count = %d, want 3 shared-quota packets", got)
	}
	return nil
}

func runRedisUnavailable(
	ctx context.Context,
	configuration config,
	backend *recordingBackend,
) (runErr error) {
	fixture, err := connectRedisRecoveryClient(ctx, configuration.GatewayAddress)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, fixture.client.Close())
	}()

	fmt.Println("checking fail-closed admission while Redis is unavailable")
	check := &verifier{backend: backend}
	if err := check.sendRejected(
		ctx,
		fixture,
		redisSharedMsgID,
		"traffic-redis-unavailable",
		resilience.ReasonAdmissionUnavailable,
	); err != nil {
		return err
	}
	if got := backend.RecordCount(); got != 0 {
		return fmt.Errorf("backend request count = %d, want 0 while Redis is unavailable", got)
	}
	return nil
}

func runRedisRecovered(
	ctx context.Context,
	configuration config,
	backend *recordingBackend,
) (runErr error) {
	fixture, err := connectRedisRecoveryClient(ctx, configuration.GatewayAddress)
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, fixture.client.Close())
	}()

	fmt.Println("checking shared admission recovers without restarting the gateway")
	check := &verifier{backend: backend}
	if err := check.sendAccepted(ctx, fixture, redisSharedMsgID, "traffic-redis-recovered"); err != nil {
		return err
	}
	if got := backend.RecordCount(); got != 1 {
		return fmt.Errorf("backend request count = %d, want 1 after Redis recovery", got)
	}
	return nil
}

func connectRedisRecoveryClient(
	ctx context.Context,
	address string,
) (*clientFixture, error) {
	const (
		clientID = "traffic-policy-redis-recovery-client"
		token    = "traffic-policy-redis-recovery-token"
	)
	client, err := connectClient(
		ctx,
		address,
		clientID,
		"traffic-policy-redis-recovery-device",
		token,
	)
	if err != nil {
		return nil, err
	}
	return &clientFixture{clientID: clientID, token: token, client: client}, nil
}

func connectClient(
	ctx context.Context,
	address string,
	clientID string,
	deviceID string,
	token string,
) (*sdkclient.Client, error) {
	client, err := sdkclient.New(sdkclient.Config{
		Address:    address,
		ClientID:   clientID,
		DeviceID:   deviceID,
		Token:      token,
		AckTimeout: 3 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create client %s for %s: %w", clientID, address, err)
	}
	if err := client.Connect(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect client %s to %s: %w", clientID, address, err)
	}
	return client, nil
}

func (check *verifier) Run(ctx context.Context) error {
	clientA := check.clients["traffic-policy-client-a"]
	clientB := check.clients["traffic-policy-client-b"]
	clientC := check.clients["traffic-policy-client-c"]

	fmt.Println("checking standard token-bucket burst")
	if err := check.sendAccepted(ctx, clientA, standardMsgID, "traffic-standard-1"); err != nil {
		return err
	}
	if err := check.sendAccepted(ctx, clientA, standardMsgID, "traffic-standard-2"); err != nil {
		return err
	}
	if err := check.sendRejected(
		ctx,
		clientA,
		standardMsgID,
		"traffic-standard-rate-limited",
		resilience.ReasonRateLimited,
	); err != nil {
		return err
	}

	fmt.Println("checking continuous token refill")
	if err := waitFor(ctx, refillWait); err != nil {
		return err
	}
	if err := check.sendAccepted(ctx, clientA, standardMsgID, "traffic-standard-refilled"); err != nil {
		return err
	}

	fmt.Println("checking higher-priority route policy")
	if err := check.sendAccepted(ctx, clientB, priorityMsgID, "traffic-priority-1"); err != nil {
		return err
	}
	if err := check.sendRejected(
		ctx,
		clientB,
		priorityMsgID,
		"traffic-priority-rate-limited",
		resilience.ReasonRateLimited,
	); err != nil {
		return err
	}

	fmt.Println("checking unmatched traffic bypasses admission buckets")
	if err := check.sendAccepted(ctx, clientC, unlimitedMsgID, "traffic-unmatched"); err != nil {
		return err
	}

	fmt.Println("checking bounded local key capacity")
	if err := check.sendRejected(
		ctx,
		clientC,
		standardMsgID,
		"traffic-key-capacity",
		resilience.ReasonOverloaded,
	); err != nil {
		return err
	}

	fmt.Println("checking idle bucket eviction")
	if err := waitFor(ctx, idleWait); err != nil {
		return err
	}
	if err := check.sendAccepted(ctx, clientC, standardMsgID, "traffic-after-idle-eviction"); err != nil {
		return err
	}

	if got := check.backend.RecordCount(); got != 6 {
		return fmt.Errorf("backend request count = %d, want 6 accepted packets", got)
	}
	return nil
}

func (check *verifier) sendAccepted(
	ctx context.Context,
	fixture *clientFixture,
	msgID uint32,
	messageID string,
) error {
	before := check.backend.RecordCount()
	body := []byte(messageID)
	result, err := fixture.client.Send(ctx, sdkclient.SendRequest{
		MsgID:       msgID,
		Body:        body,
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: true,
	})
	if err != nil {
		return fmt.Errorf("send accepted %s: %w", messageID, err)
	}
	if result.Ack == nil ||
		result.Ack.Code != protocol.AckAccepted ||
		result.Ack.MsgID != msgID ||
		result.Ack.MessageID != messageID {
		return fmt.Errorf("send accepted %s returned unexpected ACK %#v", messageID, result.Ack)
	}
	if got := check.backend.RecordCount(); got != before+1 {
		return fmt.Errorf("send accepted %s forwarded %d requests, want 1", messageID, got-before)
	}
	record, err := check.backend.Record(before)
	if err != nil {
		return err
	}
	if record.Message.MsgID != msgID ||
		record.Message.ClientID != fixture.clientID ||
		record.Message.MessageID != messageID ||
		record.Message.TraceID != messageID ||
		!bytes.Equal(record.Message.Body, body) {
		return fmt.Errorf("send accepted %s produced unexpected backend record %+v", messageID, record.Message)
	}
	return nil
}

func (check *verifier) sendRejected(
	ctx context.Context,
	fixture *clientFixture,
	msgID uint32,
	messageID string,
	wantReason string,
) error {
	before := check.backend.RecordCount()
	_, err := fixture.client.Send(ctx, sdkclient.SendRequest{
		MsgID:       msgID,
		Body:        []byte(messageID),
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: true,
	})
	if err == nil {
		return fmt.Errorf("send %s succeeded, want rejected ACK reason %q", messageID, wantReason)
	}
	if !errors.Is(err, sdkclient.ErrAckRejected) {
		return fmt.Errorf("send %s returned %v, want ErrAckRejected", messageID, err)
	}
	var ackErr *sdkclient.AckError
	if !errors.As(err, &ackErr) {
		return fmt.Errorf("send %s returned %T, want *client.AckError", messageID, err)
	}
	if ackErr.Ack.Code != protocol.AckRejected ||
		ackErr.Ack.MsgID != msgID ||
		ackErr.Ack.MessageID != messageID ||
		ackErr.Ack.Reason != wantReason {
		return fmt.Errorf("send %s returned unexpected rejected ACK %#v", messageID, ackErr.Ack)
	}
	if got := check.backend.RecordCount(); got != before {
		return fmt.Errorf("rejected packet %s reached backend: count %d -> %d", messageID, before, got)
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

const maxBackendRequestBody = 2 << 20

type backendRecord struct {
	Message upstream.Message
}

type recordingBackend struct {
	address       string
	expectedToken string

	mu      sync.Mutex
	server  *http.Server
	done    chan error
	records []backendRecord
}

func newRecordingBackend(address, expectedToken string) *recordingBackend {
	return &recordingBackend{address: address, expectedToken: expectedToken}
}

func (backend *recordingBackend) Start() error {
	listener, err := net.Listen("tcp", backend.address)
	if err != nil {
		return fmt.Errorf("listen traffic policy backend on %s: %w", backend.address, err)
	}
	server := &http.Server{
		Handler:           backend,
		ReadHeaderTimeout: time.Second,
	}
	done := make(chan error, 1)

	backend.mu.Lock()
	backend.address = listener.Addr().String()
	backend.server = server
	backend.done = done
	backend.mu.Unlock()

	go func() {
		serveErr := server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		done <- serveErr
		close(done)
	}()
	return nil
}

func (backend *recordingBackend) Close() error {
	backend.mu.Lock()
	server := backend.server
	done := backend.done
	backend.server = nil
	backend.done = nil
	backend.mu.Unlock()
	if server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(ctx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}

	var serveErr error
	if done != nil {
		serveErr = <-done
	}
	return errors.Join(shutdownErr, serveErr)
}

func (backend *recordingBackend) Address() string {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.address
}

func (backend *recordingBackend) RecordCount() int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return len(backend.records)
}

func (backend *recordingBackend) Record(index int) (backendRecord, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if index < 0 || index >= len(backend.records) {
		return backendRecord{}, fmt.Errorf(
			"backend record index %d is outside %d recorded requests",
			index,
			len(backend.records),
		)
	}
	record := backend.records[index]
	record.Message.Body = bytes.Clone(record.Message.Body)
	return record, nil
}

func (backend *recordingBackend) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/gateway/upstream" {
		http.NotFound(writer, request)
		return
	}
	if request.Header.Get(httpforwarder.InternalTokenHeader) != backend.expectedToken {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}

	rawBody, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxBackendRequestBody))
	if err != nil {
		http.Error(writer, "invalid request body", http.StatusBadRequest)
		return
	}
	var message upstream.Message
	if err := sonic.Unmarshal(rawBody, &message); err != nil {
		http.Error(writer, "invalid request envelope", http.StatusBadRequest)
		return
	}
	if request.Header.Get(httpforwarder.TraceIDHeader) != message.TraceID {
		http.Error(writer, "trace header mismatch", http.StatusBadRequest)
		return
	}

	backend.mu.Lock()
	backend.records = append(backend.records, backendRecord{Message: message})
	backend.mu.Unlock()
	writer.WriteHeader(http.StatusAccepted)
}
