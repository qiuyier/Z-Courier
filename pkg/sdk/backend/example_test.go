package backend_test

import (
	"context"
	"errors"
	"log"

	"github.com/qiuyier/Z-Courier/pkg/sdk/backend"
)

func ExampleClient_Push() {
	client, err := backend.NewClient(backend.Config{
		BaseURL:       "http://gateway:18182",
		InternalToken: "replace-with-shared-secret",
	})
	if err != nil {
		log.Fatal(err)
	}

	response, err := client.Push(context.Background(), backend.PushRequest{
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		MessageID:   "message-1",
		TraceID:     "trace-1",
		AckRequired: true,
		Body:        []byte("hello"),
	})
	if err != nil {
		var apiError *backend.APIError
		if errors.As(err, &apiError) && apiError.Retryable() {
			// Retry later using application backoff and idempotent MessageID.
			return
		}
		log.Fatal(err)
	}

	_ = response.DeliveryState
}
