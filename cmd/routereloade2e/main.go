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
	"path/filepath"
	"time"

	"github.com/bytedance/sonic"
	sdkbackend "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
	sdkclient "github.com/qiuyier/Z-Courier/pkg/sdk/client"
	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

const (
	defaultGatewayAddress  = "127.0.0.1:9961"
	defaultGatewayReadyURL = "http://127.0.0.1:18221/readyz"
	defaultInternalURL     = "http://127.0.0.1:18221"
	defaultBackendAAddress = "127.0.0.1:18222"
	defaultBackendBAddress = "127.0.0.1:18223"
	defaultInternalToken   = "route-reload-e2e-internal-token"
	defaultUpstreamToken   = "route-reload-e2e-upstream-token"
	defaultClientID        = "route-reload-e2e-client"
	defaultDeviceID        = "route-reload-e2e-device"
	defaultClientToken     = "route-reload-e2e-token"
	primaryMsgID           = uint32(1101)
	addedMsgID             = uint32(1102)
	outOfEnvelopeMsgID     = uint32(1200)
	maximumAdminBodySize   = int64(1 << 20)

	routeStatusPath = "/internal/admin/routes/status"
	routeReloadPath = "/internal/admin/routes/reload"
)

type config struct {
	GatewayBin    string
	GatewayConfig string
	ZinxConfig    string
	GatewayLog    string
	Timeout       time.Duration
}

type verifier struct {
	client     *sdkclient.Client
	backendA   *controlledBackend
	backendB   *controlledBackend
	routeFile  string
	control    *routeControlClient
	sessionID  string
	generation uint64
}

type routeGeneration struct {
	Number     uint64 `json:"number"`
	RouteCount int    `json:"route_count"`
	InFlight   int64  `json:"in_flight"`
	State      string `json:"state"`
}

type routeReloadAttempt struct {
	Stage  string `json:"stage"`
	Result string `json:"result"`
}

type routeControlResponse struct {
	Code          string               `json:"code"`
	Result        string               `json:"result"`
	Reason        string               `json:"reason"`
	Stage         string               `json:"stage"`
	ReloadEnabled bool                 `json:"reload_enabled"`
	Active        *routeGeneration     `json:"active"`
	Candidate     *routeGeneration     `json:"candidate"`
	Retiring      *routeGeneration     `json:"retiring"`
	LastAttempt   *routeReloadAttempt  `json:"last_attempt"`
	Recent        []routeReloadAttempt `json:"recent_attempts"`
}

type routeReloadRequest struct {
	DryRun             bool   `json:"dry_run"`
	ExpectedGeneration uint64 `json:"expected_generation"`
}

type routeControlClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func main() {
	configuration, err := parseConfig(os.Args[1:], os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "route reload e2e configuration failed: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), configuration.Timeout)
	defer cancel()
	if err := run(ctx, configuration); err != nil {
		fmt.Fprintf(os.Stderr, "route reload e2e failed: %v\n", err)
		printGatewayLogTail(configuration.GatewayLog, 48<<10)
		os.Exit(1)
	}
	fmt.Println("route reload e2e passed")
}

func parseConfig(arguments []string, output io.Writer) (config, error) {
	var configuration config
	flags := flag.NewFlagSet("routereloade2e", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&configuration.GatewayBin, "gateway-bin", "", "path to a built gateway binary")
	flags.StringVar(
		&configuration.GatewayConfig,
		"gateway-config",
		"configs/z-courier.route-reload-e2e.yaml",
		"path to the route reload E2E gateway config",
	)
	flags.StringVar(
		&configuration.ZinxConfig,
		"zinx-config",
		"conf/zinx.route-reload-e2e.json",
		"path to the route reload E2E Zinx config",
	)
	flags.StringVar(
		&configuration.GatewayLog,
		"gateway-log",
		"log/e2e-route-reload-gateway.log",
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
	routeDirectory, err := os.MkdirTemp("", "z-courier-route-reload-e2e-")
	if err != nil {
		return fmt.Errorf("create route fixture directory: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, os.RemoveAll(routeDirectory))
	}()
	routeFile := filepath.Join(routeDirectory, "upstream-routes.yaml")

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

	if err := writeRouteDocumentAtomic(routeFile, routeDocumentForBackendA(backendA.Address())); err != nil {
		return fmt.Errorf("write initial route file: %w", err)
	}

	gateway, err := startGatewayProcess(configuration, routeFile)
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
		AckTimeout: 8 * time.Second,
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
	binding := client.Binding()
	if binding.SessionID == "" {
		return errors.New("gateway client binding has no session ID")
	}

	check := &verifier{
		client:    client,
		backendA:  backendA,
		backendB:  backendB,
		routeFile: routeFile,
		control: &routeControlClient{
			baseURL: defaultInternalURL,
			token:   defaultInternalToken,
			client:  &http.Client{Timeout: 3 * time.Second},
		},
		sessionID:  binding.SessionID,
		generation: 1,
	}
	return check.Run(ctx)
}

func (check *verifier) Run(ctx context.Context) error {
	if err := check.verifyInitialGeneration(ctx); err != nil {
		return err
	}
	if err := check.verifyFailedCandidates(ctx); err != nil {
		return err
	}
	if err := check.verifySwitchWithInFlightDrain(ctx); err != nil {
		return err
	}
	if err := check.verifyRollbackAndRouteRemoval(ctx); err != nil {
		return err
	}
	return nil
}

func (check *verifier) verifyInitialGeneration(ctx context.Context) error {
	fmt.Println("checking initial generation and persistent TCP binding")
	statusCode, status, err := check.control.Status(ctx)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK || !status.ReloadEnabled {
		return fmt.Errorf("initial route status = HTTP %d %+v", statusCode, status)
	}
	if err := expectActiveGeneration(status, 1, 1); err != nil {
		return fmt.Errorf("initial route status: %w", err)
	}

	aBefore := check.backendA.RecordCount()
	bBefore := check.backendB.RecordCount()
	if err := check.sendAccepted(ctx, primaryMsgID, "route-reload-before", []byte("before-reload")); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, aBefore+1, check.backendB, bBefore); err != nil {
		return fmt.Errorf("initial generation target: %w", err)
	}
	return expectRecord(check.backendA, aBefore, primaryMsgID, "route-reload-before", []byte("before-reload"))
}

func (check *verifier) verifyFailedCandidates(ctx context.Context) error {
	fmt.Println("checking parse and admission failures preserve the active generation")
	if err := writeRouteBytesAtomic(check.routeFile, []byte("version: 1\nroutes: [\n")); err != nil {
		return fmt.Errorf("write malformed route candidate: %w", err)
	}
	statusCode, response, err := check.control.Reload(ctx, true, check.generation)
	if err != nil {
		return err
	}
	if statusCode != http.StatusUnprocessableEntity || response.Code != "parse_failed" || response.Stage != "parse" {
		return fmt.Errorf("malformed candidate response = HTTP %d %+v", statusCode, response)
	}
	if err := expectActiveGeneration(response, check.generation, 1); err != nil {
		return fmt.Errorf("malformed candidate active generation: %w", err)
	}

	if err := writeRouteDocumentAtomic(check.routeFile, routeDocumentOutsideEnvelope(check.backendA.Address())); err != nil {
		return fmt.Errorf("write out-of-envelope route candidate: %w", err)
	}
	statusCode, response, err = check.control.Reload(ctx, true, check.generation)
	if err != nil {
		return err
	}
	if statusCode != http.StatusUnprocessableEntity || response.Code != "validation_failed" || response.Stage != "validation" {
		return fmt.Errorf("out-of-envelope candidate response = HTTP %d %+v", statusCode, response)
	}
	if err := expectActiveGeneration(response, check.generation, 1); err != nil {
		return fmt.Errorf("out-of-envelope candidate active generation: %w", err)
	}

	aBefore := check.backendA.RecordCount()
	bBefore := check.backendB.RecordCount()
	if err := check.sendAccepted(ctx, primaryMsgID, "route-reload-after-invalid", []byte("active-still-a")); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, aBefore+1, check.backendB, bBefore); err != nil {
		return fmt.Errorf("failed candidate changed active forwarding: %w", err)
	}
	return check.expectStableSession()
}

func (check *verifier) verifySwitchWithInFlightDrain(ctx context.Context) error {
	fmt.Println("checking atomic A-to-B switch with an in-flight generation lease")
	messageID := "route-reload-in-flight"
	block, err := check.backendA.Block(messageID)
	if err != nil {
		return err
	}
	defer block.Release()

	aBefore := check.backendA.RecordCount()
	bBefore := check.backendB.RecordCount()
	sendDone := make(chan error, 1)
	go func() {
		sendDone <- check.sendAccepted(ctx, primaryMsgID, messageID, []byte("leased-before-swap"))
	}()
	if err := block.WaitEntered(ctx); err != nil {
		return fmt.Errorf("wait for in-flight backend request: %w", err)
	}

	if err := writeRouteDocumentAtomic(check.routeFile, routeDocumentForBackendB(check.backendB.Address())); err != nil {
		return fmt.Errorf("write backend B route candidate: %w", err)
	}
	statusCode, response, err := check.control.Reload(ctx, false, check.generation)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK || response.Result != "reloaded" || response.Stage != "activation" {
		return fmt.Errorf("A-to-B reload response = HTTP %d %+v", statusCode, response)
	}
	check.generation++
	if err := expectActiveGeneration(response, check.generation, 2); err != nil {
		return fmt.Errorf("A-to-B active generation: %w", err)
	}
	if response.Retiring == nil || response.Retiring.Number != check.generation-1 || response.Retiring.InFlight < 1 {
		return fmt.Errorf("A-to-B retiring generation = %+v, want generation %d with in-flight work", response.Retiring, check.generation-1)
	}
	if err := check.expectStableSession(); err != nil {
		return err
	}

	block.Release()
	select {
	case err := <-sendDone:
		if err != nil {
			return fmt.Errorf("in-flight request after activation: %w", err)
		}
	case <-ctx.Done():
		return fmt.Errorf("wait for in-flight request completion: %w", ctx.Err())
	}
	if err := expectCounts(check.backendA, aBefore+1, check.backendB, bBefore); err != nil {
		return fmt.Errorf("in-flight request was replayed or rerouted: %w", err)
	}
	if err := expectRecord(check.backendA, aBefore, primaryMsgID, messageID, []byte("leased-before-swap")); err != nil {
		return err
	}
	if err := check.waitForRetirement(ctx, 2); err != nil {
		return err
	}

	aBefore = check.backendA.RecordCount()
	bBefore = check.backendB.RecordCount()
	if err := check.sendAccepted(ctx, primaryMsgID, "route-reload-after-swap", []byte("new-generation-b")); err != nil {
		return err
	}
	if err := check.sendAccepted(ctx, addedMsgID, "route-reload-added-route", []byte("new-msg-id-route")); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, aBefore, check.backendB, bBefore+2); err != nil {
		return fmt.Errorf("new generation target and route addition: %w", err)
	}
	if err := expectRecord(check.backendB, bBefore, primaryMsgID, "route-reload-after-swap", []byte("new-generation-b")); err != nil {
		return err
	}
	return expectRecord(check.backendB, bBefore+1, addedMsgID, "route-reload-added-route", []byte("new-msg-id-route"))
}

