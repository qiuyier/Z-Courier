package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
)

func TestPushSendsInternalPushRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubHTTPClient(t, http.StatusAccepted, `{"code":"accepted"}`, &gotReq, &gotBody)

	err := push(pushConfig{
		InternalURL:   "http://gateway-a:18182/",
		InternalToken: "secret",
		ClientID:      "client-1",
		DeviceID:      "device-1",
		MsgID:         2001,
		MessageID:     "message-1",
		TraceID:       "trace-1",
		AckRequired:   true,
		Body:          "hello",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("push() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotReq.Method)
	}
	if gotReq.URL.String() != "http://gateway-a:18182/internal/push" {
		t.Fatalf("url = %s, want http://gateway-a:18182/internal/push", gotReq.URL.String())
	}
	if gotReq.Header.Get(downlink.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token header = %q, want secret", gotReq.Header.Get(downlink.InternalTokenHeader))
	}

	var gotPush downlink.PushRequest
	if err := sonic.Unmarshal(gotBody, &gotPush); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if gotPush.ClientID != "client-1" || gotPush.DeviceID != "device-1" || gotPush.MsgID != 2001 {
		t.Fatalf("identity = %q/%q/%d, want client-1/device-1/2001", gotPush.ClientID, gotPush.DeviceID, gotPush.MsgID)
	}
	if gotPush.MessageID != "message-1" || gotPush.TraceID != "trace-1" {
		t.Fatalf("ids = %q/%q, want message-1/trace-1", gotPush.MessageID, gotPush.TraceID)
	}
	if !gotPush.AckRequired {
		t.Fatal("AckRequired = false, want true")
	}
	if string(gotPush.Body) != "hello" {
		t.Fatalf("Body = %q, want hello", string(gotPush.Body))
	}
}

func TestBatchSendsInternalBatchPushRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubHTTPClient(t, http.StatusOK, `{"code":"ok","total":2,"success":2}`, &gotReq, &gotBody)

	err := batch(batchConfig{
		InternalURL:   "http://gateway-a:18182/",
		InternalToken: "secret",
		Messages: messageFlags{
			"client-1,device-1,2001,hello",
			"client-2,device-2,2002,world,with,comma",
		},
		AckRequired: true,
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("batch() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotReq.Method)
	}
	if gotReq.URL.String() != "http://gateway-a:18182/internal/push/batch" {
		t.Fatalf("url = %s, want http://gateway-a:18182/internal/push/batch", gotReq.URL.String())
	}
	if gotReq.Header.Get(downlink.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token header = %q, want secret", gotReq.Header.Get(downlink.InternalTokenHeader))
	}

	var gotPush downlink.BatchPushRequest
	if err := sonic.Unmarshal(gotBody, &gotPush); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(gotPush.Messages) != 2 {
		t.Fatalf("messages length = %d, want 2", len(gotPush.Messages))
	}
	if gotPush.Messages[0].ClientID != "client-1" || gotPush.Messages[0].DeviceID != "device-1" || gotPush.Messages[0].MsgID != 2001 {
		t.Fatalf("first identity = %q/%q/%d, want client-1/device-1/2001", gotPush.Messages[0].ClientID, gotPush.Messages[0].DeviceID, gotPush.Messages[0].MsgID)
	}
	if string(gotPush.Messages[0].Body) != "hello" {
		t.Fatalf("first body = %q, want hello", string(gotPush.Messages[0].Body))
	}
	if string(gotPush.Messages[1].Body) != "world,with,comma" {
		t.Fatalf("second body = %q, want world,with,comma", string(gotPush.Messages[1].Body))
	}
	if gotPush.Messages[0].MessageID == "" || gotPush.Messages[0].TraceID == "" {
		t.Fatal("first message id and trace id should be generated")
	}
}

func TestStatusSendsInternalStatusRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubHTTPClient(t, http.StatusOK, `{"code":"ok","message_id":"message-1","status":"delivered"}`, &gotReq, &gotBody)

	err := status(statusConfig{
		InternalURL:   "http://gateway-a:18182/",
		InternalToken: "secret",
		MessageID:     "message-1",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("status() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodGet {
		t.Fatalf("method = %s, want GET", gotReq.Method)
	}
	if gotReq.URL.Path != "/internal/message/status" {
		t.Fatalf("path = %s, want /internal/message/status", gotReq.URL.Path)
	}
	if gotReq.URL.Query().Get("message_id") != "message-1" {
		t.Fatalf("message_id query = %q, want message-1", gotReq.URL.Query().Get("message_id"))
	}
	if gotReq.Header.Get(downlink.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token header = %q, want secret", gotReq.Header.Get(downlink.InternalTokenHeader))
	}
	if len(gotBody) != 0 {
		t.Fatalf("body length = %d, want 0", len(gotBody))
	}
}

func TestListSendsInternalMessagesRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubHTTPClient(t, http.StatusOK, `{"code":"ok","status":"failed","total":0}`, &gotReq, &gotBody)

	err := listMessages(listConfig{
		InternalURL:   "http://gateway-a:18182/",
		InternalToken: "secret",
		Status:        "failed",
		Limit:         25,
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("listMessages() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodGet {
		t.Fatalf("method = %s, want GET", gotReq.Method)
	}
	if gotReq.URL.Path != "/internal/messages" {
		t.Fatalf("path = %s, want /internal/messages", gotReq.URL.Path)
	}
	if gotReq.URL.Query().Get("status") != "failed" {
		t.Fatalf("status query = %q, want failed", gotReq.URL.Query().Get("status"))
	}
	if gotReq.URL.Query().Get("limit") != "25" {
		t.Fatalf("limit query = %q, want 25", gotReq.URL.Query().Get("limit"))
	}
	if gotReq.Header.Get(downlink.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token header = %q, want secret", gotReq.Header.Get(downlink.InternalTokenHeader))
	}
	if len(gotBody) != 0 {
		t.Fatalf("body length = %d, want 0", len(gotBody))
	}
}

func TestRequeueSendsInternalRequeueRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubHTTPClient(t, http.StatusOK, `{"code":"ok","message_id":"message-1","status":"pending"}`, &gotReq, &gotBody)

	err := requeue(messageActionConfig{
		InternalURL:   "http://gateway-a:18182/",
		InternalToken: "secret",
		MessageID:     "message-1",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("requeue() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotReq.Method)
	}
	if gotReq.URL.String() != "http://gateway-a:18182/internal/message/requeue" {
		t.Fatalf("url = %s, want http://gateway-a:18182/internal/message/requeue", gotReq.URL.String())
	}
	if gotReq.Header.Get(downlink.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token header = %q, want secret", gotReq.Header.Get(downlink.InternalTokenHeader))
	}

	var gotAction downlink.MessageActionRequest
	if err := sonic.Unmarshal(gotBody, &gotAction); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if gotAction.MessageID != "message-1" {
		t.Fatalf("MessageID = %q, want message-1", gotAction.MessageID)
	}
}

func TestDiscardSendsInternalDiscardRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubHTTPClient(t, http.StatusOK, `{"code":"ok","message_id":"message-1","status":"discarded"}`, &gotReq, &gotBody)

	err := discard(messageActionConfig{
		InternalURL:   "http://gateway-a:18182/",
		InternalToken: "secret",
		MessageID:     "message-1",
		Reason:        "manual",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("discard() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotReq.Method)
	}
	if gotReq.URL.String() != "http://gateway-a:18182/internal/message/discard" {
		t.Fatalf("url = %s, want http://gateway-a:18182/internal/message/discard", gotReq.URL.String())
	}
	if gotReq.Header.Get(downlink.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token header = %q, want secret", gotReq.Header.Get(downlink.InternalTokenHeader))
	}

	var gotAction downlink.MessageActionRequest
	if err := sonic.Unmarshal(gotBody, &gotAction); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if gotAction.MessageID != "message-1" || gotAction.Reason != "manual" {
		t.Fatalf("action = %+v, want message-1 manual", gotAction)
	}
}

func TestRouteSendsInternalDebugRouteRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubHTTPClient(t, http.StatusOK, `{"code":"ok","local_session_found":true}`, &gotReq, &gotBody)

	err := route(routeConfig{
		InternalURL:   "http://gateway-a:18182/",
		InternalToken: "secret",
		ClientID:      "client-1",
		DeviceID:      "device-1",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("route() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodGet {
		t.Fatalf("method = %s, want GET", gotReq.Method)
	}
	if gotReq.URL.Path != "/internal/debug/route" {
		t.Fatalf("path = %s, want /internal/debug/route", gotReq.URL.Path)
	}
	if gotReq.URL.Query().Get("client_id") != "client-1" || gotReq.URL.Query().Get("device_id") != "device-1" {
		t.Fatalf("query = %s, want client/device", gotReq.URL.RawQuery)
	}
	if gotReq.Header.Get(downlink.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token header = %q, want secret", gotReq.Header.Get(downlink.InternalTokenHeader))
	}
	if len(gotBody) != 0 {
		t.Fatalf("body length = %d, want 0", len(gotBody))
	}
}

func TestSessionsSendsInternalDebugSessionsRequest(t *testing.T) {
	var gotReq *http.Request
	var gotBody []byte
	stubHTTPClient(t, http.StatusOK, `{"code":"ok","total":1}`, &gotReq, &gotBody)

	err := sessions(sessionsConfig{
		InternalURL:   "http://gateway-a:18182/",
		InternalToken: "secret",
		ClientID:      "client-1",
		Limit:         25,
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("sessions() error = %v", err)
	}

	if gotReq == nil {
		t.Fatal("request was not sent")
	}
	if gotReq.Method != http.MethodGet {
		t.Fatalf("method = %s, want GET", gotReq.Method)
	}
	if gotReq.URL.Path != "/internal/debug/sessions" {
		t.Fatalf("path = %s, want /internal/debug/sessions", gotReq.URL.Path)
	}
	if gotReq.URL.Query().Get("client_id") != "client-1" || gotReq.URL.Query().Get("limit") != "25" {
		t.Fatalf("query = %s, want client_id and limit", gotReq.URL.RawQuery)
	}
	if gotReq.Header.Get(downlink.InternalTokenHeader) != "secret" {
		t.Fatalf("internal token header = %q, want secret", gotReq.Header.Get(downlink.InternalTokenHeader))
	}
	if len(gotBody) != 0 {
		t.Fatalf("body length = %d, want 0", len(gotBody))
	}
}

func stubHTTPClient(t *testing.T, status int, body string, gotReq **http.Request, gotBody *[]byte) {
	t.Helper()

	oldClient := http.DefaultClient
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})

	http.DefaultClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			*gotReq = req
			if req.Body != nil {
				var err error
				*gotBody, err = io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("ReadAll() error = %v", err)
				}
			} else {
				*gotBody = nil
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
