package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

func main() {
	host := flag.String("host", "127.0.0.1", "gateway host")
	port := flag.Int("port", 8999, "gateway tcp port")
	clientID := flag.String("client-id", "dev-client", "claimed client id")
	deviceID := flag.String("device-id", "device-1", "device id")
	token := flag.String("token", "dev-token", "auth token")
	msgID := flag.Uint("msg-id", uint(protocol.MsgIDBind), "AUTH/BIND message id")
	upstreamMsgID := flag.Uint("upstream-msg-id", 0, "optional business upstream message id sent after AUTH/BIND succeeds")
	upstreamBody := flag.String("upstream-body", "devclient-upstream", "optional business upstream body")
	flag.Parse()

	bindMessageID := fmt.Sprintf("devclient-bind-%d", time.Now().UnixNano())
	client := znet.NewClient(*host, *port)
	client.AddRouter(protocol.MsgIDAck, &packetRouter{
		name:          "ack",
		token:         *token,
		clientID:      *clientID,
		deviceID:      *deviceID,
		bindMessageID: bindMessageID,
		upstreamMsgID: uint32(*upstreamMsgID),
		upstreamBody:  []byte(*upstreamBody),
	})
	client.AddRouter(2001, &packetRouter{name: "downlink", token: *token})
	client.SetOnConnStart(func(conn ziface.IConnection) {
		fmt.Printf("connected: local=%s remote=%s conn_id=%d\n", conn.LocalAddrString(), conn.RemoteAddrString(), conn.GetConnID())
		if err := sendPacket(conn, packetInput{
			msgID:     uint32(*msgID),
			clientID:  *clientID,
			deviceID:  *deviceID,
			token:     *token,
			messageID: bindMessageID,
			body:      []byte("devclient-bind"),
		}); err != nil {
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

type packetInput struct {
	msgID     uint32
	clientID  string
	deviceID  string
	token     string
	messageID string
	body      []byte
}

func sendPacket(conn ziface.IConnection, input packetInput) error {
	packet := protocol.NewPacket(input.msgID, input.body)
	packet.ClientID = input.clientID
	packet.DeviceID = input.deviceID
	packet.Token = input.token
	packet.MessageID = input.messageID
	if packet.MessageID == "" {
		packet.MessageID = fmt.Sprintf("devclient-%d", time.Now().UnixNano())
	}
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

	name             string
	token            string
	clientID         string
	deviceID         string
	bindMessageID    string
	upstreamMsgID    uint32
	upstreamBody     []byte
	sendUpstreamOnce sync.Once
}

func (r *packetRouter) Handle(request ziface.IRequest) {
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

	if r.name == "downlink" && packet.Flags&protocol.FlagAckRequired != 0 && packet.MessageID != "" {
		if err := sendDownlinkAck(request.GetConnection(), packet, r.token); err != nil {
			fmt.Printf("[downlink] send ack failed: message_id=%s error=%v\n", packet.MessageID, err)
		}
	}
	if r.name == "ack" && r.upstreamMsgID != 0 {
		r.maybeSendUpstreamAfterBindAck(request.GetConnection(), packet.Body)
	}
}

func (r *packetRouter) maybeSendUpstreamAfterBindAck(conn ziface.IConnection, body []byte) {
	var ack protocol.Ack
	if err := sonic.Unmarshal(body, &ack); err != nil {
		fmt.Printf("[ack] unmarshal failed: %v\n", err)
		return
	}
	if ack.MessageID != r.bindMessageID || ack.Code != protocol.AckAccepted {
		return
	}

	r.sendUpstreamOnce.Do(func() {
		messageID := fmt.Sprintf("devclient-upstream-%d", time.Now().UnixNano())
		if err := sendPacket(conn, packetInput{
			msgID:     r.upstreamMsgID,
			clientID:  r.clientID,
			deviceID:  r.deviceID,
			token:     r.token,
			messageID: messageID,
			body:      r.upstreamBody,
		}); err != nil {
			fmt.Printf("[upstream] send failed: msg_id=%d error=%v\n", r.upstreamMsgID, err)
			return
		}
		fmt.Printf("[upstream] sent msg_id=%d message_id=%s\n", r.upstreamMsgID, messageID)
	})
}

func sendDownlinkAck(conn ziface.IConnection, origin *protocol.Packet, token string) error {
	body, err := sonic.Marshal(downlink.ClientAckRequest{
		MessageID: origin.MessageID,
		Code:      downlink.ClientAckCodeDelivered,
	})
	if err != nil {
		return err
	}

	packet := protocol.NewPacket(protocol.MsgIDDownlinkAck, body)
	packet.ClientID = origin.ClientID
	packet.DeviceID = origin.DeviceID
	packet.SessionID = origin.SessionID
	packet.MessageID = origin.MessageID
	packet.TraceID = origin.TraceID
	packet.Token = token
	packet.Timestamp = time.Now().UnixMilli()

	data, err := protocol.Encode(packet)
	if err != nil {
		return err
	}

	return conn.SendMsg(packet.MsgID, data)
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
