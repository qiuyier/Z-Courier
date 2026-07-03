package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	sdkbackend "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

func TestOverviewSendsAdminRequest(t *testing.T) {
	var gotReq *http.Request
	stubAdminHTTPClient(t, http.StatusOK, `{"code":"ok"}`, &gotReq)

	err := overview(commonConfig{
		InternalURL:   "http://gateway-a:18182/",
		AuthMode:      authModeToken,
		InternalToken: "secret",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("overview() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodGet || gotReq.URL.String() != "http://gateway-a:18182/internal/admin/overview" {
		t.Fatalf("request = %s %s, want GET /internal/admin/overview", gotReq.Method, gotReq.URL.String())
	}
	if gotReq.Header.Get(sdkbackend.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token = %q, want secret", gotReq.Header.Get(sdkbackend.InternalTokenHeader))
	}
}

func TestDiagnosticsSendsAdminRequest(t *testing.T) {
	var gotReq *http.Request
	stubAdminHTTPClient(t, http.StatusOK, `{"code":"ok","warnings":[]}`, &gotReq)

	err := diagnostics(commonConfig{
		InternalURL:   "http://gateway-a:18182/",
		AuthMode:      authModeToken,
		InternalToken: "secret",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("diagnostics() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodGet || gotReq.URL.String() != "http://gateway-a:18182/internal/admin/diagnostics" {
		t.Fatalf("request = %s %s, want GET /internal/admin/diagnostics", gotReq.Method, gotReq.URL.String())
	}
	if gotReq.Header.Get(sdkbackend.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token = %q, want secret", gotReq.Header.Get(sdkbackend.InternalTokenHeader))
	}
}

func TestCheckSendsAdminRequest(t *testing.T) {
	var gotReq *http.Request
	stubAdminHTTPClient(t, http.StatusOK, `{"code":"ok","status":"ok","checks":[]}`, &gotReq)

	err := check(checkConfig{
		commonConfig: commonConfig{
			InternalURL:   "http://gateway-a:18182/",
			AuthMode:      authModeToken,
			InternalToken: "secret",
			Timeout:       time.Second,
		},
		ProbeTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("check() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodGet || gotReq.URL.Path != "/internal/admin/check" {
		t.Fatalf("request = %s %s, want GET /internal/admin/check", gotReq.Method, gotReq.URL.String())
	}
	if gotReq.URL.Query().Get("timeout") != "3s" {
		t.Fatalf("timeout = %q, want 3s", gotReq.URL.Query().Get("timeout"))
	}
	if gotReq.Header.Get(sdkbackend.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token = %q, want secret", gotReq.Header.Get(sdkbackend.InternalTokenHeader))
	}
}

func TestCheckFailsWhenGatewayReportsFailure(t *testing.T) {
	var gotReq *http.Request
	stubAdminHTTPClient(t, http.StatusOK, `{"code":"ok","status":"failed","checks":[]}`, &gotReq)

	err := check(checkConfig{
		commonConfig: validCommonConfig(t),
		ProbeTimeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("check() error = %v, want failed status error", err)
	}
}

func TestDiagnoseCollectsBundle(t *testing.T) {
	var gotPaths []string
	stubAdminHTTPClientFunc(t, func(req *http.Request) (int, string) {
		gotPaths = append(gotPaths, req.URL.RequestURI())
		switch req.URL.Path {
		case "/internal/admin/overview":
			return http.StatusOK, `{"code":"ok","gateway_node":"gateway-a"}`
		case "/internal/admin/diagnostics":
			return http.StatusOK, `{"code":"ok","warnings":[]}`
		case "/internal/admin/check":
			return http.StatusOK, `{"code":"ok","status":"ok","checks":[]}`
		case "/internal/admin/routes":
			return http.StatusOK, `{"code":"ok","routes":[]}`
		case "/internal/messages":
			return http.StatusOK, `{"code":"ok","total":0,"messages":[]}`
		case "/internal/debug/sessions":
			return http.StatusOK, `{"code":"ok","sessions":[]}`
		case "/internal/debug/route":
			return http.StatusOK, `{"code":"ok","cluster_route_found":false}`
		default:
			return http.StatusNotFound, `{"code":"not_found"}`
		}
	})

	output := filepath.Join(t.TempDir(), "gateway-a.json")
	err := diagnose(diagnoseConfig{
		commonConfig: commonConfig{
			InternalURL:   "http://user:password@gateway-a:18182",
			AuthMode:      authModeToken,
			InternalToken: "secret",
			Timeout:       time.Second,
		},
		ProbeTimeout: time.Second,
		Output:       output,
		ClientID:     "client-1",
		DeviceID:     "device-1",
		SessionLimit: 10,
		MessageLimit: 5,
	})
	if err != nil {
		t.Fatalf("diagnose() error = %v", err)
	}

	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	body := string(content)
	for _, secret := range []string{"user:password", "secret"} {
		if strings.Contains(body, secret) {
			t.Fatalf("diagnose output leaked %q: %s", secret, body)
		}
	}

	var bundle diagnoseBundle
	if err := sonic.Unmarshal(content, &bundle); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if bundle.Code != "ok" || bundle.CollectionStatus != "complete" || bundle.TargetURL != "http://gateway-a:18182" {
		t.Fatalf("bundle = %+v, want complete sanitized target", bundle)
	}
	for _, section := range []string{"overview", "diagnostics", "check", "routes", "failed_messages", "sessions", "route"} {
		if _, ok := bundle.Sections[section]; !ok {
			t.Fatalf("missing section %q in %+v", section, bundle.Sections)
		}
	}

	if !containsPath(gotPaths, "/internal/admin/check?timeout=1s") {
		t.Fatalf("paths = %v, want check timeout path", gotPaths)
	}
	if !containsPath(gotPaths, "/internal/messages?limit=5&status=failed") {
		t.Fatalf("paths = %v, want failed messages path", gotPaths)
	}
	if !containsPath(gotPaths, "/internal/debug/sessions?client_id=client-1&device_id=device-1&limit=10") {
		t.Fatalf("paths = %v, want sessions path", gotPaths)
	}
	if !containsPath(gotPaths, "/internal/debug/route?client_id=client-1&device_id=device-1") {
		t.Fatalf("paths = %v, want route path", gotPaths)
	}
}

func TestDiagnoseRequiresClientIDWithDeviceID(t *testing.T) {
	err := diagnose(diagnoseConfig{
		commonConfig: validCommonConfig(t),
		ProbeTimeout: time.Second,
		DeviceID:     "device-1",
		SessionLimit: 100,
		MessageLimit: 20,
	})
	if err == nil || !strings.Contains(err.Error(), "client-id is required") {
		t.Fatalf("diagnose() error = %v, want client-id error", err)
	}
}

func TestRoutesSignsAdminRequestWithHMAC(t *testing.T) {
	var gotReq *http.Request
	stubAdminHTTPClient(t, http.StatusOK, `{"code":"ok"}`, &gotReq)

	err := routes(commonConfig{
		InternalURL: "http://gateway-a:18182",
		AuthMode:    authModeHMAC,
		HMACKeyID:   "backend-1",
		HMACSecret:  "0123456789abcdef0123456789abcdef",
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("routes() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.URL.Path != "/internal/admin/routes" {
		t.Fatalf("path = %s, want /internal/admin/routes", gotReq.URL.Path)
	}
	if gotReq.Header.Get(sdkbackend.InternalTokenHeader) != "" {
		t.Fatalf("internal token = %q, want empty", gotReq.Header.Get(sdkbackend.InternalTokenHeader))
	}
	if gotReq.Header.Get(signing.HeaderKeyID) != "backend-1" || gotReq.Header.Get(signing.HeaderSignature) == "" {
		t.Fatalf("missing HMAC headers: key=%q signature=%q", gotReq.Header.Get(signing.HeaderKeyID), gotReq.Header.Get(signing.HeaderSignature))
	}
}

func TestRouteSendsDebugRouteRequest(t *testing.T) {
	var gotReq *http.Request
	stubAdminHTTPClient(t, http.StatusOK, `{"code":"ok"}`, &gotReq)

	err := route(routeConfig{
		commonConfig: commonConfig{
			InternalURL:   "http://gateway-a:18182/",
			AuthMode:      authModeToken,
			InternalToken: "secret",
			Timeout:       time.Second,
		},
		ClientID: "client-1",
		DeviceID: "device-1",
	})
	if err != nil {
		t.Fatalf("route() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.URL.Path != "/internal/debug/route" {
		t.Fatalf("path = %s, want /internal/debug/route", gotReq.URL.Path)
	}
	if gotReq.URL.Query().Get("client_id") != "client-1" || gotReq.URL.Query().Get("device_id") != "device-1" {
		t.Fatalf("query = %s, want client/device", gotReq.URL.RawQuery)
	}
}

func TestSessionsSendsDebugSessionsRequest(t *testing.T) {
	var gotReq *http.Request
	stubAdminHTTPClient(t, http.StatusOK, `{"code":"ok"}`, &gotReq)

	err := sessions(sessionsConfig{
		commonConfig: commonConfig{
			InternalURL:   "http://gateway-a:18182/",
			AuthMode:      authModeToken,
			InternalToken: "secret",
			Timeout:       time.Second,
		},
		SessionID: "session-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		Limit:     25,
	})
	if err != nil {
		t.Fatalf("sessions() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.URL.Path != "/internal/debug/sessions" {
		t.Fatalf("path = %s, want /internal/debug/sessions", gotReq.URL.Path)
	}
	query := gotReq.URL.Query()
	if query.Get("session_id") != "session-1" || query.Get("client_id") != "client-1" || query.Get("device_id") != "device-1" || query.Get("limit") != "25" {
		t.Fatalf("query = %s, want session_id, client_id, device_id, and limit", gotReq.URL.RawQuery)
	}
}

func TestMessageSendsStatusRequest(t *testing.T) {
	var gotReq *http.Request
	stubAdminHTTPClient(t, http.StatusOK, `{"code":"ok","message_id":"message-1"}`, &gotReq)

	err := message(messageConfig{
		commonConfig: commonConfig{
			InternalURL:   "http://gateway-a:18182/",
			AuthMode:      authModeToken,
			InternalToken: "secret",
			Timeout:       time.Second,
		},
		MessageID: " message-1 ",
	})
	if err != nil {
		t.Fatalf("message() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.URL.Path != "/internal/message/status" {
		t.Fatalf("path = %s, want /internal/message/status", gotReq.URL.Path)
	}
	if gotReq.URL.Query().Get("message_id") != "message-1" {
		t.Fatalf("message_id = %q, want message-1", gotReq.URL.Query().Get("message_id"))
	}
	if gotReq.Header.Get(sdkbackend.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token = %q, want secret", gotReq.Header.Get(sdkbackend.InternalTokenHeader))
	}
}

func TestMessagesSendsListRequest(t *testing.T) {
	var gotReq *http.Request
	stubAdminHTTPClient(t, http.StatusOK, `{"code":"ok","total":0}`, &gotReq)

	err := messages(messagesConfig{
		commonConfig: commonConfig{
			InternalURL:   "http://gateway-a:18182/",
			AuthMode:      authModeToken,
			InternalToken: "secret",
			Timeout:       time.Second,
		},
		Status: "failed",
		Limit:  25,
	})
	if err != nil {
		t.Fatalf("messages() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.URL.Path != "/internal/messages" {
		t.Fatalf("path = %s, want /internal/messages", gotReq.URL.Path)
	}
	if gotReq.URL.Query().Get("status") != "failed" || gotReq.URL.Query().Get("limit") != "25" {
		t.Fatalf("query = %s, want failed limit 25", gotReq.URL.RawQuery)
	}
}

func TestRequeueRequiresConfirm(t *testing.T) {
	var called bool
	stubFailingAdminHTTPClient(t, &called)

	err := requeue(requeueConfig{
		commonConfig: validCommonConfig(t),
		MessageID:    "message-1",
	})
	if err == nil || !strings.Contains(err.Error(), "without -confirm") {
		t.Fatalf("requeue() error = %v, want confirm error", err)
	}
	if called {
		t.Fatal("requeue() sent an HTTP request without confirm")
	}
}

func TestRequeueSendsConfirmedRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubAdminHTTPClientWithBody(t, http.StatusOK, `{"code":"ok"}`, &gotReq, &gotBody)

	err := requeue(requeueConfig{
		commonConfig: commonConfig{
			InternalURL:   "http://gateway-a:18182/",
			AuthMode:      authModeToken,
			InternalToken: "secret",
			Timeout:       time.Second,
		},
		MessageID: " message-1 ",
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("requeue() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodPost || gotReq.URL.String() != "http://gateway-a:18182/internal/message/requeue" {
		t.Fatalf("request = %s %s, want POST /internal/message/requeue", gotReq.Method, gotReq.URL.String())
	}
	if gotReq.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q, want application/json", gotReq.Header.Get("Content-Type"))
	}
	if gotReq.Header.Get(sdkbackend.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token = %q, want secret", gotReq.Header.Get(sdkbackend.InternalTokenHeader))
	}

	var body sdkbackend.MessageActionRequest
	if err := sonic.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.MessageID != "message-1" || body.Reason != "" {
		t.Fatalf("body = %+v, want message_id only", body)
	}
}

func TestDiscardRequiresConfirmAndReason(t *testing.T) {
	var called bool
	stubFailingAdminHTTPClient(t, &called)

	if err := discard(discardConfig{commonConfig: validCommonConfig(t), MessageID: "message-1", Reason: "manual"}); err == nil || !strings.Contains(err.Error(), "without -confirm") {
		t.Fatalf("discard() confirm error = %v, want confirm error", err)
	}
	if err := discard(discardConfig{commonConfig: validCommonConfig(t), MessageID: "message-1", Confirm: true}); err == nil || !strings.Contains(err.Error(), "reason is required") {
		t.Fatalf("discard() reason error = %v, want reason error", err)
	}
	if called {
		t.Fatal("discard() sent an HTTP request before validation passed")
	}
}

func TestDiscardSendsConfirmedRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubAdminHTTPClientWithBody(t, http.StatusOK, `{"code":"ok"}`, &gotReq, &gotBody)

	err := discard(discardConfig{
		commonConfig: commonConfig{
			InternalURL:   "http://gateway-a:18182/",
			AuthMode:      authModeToken,
			InternalToken: "secret",
			Timeout:       time.Second,
		},
		MessageID: " message-1 ",
		Reason:    " handled manually ",
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("discard() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodPost || gotReq.URL.String() != "http://gateway-a:18182/internal/message/discard" {
		t.Fatalf("request = %s %s, want POST /internal/message/discard", gotReq.Method, gotReq.URL.String())
	}

	var body sdkbackend.MessageActionRequest
	if err := sonic.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.MessageID != "message-1" || body.Reason != "handled manually" {
		t.Fatalf("body = %+v, want message_id and trimmed reason", body)
	}
}

func TestRetryScanRequiresConfirm(t *testing.T) {
	var called bool
	stubFailingAdminHTTPClient(t, &called)

	err := retryScan(retryScanConfig{
		commonConfig: validCommonConfig(t),
		Limit:        10,
	})
	if err == nil || !strings.Contains(err.Error(), "without -confirm") {
		t.Fatalf("retryScan() error = %v, want confirm error", err)
	}
	if called {
		t.Fatal("retryScan() sent an HTTP request without confirm")
	}
}

func TestRetryScanSendsConfirmedRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubAdminHTTPClientWithBody(t, http.StatusOK, `{"code":"ok","scanned":0}`, &gotReq, &gotBody)

	err := retryScan(retryScanConfig{
		commonConfig: commonConfig{
			InternalURL:   "http://gateway-a:18182/",
			AuthMode:      authModeToken,
			InternalToken: "secret",
			Timeout:       time.Second,
		},
		Limit:   25,
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("retryScan() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodPost || gotReq.URL.String() != "http://gateway-a:18182/internal/messages/retry/scan" {
		t.Fatalf("request = %s %s, want POST /internal/messages/retry/scan", gotReq.Method, gotReq.URL.String())
	}
	if gotReq.Header.Get(sdkbackend.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token = %q, want secret", gotReq.Header.Get(sdkbackend.InternalTokenHeader))
	}

	var body sdkbackend.RetryScanRequest
	if err := sonic.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.Limit != 25 {
		t.Fatalf("body = %+v, want limit 25", body)
	}
}

func TestRequeueSignsRequestWithHMAC(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubAdminHTTPClientWithBody(t, http.StatusOK, `{"code":"ok"}`, &gotReq, &gotBody)

	err := requeue(requeueConfig{
		commonConfig: commonConfig{
			InternalURL: "http://gateway-a:18182",
			AuthMode:    authModeHMAC,
			HMACKeyID:   "backend-1",
			HMACSecret:  "0123456789abcdef0123456789abcdef",
			Timeout:     time.Second,
		},
		MessageID: "message-1",
		Confirm:   true,
	})
	if err != nil {
		t.Fatalf("requeue() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if len(gotBody) == 0 {
		t.Fatal("request body is empty")
	}
	if gotReq.Header.Get(sdkbackend.InternalTokenHeader) != "" {
		t.Fatalf("internal token = %q, want empty", gotReq.Header.Get(sdkbackend.InternalTokenHeader))
	}
	if gotReq.Header.Get(signing.HeaderKeyID) != "backend-1" || gotReq.Header.Get(signing.HeaderSignature) == "" {
		t.Fatalf("missing HMAC headers: key=%q signature=%q", gotReq.Header.Get(signing.HeaderKeyID), gotReq.Header.Get(signing.HeaderSignature))
	}
}

func TestCommonConfigValidation(t *testing.T) {
	tests := []commonConfig{
		{InternalURL: "", AuthMode: authModeToken, Timeout: time.Second},
		{InternalURL: "http://gateway", AuthMode: "unknown", Timeout: time.Second},
		{InternalURL: "http://gateway", AuthMode: authModeHMAC, HMACSecret: "0123456789abcdef0123456789abcdef", Timeout: time.Second},
		{InternalURL: "http://gateway", AuthMode: authModeHMAC, HMACKeyID: "backend-1", Timeout: time.Second},
		{InternalURL: "http://gateway", AuthMode: authModeToken, Timeout: 0},
	}

	for _, test := range tests {
		if err := validateCommonConfig(test); err == nil {
			t.Fatalf("validateCommonConfig(%+v) error = nil, want error", test)
		}
	}
}

func TestMessageCommandValidation(t *testing.T) {
	if err := message(messageConfig{commonConfig: validCommonConfig(t)}); err == nil {
		t.Fatal("message() error = nil, want missing message-id error")
	}
	if err := messages(messagesConfig{commonConfig: validCommonConfig(t), Status: "unknown", Limit: 10}); err == nil {
		t.Fatal("messages() error = nil, want invalid status error")
	}
	if err := messages(messagesConfig{commonConfig: validCommonConfig(t), Status: "failed", Limit: 0}); err == nil {
		t.Fatal("messages() error = nil, want invalid limit error")
	}
}

func validCommonConfig(t *testing.T) commonConfig {
	t.Helper()
	return commonConfig{
		InternalURL:   "http://gateway-a:18182",
		AuthMode:      authModeToken,
		InternalToken: "secret",
		Timeout:       time.Second,
	}
}

func stubAdminHTTPClient(t *testing.T, status int, body string, gotReq **http.Request) {
	t.Helper()
	stubAdminHTTPClientWithBody(t, status, body, gotReq, nil)
}

func stubAdminHTTPClientFunc(t *testing.T, handler func(*http.Request) (int, string)) {
	t.Helper()

	oldClient := httpClient
	t.Cleanup(func() {
		httpClient = oldClient
	})

	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			status, body := handler(req)
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
}

func stubAdminHTTPClientWithBody(t *testing.T, status int, body string, gotReq **http.Request, gotBody *[]byte) {
	t.Helper()

	oldClient := httpClient
	t.Cleanup(func() {
		httpClient = oldClient
	})

	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			*gotReq = req
			if gotBody != nil && req.Body != nil {
				content, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}
				*gotBody = content
				req.Body = io.NopCloser(bytes.NewReader(content))
			}
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
}

func stubFailingAdminHTTPClient(t *testing.T, called *bool) {
	t.Helper()

	oldClient := httpClient
	t.Cleanup(func() {
		httpClient = oldClient
	})

	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			*called = true
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader(`{"code":"unexpected"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}
