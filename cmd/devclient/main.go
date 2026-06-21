package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bytedance/sonic"
	sdkclient "github.com/qiuyier/Z-Courier/pkg/sdk/client"
	"github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

func main() {
	host := flag.String("host", "127.0.0.1", "gateway host")
	port := flag.Int("port", 8999, "gateway tcp port")
	clientID := flag.String("client-id", "dev-client", "claimed client id")
	deviceID := flag.String("device-id", "device-1", "device id")
	token := flag.String("token", "dev-token", "auth token")
	msgID := flag.Uint("msg-id", uint(protocol.MsgIDBind), "AUTH/BIND message id (must be 1000)")
	upstreamMsgID := flag.Uint("upstream-msg-id", 0, "optional business upstream message id sent after AUTH/BIND succeeds")
	upstreamBody := flag.String("upstream-body", "devclient-upstream", "optional business upstream body")
	flag.Parse()

	if uint32(*msgID) != protocol.MsgIDBind {
		fmt.Fprintf(os.Stderr, "invalid -msg-id %d: AUTH/BIND uses reserved MsgID %d\n", *msgID, protocol.MsgIDBind)
		os.Exit(2)
	}

	runContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	gateway, err := sdkclient.New(sdkclient.Config{
		Address:  net.JoinHostPort(*host, fmt.Sprintf("%d", *port)),
		ClientID: *clientID,
		DeviceID: *deviceID,
		Token:    *token,
		DownlinkHandler: func(_ context.Context, packet *protocol.Packet) error {
			printPacket("downlink", packet)
			return nil
		},
		OnDownlinkError: func(err error) {
			fmt.Printf("[downlink] processing failed: %v\n", err)
		},
		Reconnect: &sdkclient.ReconnectConfig{
			InitialDelay: 250 * time.Millisecond,
			MaxDelay:     10 * time.Second,
			Multiplier:   2,
			Jitter:       0.2,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create client failed: %v\n", err)
		os.Exit(1)
	}
	defer gateway.Close()

	if err := gateway.Connect(runContext); err != nil {
		fmt.Fprintf(os.Stderr, "connect failed: %v\n", err)
		os.Exit(1)
	}
	binding := gateway.Binding()
	fmt.Printf(
		"connected: remote=%s client_id=%s device_id=%s session_id=%s\n",
		net.JoinHostPort(*host, fmt.Sprintf("%d", *port)),
		binding.ClientID,
		binding.DeviceID,
		binding.SessionID,
	)

	if *upstreamMsgID != 0 {
		if err := sendUpstream(runContext, gateway, uint32(*upstreamMsgID), []byte(*upstreamBody)); err != nil {
			fmt.Fprintf(os.Stderr, "[upstream] send failed: msg_id=%d error=%v\n", *upstreamMsgID, err)
			os.Exit(1)
		}
	}

	<-runContext.Done()
	fmt.Printf("exit: %v\n", runContext.Err())
}

func sendUpstream(ctx context.Context, gateway *sdkclient.Client, msgID uint32, body []byte) error {
	messageID := fmt.Sprintf("devclient-upstream-%d", time.Now().UnixNano())
	result, err := gateway.Send(ctx, sdkclient.SendRequest{
		MsgID:       msgID,
		Body:        body,
		MessageID:   messageID,
		AckRequired: true,
	})
	if err != nil {
		return err
	}
	fmt.Printf("[upstream] sent msg_id=%d message_id=%s\n", msgID, result.MessageID)
	if result.Ack != nil {
		fmt.Printf(
			"[ack] code=%s msg_id=%d message_id=%s reason=%s\n",
			result.Ack.Code,
			result.Ack.MsgID,
			result.Ack.MessageID,
			result.Ack.Reason,
		)
	}
	return nil
}

func printPacket(name string, packet *protocol.Packet) {
	fmt.Printf(
		"[%s] packet_msg_id=%d client_id=%s device_id=%s session_id=%s message_id=%s trace_id=%s flags=%d body=%s\n",
		name,
		packet.MsgID,
		packet.ClientID,
		packet.DeviceID,
		packet.SessionID,
		packet.MessageID,
		packet.TraceID,
		packet.Flags,
		formatBody(packet.Body),
	)
}

func formatBody(body []byte) string {
	var decoded any
	if err := sonic.Unmarshal(body, &decoded); err == nil {
		if pretty, err := sonic.MarshalString(decoded); err == nil {
			return pretty
		}
	}
	return string(body)
}
