package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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
		ClientID: "client-1",
		Limit:    25,
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
	if gotReq.URL.Query().Get("client_id") != "client-1" || gotReq.URL.Query().Get("limit") != "25" {
		t.Fatalf("query = %s, want client_id and limit", gotReq.URL.RawQuery)
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

	oldClient := httpClient
	t.Cleanup(func() {
		httpClient = oldClient
	})

	httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			*gotReq = req
			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(body)),
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
