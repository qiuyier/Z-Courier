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

func stubHTTPClient(t *testing.T, status int, body string, gotReq **http.Request, gotBody *[]byte) {
	t.Helper()

	oldClient := http.DefaultClient
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})

	http.DefaultClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			*gotReq = req
			var err error
			*gotBody, err = io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
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
