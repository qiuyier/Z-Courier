package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	sdkclient "github.com/qiuyier/Z-Courier/pkg/sdk/client"
	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func main() {
	address := flag.String("address", "127.0.0.1:8999", "gateway TCP address")
	clientID := flag.String("client-id", "example-client", "claimed client ID")
	deviceID := flag.String("device-id", "worker-1", "device ID")
	msgID := flag.Uint("msg-id", 2001, "business upstream MsgID")
	body := flag.String("body", `{"source":"go-example"}`, "business upstream body")
	flag.Parse()
	if uint64(*msgID) > uint64(^uint32(0)) || protocol.IsReservedMsgID(uint32(*msgID)) {
		log.Fatalf("msg-id %d must be a non-reserved uint32", *msgID)
	}

	runContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	gateway, err := sdkclient.New(sdkclient.Config{
		Address:       *address,
		ClientID:      *clientID,
		DeviceID:      *deviceID,
		TokenProvider: tokenFromEnvironment,
		DownlinkHandler: func(_ context.Context, packet *protocol.Packet) error {
			return processDownlink(packet)
		},
		OnDownlinkError: func(err error) {
			log.Printf("downlink failed: %v", err)
		},
		Reconnect: &sdkclient.ReconnectConfig{
			InitialDelay: 500 * time.Millisecond,
			MaxDelay:     30 * time.Second,
			Multiplier:   2,
			Jitter:       0.2,
			MaxAttempts:  0,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := gateway.Close(); err != nil {
			log.Printf("close client: %v", err)
		}
	}()

	if err := gateway.Connect(runContext); err != nil {
		log.Fatal(err)
	}
	binding := gateway.Binding()
	log.Printf("connected client_id=%s device_id=%s session_id=%s", binding.ClientID, binding.DeviceID, binding.SessionID)

	result, err := gateway.Send(runContext, sdkclient.SendRequest{
		MsgID:       uint32(*msgID),
		Body:        []byte(*body),
		AckRequired: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("upstream accepted message_id=%s code=%s", result.MessageID, result.Ack.Code)

	<-runContext.Done()
}

func tokenFromEnvironment(context.Context) (string, error) {
	token := os.Getenv("ZCOURIER_CLIENT_TOKEN")
	if token == "" {
		return "", fmt.Errorf("ZCOURIER_CLIENT_TOKEN is required")
	}
	return token, nil
}

func processDownlink(packet *protocol.Packet) error {
	// Replace this with an idempotent transaction keyed by packet.MessageID.
	log.Printf("downlink msg_id=%d message_id=%s body=%s", packet.MsgID, packet.MessageID, packet.Body)
	return nil
}