func (check *verifier) verifyRollbackAndRouteRemoval(ctx context.Context) error {
	fmt.Println("checking rollback to A and route removal without reconnect")
	if err := writeRouteDocumentAtomic(check.routeFile, routeDocumentForBackendA(check.backendA.Address())); err != nil {
		return fmt.Errorf("write rollback route candidate: %w", err)
	}
	statusCode, response, err := check.control.Reload(ctx, false, check.generation)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK || response.Result != "reloaded" {
		return fmt.Errorf("rollback response = HTTP %d %+v", statusCode, response)
	}
	check.generation++
	if err := expectActiveGeneration(response, check.generation, 1); err != nil {
		return fmt.Errorf("rollback active generation: %w", err)
	}
	if err := check.waitForRetirement(ctx, 1); err != nil {
		return err
	}

	aBefore := check.backendA.RecordCount()
	bBefore := check.backendB.RecordCount()
	if err := check.sendAccepted(ctx, primaryMsgID, "route-reload-rollback", []byte("rolled-back-to-a")); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, aBefore+1, check.backendB, bBefore); err != nil {
		return fmt.Errorf("rollback target: %w", err)
	}

	aBefore = check.backendA.RecordCount()
	bBefore = check.backendB.RecordCount()
	if err := check.sendAccepted(ctx, addedMsgID, "route-reload-removed-route", []byte("must-not-forward")); err != nil {
		return err
	}
	if err := expectCounts(check.backendA, aBefore, check.backendB, bBefore); err != nil {
		return fmt.Errorf("removed route still forwarded: %w", err)
	}
	return check.expectStableSession()
}

