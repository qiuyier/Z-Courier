package backend

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	tests := []Config{
		{},
		{BaseURL: "gateway:18182"},
		{BaseURL: "ftp://gateway:18182"},
		{BaseURL: "http://user:secret@gateway:18182"},
		{BaseURL: "http://gateway:18182?token=secret"},
		{BaseURL: "http://gateway:18182", Timeout: -time.Second},
		{BaseURL: "http://gateway:18182", MaxResponseBodySize: -1},
		{BaseURL: "http://gateway:18182", InternalToken: "token", HMAC: &HMACConfig{KeyID: "key", Secret: testHMACSecret}},
		{BaseURL: "http://gateway:18182", HMAC: &HMACConfig{KeyID: "key", Secret: []byte("short")}},
	}

	for _, config := range tests {
		if _, err := NewClient(config); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("NewClient(%+v) error = %v, want ErrInvalidConfig", config, err)
		}
	}
}

func TestClientSignsRequestsWithHMAC(t *testing.T) {
	verifier, err := signing.NewVerifier(signing.VerifierConfig{
		Keys: map[string][]byte{"backend-1": testHMACSecret},
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll() error = %v", err)
		}
		if err := verifier.Verify(r, body); err != nil {
			t.Errorf("Verify() error = %v", err)
		}
		if got := r.Header.Get(InternalTokenHeader); got != "" {
			t.Errorf("internal token = %q, want empty in HMAC mode", got)
		}
		writeTestJSON(t, w, http.StatusAccepted, PushResponse{Code: "ok", DeliveryState: DeliveryStateQueued})
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL: server.URL,
		HMAC: &HMACConfig{
			KeyID:  "backend-1",
			Secret: testHMACSecret,
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Push(context.Background(), PushRequest{
		ClientID: "client-1",
		DeviceID: "device-1",
		MsgID:    2001,
		Body:     []byte("hello"),
	}); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
}

func TestNewClientDoesNotMutateProvidedHTTPClient(t *testing.T) {
	originalError := errors.New("original redirect policy")
	httpClient := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return originalError
		},
	}

	if _, err := NewClient(Config{BaseURL: "http://gateway:18182", HTTPClient: httpClient}); err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if err := httpClient.CheckRedirect(nil, nil); !errors.Is(err, originalError) {
		t.Fatalf("provided HTTP client was mutated: error = %v", err)
	}
}

func TestPushSendsTokenAndDecodesQueuedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != pushPath {
			t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, pushPath)
		}
		if got := r.Header.Get(InternalTokenHeader); got != "secret" {
			t.Errorf("internal token = %q, want secret", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}

		var request PushRequest
		if err := sonic.ConfigDefault.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.ClientID != "client-1" || request.DeviceID != "device-1" || request.MsgID != 2001 {
			t.Errorf("request = %+v", request)
		}
		if string(request.Body) != "hello" {
			t.Errorf("body = %q, want hello", request.Body)
		}

		writeTestJSON(t, w, http.StatusAccepted, PushResponse{
			Code:          "ok",
			DeliveryState: DeliveryStateQueued,
			ClientID:      request.ClientID,
			DeviceID:      request.DeviceID,
			MessageID:     "message-1",
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL+"/", "secret")
	response, err := client.Push(context.Background(), PushRequest{
		ClientID: "client-1",
		DeviceID: "device-1",
		MsgID:    2001,
		Body:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if response.DeliveryState != DeliveryStateQueued || response.MessageID != "message-1" {
		t.Fatalf("response = %+v, want queued message-1", response)
	}
}

func TestPushReturnsMessageIDConflictAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(t, w, http.StatusConflict, PushResponse{
			Code:      "message_id_conflict",
			Reason:    "immutable identity differs",
			MessageID: "message-1",
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	_, err := client.Push(context.Background(), PushRequest{
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		MessageID: "message-1",
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Push() error = %v, want APIError", err)
	}
	if apiErr.StatusCode != http.StatusConflict || apiErr.Code != "message_id_conflict" {
		t.Fatalf("APIError = %+v", apiErr)
	}
}

func TestPushBatchDecodesMultiStatusWithoutReturningError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, http.StatusMultiStatus, BatchPushResponse{
			Code:    "partial_failure",
			Total:   2,
			Success: 1,
			Failed:  1,
			Results: []PushResponse{
				{Code: "ok", MessageID: "message-1"},
				{Code: "session_not_found", MessageID: "message-2", Reason: "offline"},
			},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	response, err := client.PushBatch(context.Background(), BatchPushRequest{Messages: []PushRequest{
		{ClientID: "client-1", DeviceID: "device-1", MsgID: 2001},
		{ClientID: "client-2", DeviceID: "device-2", MsgID: 2001},
	}})
	if err != nil {
		t.Fatalf("PushBatch() error = %v", err)
	}
	if response.Code != "partial_failure" || response.Success != 1 || response.Failed != 1 || len(response.Results) != 2 {
		t.Fatalf("response = %+v", response)
	}
}

func TestMessageQueriesEncodeParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case statusPath:
			if got := r.URL.Query().Get("message_id"); got != "message / 1" {
				t.Errorf("message_id = %q, want escaped message / 1", got)
			}
			writeTestJSON(t, w, http.StatusOK, MessageStatusResponse{
				Code:                  "ok",
				MessageID:             "message / 1",
				PolicyName:            "critical",
				Status:                MessageStatusFailed,
				TerminalReason:        "max_attempts_exceeded",
				TerminalPublishStatus: "published",
			})
		case listPath:
			if got := r.URL.Query().Get("status"); got != string(MessageStatusFailed) {
				t.Errorf("status = %q, want failed", got)
			}
			if got := r.URL.Query().Get("limit"); got != "25" {
				t.Errorf("limit = %q, want 25", got)
			}
			if got := r.URL.Query().Get("cursor"); got != "cursor-1" {
				t.Errorf("cursor = %q, want cursor-1", got)
			}
			writeTestJSON(t, w, http.StatusOK, ListMessagesResponse{
				Code:       "ok",
				Status:     MessageStatusFailed,
				Limit:      25,
				Cursor:     "cursor-1",
				NextCursor: "cursor-2",
				HasMore:    true,
				Total:      1,
				Messages: []MessageStatusResponse{{
					Code: "ok", MessageID: "message-2", Status: MessageStatusFailed,
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	status, err := client.GetMessage(context.Background(), " message / 1 ")
	if err != nil {
		t.Fatalf("GetMessage() error = %v", err)
	}
	if status.MessageID != "message / 1" || status.PolicyName != "critical" || status.Status != MessageStatusFailed ||
		status.TerminalReason != "max_attempts_exceeded" || status.TerminalPublishStatus != "published" {
		t.Fatalf("status response = %+v", status)
	}

	list, err := client.ListMessages(context.Background(), ListMessagesRequest{Status: MessageStatusFailed, Limit: 25, Cursor: " cursor-1 "})
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if list.Total != 1 || !list.HasMore || list.NextCursor != "cursor-2" || len(list.Messages) != 1 || list.Messages[0].MessageID != "message-2" {
		t.Fatalf("list response = %+v", list)
	}
}

func TestMessageActionsSendExpectedBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request MessageActionRequest
		if err := sonic.ConfigDefault.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}

		status := MessageStatusPending
		switch r.URL.Path {
		case requeuePath:
			if request.MessageID != "message-1" || request.Reason != "" {
				t.Errorf("requeue request = %+v", request)
			}
		case discardPath:
			status = MessageStatusDiscarded
			if request.MessageID != "message-2" || request.Reason != "manual" {
				t.Errorf("discard request = %+v", request)
			}
		default:
			http.NotFound(w, r)
			return
		}

		writeTestJSON(t, w, http.StatusOK, MessageStatusResponse{
			Code: "ok", MessageID: request.MessageID, Status: status,
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	if response, err := client.Requeue(context.Background(), " message-1 "); err != nil || response.Status != MessageStatusPending {
		t.Fatalf("Requeue() response = %+v, error = %v", response, err)
	}
	if response, err := client.Discard(context.Background(), "message-2", " manual "); err != nil || response.Status != MessageStatusDiscarded {
		t.Fatalf("Discard() response = %+v, error = %v", response, err)
	}
}

func TestAPIErrorPreservesGatewayStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(t, w, http.StatusTooManyRequests, PushResponse{Code: "overloaded", Reason: "try later"})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	_, err := client.Push(context.Background(), PushRequest{ClientID: "client-1", DeviceID: "device-1", MsgID: 2001})
	if !errors.Is(err, ErrAPI) {
		t.Fatalf("Push() error = %v, want ErrAPI", err)
	}
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("Push() error type = %T, want *APIError", err)
	}
	if apiError.StatusCode != http.StatusTooManyRequests || apiError.Code != "overloaded" || !apiError.Retryable() {
		t.Fatalf("APIError = %+v", apiError)
	}
}

func TestClientRejectsInvalidAndOversizedResponses(t *testing.T) {
	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not-json"))
		}))
		defer server.Close()

		client := newTestClient(t, server.URL, "")
		_, err := client.GetMessage(context.Background(), "message-1")
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("GetMessage() error = %v, want ErrInvalidResponse", err)
		}
	})

	t.Run("missing code", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"message_id":"message-1"}`))
		}))
		defer server.Close()

		client := newTestClient(t, server.URL, "")
		_, err := client.GetMessage(context.Background(), "message-1")
		if !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("GetMessage() error = %v, want ErrInvalidResponse", err)
		}
	})

	t.Run("response too large", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":"ok","reason":"response exceeds limit"}`))
		}))
		defer server.Close()

		client, err := NewClient(Config{BaseURL: server.URL, MaxResponseBodySize: 16})
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		_, err = client.GetMessage(context.Background(), "message-1")
		if !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("GetMessage() error = %v, want ErrResponseTooLarge", err)
		}
	})
}

