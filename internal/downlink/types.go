package downlink

import sdkbackend "github.com/qiuyier/Z-Courier/pkg/sdk/backend"

const InternalTokenHeader = sdkbackend.InternalTokenHeader

type PushRequest = sdkbackend.PushRequest

type PeerPushRequest struct {
	OriginNode  string `json:"origin_node,omitempty"`
	ClientID    string `json:"client_id"`
	DeviceID    string `json:"device_id"`
	SessionID   string `json:"session_id"`
	MsgID       uint32 `json:"msg_id"`
	MessageID   string `json:"message_id,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	AckRequired bool   `json:"ack_required,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

type PushResponse = sdkbackend.PushResponse
type BatchPushRequest = sdkbackend.BatchPushRequest
type BatchPushResponse = sdkbackend.BatchPushResponse
type MessageStatusRequest = sdkbackend.MessageStatusRequest
type MessageStatusResponse = sdkbackend.MessageStatusResponse
type ListMessagesResponse = sdkbackend.ListMessagesResponse
type MessageActionRequest = sdkbackend.MessageActionRequest

type PeerPushResponse struct {
	Code          string `json:"code"`
	Reason        string `json:"reason,omitempty"`
	DeliveryState string `json:"delivery_state,omitempty"`
	GatewayNode   string `json:"gateway_node,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	DeviceID      string `json:"device_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	ConnID        uint64 `json:"conn_id,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
}

type ClientAckRequest struct {
	MessageID string `json:"message_id"`
	Code      string `json:"code"`
}

const ClientAckCodeDelivered = "delivered"
