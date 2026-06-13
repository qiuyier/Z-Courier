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
	oldClient := http.DefaultClient
	t.Cleanup(func() {
		http.DefaultClient = oldClient
	})

	var gotReq *http.Request
	var gotBody []byte
	http.DefaultClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotReq = req
			var err error
			gotBody, err = io.ReadAll(req.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusAccepted,
				Body:       io.NopCloser(strings.NewReader(`{"code":"accepted"}`)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