func TestClientTimeoutPreservesContextError(t *testing.T) {
	client, err := NewClient(Config{
		BaseURL: "http://gateway:18182",
		Timeout: 10 * time.Millisecond,
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.GetMessage(context.Background(), "message-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetMessage() error = %v, want context deadline exceeded", err)
	}
	var requestError *RequestError
	if !errors.As(err, &requestError) {
		t.Fatalf("GetMessage() error type = %T, want *RequestError", err)
	}
}

func TestClientRejectsRedirectWithoutLeakingToken(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	client := newTestClient(t, redirect.URL, "secret")
	_, err := client.GetMessage(context.Background(), "message-1")
	if !errors.Is(err, ErrRedirect) {
		t.Fatalf("GetMessage() error = %v, want ErrRedirect", err)
	}
	if got := targetCalls.Load(); got != 0 {
		t.Fatalf("redirect target calls = %d, want 0", got)
	}
}

func TestClientValidatesArgumentsBeforeSending(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "")
	tests := []func() error{
		func() error {
			_, err := client.Push(nil, PushRequest{ClientID: "client-1", DeviceID: "device-1", MsgID: 2001})
			return err
		},
		func() error { _, err := client.Push(context.Background(), PushRequest{}); return err },
		func() error { _, err := client.PushBatch(context.Background(), BatchPushRequest{}); return err },
		func() error { _, err := client.GetMessage(context.Background(), " "); return err },
		func() error {
			_, err := client.ListMessages(context.Background(), ListMessagesRequest{Status: "unknown"})
			return err
		},
		func() error { _, err := client.Requeue(context.Background(), ""); return err },
	}
	for index, call := range tests {
		if err := call(); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d error = %v, want ErrInvalidArgument", index, err)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("HTTP calls = %d, want 0", got)
	}
}

func TestClientPreservesInternalTokenBytes(t *testing.T) {
	client, err := NewClient(Config{BaseURL: "http://gateway:18182", InternalToken: " secret "})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.internalToken != " secret " {
		t.Fatalf("internal token = %q, want configured value preserved", client.internalToken)
	}
}

func newTestClient(t *testing.T, baseURL, token string) *Client {
	t.Helper()
	client, err := NewClient(Config{BaseURL: baseURL, InternalToken: token})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	data, err := sonic.Marshal(value)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

var testHMACSecret = []byte("0123456789abcdef0123456789abcdef")