func (check *verifier) sendAccepted(ctx context.Context, msgID uint32, messageID string, body []byte) error {
	result, err := check.client.Send(ctx, sdkclient.SendRequest{
		MsgID:       msgID,
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

func (check *verifier) expectStableSession() error {
	if !check.client.Ready() {
		return errors.New("gateway client is not ready after route operation")
	}
	binding := check.client.Binding()
	if binding.SessionID != check.sessionID {
		return fmt.Errorf("client session changed from %q to %q during route reload", check.sessionID, binding.SessionID)
	}
	return nil
}

func (check *verifier) waitForRetirement(ctx context.Context, routeCount int) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		statusCode, response, err := check.control.Status(ctx)
		if err != nil {
			return err
		}
		if statusCode == http.StatusOK && response.Retiring == nil {
			return expectActiveGeneration(response, check.generation, routeCount)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for generation %d retirement: %w", check.generation-1, ctx.Err())
		case <-ticker.C:
		}
	}
}

func expectActiveGeneration(response routeControlResponse, number uint64, routeCount int) error {
	if response.Active == nil {
		return errors.New("active generation is missing")
	}
	if response.Active.Number != number || response.Active.RouteCount != routeCount || response.Active.State != "active" {
		return fmt.Errorf("active generation = %+v, want number=%d routes=%d state=active", response.Active, number, routeCount)
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

func expectRecord(backend *controlledBackend, index int, msgID uint32, messageID string, body []byte) error {
	record, err := backend.Record(index)
	if err != nil {
		return err
	}
	message := record.Message
	if message.MsgID != msgID {
		return fmt.Errorf("backend %s record %d msg_id=%d, want %d", backend.name, index, message.MsgID, msgID)
	}
	if message.ClientID != defaultClientID || message.DeviceID != defaultDeviceID || message.SessionID == "" {
		return fmt.Errorf("backend %s record %d identity=%s/%s session=%q", backend.name, index, message.ClientID, message.DeviceID, message.SessionID)
	}
	if message.MessageID != messageID || message.TraceID != messageID || !bytes.Equal(message.Body, body) {
		return fmt.Errorf(
			"backend %s record %d message_id=%q trace_id=%q body=%q, want %q/%q/%q",
			backend.name,
			index,
			message.MessageID,
			message.TraceID,
			message.Body,
			messageID,
			messageID,
			body,
		)
	}
	return nil
}

func (client *routeControlClient) Status(ctx context.Context) (int, routeControlResponse, error) {
	return client.do(ctx, http.MethodGet, routeStatusPath, nil)
}

func (client *routeControlClient) Reload(ctx context.Context, dryRun bool, expectedGeneration uint64) (int, routeControlResponse, error) {
	body, err := sonic.Marshal(routeReloadRequest{
		DryRun:             dryRun,
		ExpectedGeneration: expectedGeneration,
	})
	if err != nil {
		return 0, routeControlResponse{}, fmt.Errorf("encode route reload request: %w", err)
	}
	return client.do(ctx, http.MethodPost, routeReloadPath, body)
}

func (client *routeControlClient) do(ctx context.Context, method, path string, body []byte) (int, routeControlResponse, error) {
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, routeControlResponse{}, fmt.Errorf("create route control request: %w", err)
	}
	request.Header.Set(sdkbackend.InternalTokenHeader, client.token)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return 0, routeControlResponse{}, fmt.Errorf("route control %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maximumAdminBodySize+1))
	if err != nil {
		return 0, routeControlResponse{}, fmt.Errorf("read route control response: %w", err)
	}
	if int64(len(responseBody)) > maximumAdminBodySize {
		return 0, routeControlResponse{}, errors.New("route control response exceeds size limit")
	}
	var decoded routeControlResponse
	if err := sonic.Unmarshal(responseBody, &decoded); err != nil {
		return 0, routeControlResponse{}, fmt.Errorf("decode route control response: %w", err)
	}
	return response.StatusCode, decoded, nil
}
