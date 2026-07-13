package downlink

import (
	sdkbackend "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
	sdkprotocol "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
)

const InternalTokenHeader = sdkbackend.InternalTokenHeader
const MaxBulkRequeueMessages = sdkbackend.MaxBulkRequeueMessages

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
type BulkRequeueRequest = sdkbackend.BulkRequeueRequest
type BulkRequeueResponse = sdkbackend.BulkRequeueResponse
type RetryScanRequest = sdkbackend.RetryScanRequest
type RetryScanResponse = sdkbackend.RetryScanResponse

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

type ClientAckRequest = sdkprotocol.DeliveryAck

const ClientAckCodeDelivered = sdkprotocol.DeliveryAckDelivered
