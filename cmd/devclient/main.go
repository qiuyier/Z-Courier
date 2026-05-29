package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

func main() {
	host := flag.String("host", "127.0.0.1", "gateway host")
	port := flag.Int("port", 8999, "gateway tcp port")
	clientID := flag.String("client-id", "dev-client", "claimed client id")
	deviceID := flag.String("device-id", "device-1", "device id")
	token := flag.String("token", "dev-token", "auth token")
	msgID := flag.Uint("msg-id", 1000, "upstream bind message id")
	flag.Parse()

	client := znet.NewClient(*host, *port)
	client.AddRouter(protocol.MsgIDAck, &packetRouter{name: "ack"})
	client.AddRouter(2001, &packetRouter{name: "downlink"})
	client.SetOnConnStart(func(conn ziface.IConnection) {
		fmt.Printf("connected: local=%s remote=%s conn_id=%d\n", conn.LocalAddrString(), conn.RemoteAddrString(), conn.GetConnID())
		if err := sendBindPacket(conn, uint32(*msgID), *clientID, *deviceID, *token); err != nil {
			fmt.Printf("send bind packet failed: %v\n", err)
			conn.Stop()
		}
	})
	client.SetOnConnStop(func(conn ziface.IConnection) {
		fmt.Printf("connection closed: conn_id=%d\n", conn.GetConnID())
	})

	client.Start()

	waitForExit(client)
}

func sendBindPacket(conn ziface.IConnection, msgID uint32, clientID, deviceID, token string) error {
	packet := protocol.NewPacket(msgID, []byte("devclient-bind"))
	packet.ClientID = clientID
	packet.DeviceID = deviceID
	packet.Token = token
	packet.MessageID = fmt.Sprintf("devclient-%d", time.Now().UnixNano())
	packet.TraceID = packet.MessageID
	packet.Timestamp = time.Now().UnixMilli()
	packet.Flags = protocol.FlagAckRequired

	data, err := protocol.Encode(packet)
	if err != nil {
		return err
	}

	return conn.SendMsg(packet.MsgID, data)
}

type packetRouter struct {
	znet.BaseRouter

	name string
}

func (r packetRouter) Handle(request ziface.IRequest) {
	packet, err := protocol.Decode(request.GetData())
	if err != nil {
		fmt.Printf("[%s] decode failed: outer_msg_id=%d error=%v\n", r.name, request.GetMsgID(), err)
		return
	}

	fmt.Printf(
		"[%s] outer_msg_id=%d packet_msg_id=%d client_id=%s device_id=%s session_id=%s message_id=%s trace_id=%s flags=%d body=%s\n",
		r.name,
		request.GetMsgID(),
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

func waitForExit(client ziface.IClient) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-signals:
		fmt.Printf("exit signal: %s\n", sig)
	case err := <-client.GetErrChan():
		fmt.Printf("client error: %v\n", err)
	}

	client.Stop()
}
