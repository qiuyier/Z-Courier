package downlink

import "time"

type PushRequest struct {
	ClientID    string `json:"client_id"`
	DeviceID    string `json:"device_id"`
	MsgID       uint32 `json:"msg_id"`
	MessageID   string `json:"message_id,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	AckRequired bool   `json:"ack_required,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

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

type PushResponse struct {
	Code          string `json:"code"`
	Reason        string `json:"reason,omitempty"`
	DeliveryState string `json:"delivery_state,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	DeviceID      string `json:"device_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	ConnID        uint64 `json:"conn_id,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	TraceID       string `json:"trace_id,omitempty"`
}

type BatchPushRequest struct {
	Messages []PushRequest `json:"messages"`
}

type BatchPushResponse struct {
	Code    string         `json:"code"`
	Reason  string         `json:"reason,omitempty"`
	Total   int            `json:"total"`
	Success int            `json:"success"`
	Failed  int            `json:"failed"`
	Results []PushResponse `json:"results"`
}

type MessageStatusRequest struct {
	MessageID string `json:"message_id"`
}

type MessageStatusResponse struct {
	Code          string        `json:"code"`
	Reason        string        `json:"reason,omitempty"`
	MessageID     string        `json:"message_id,omitempty"`
	ClientID      string        `json:"client_id,omitempty"`
	DeviceID      string        `json:"device_id,omitempty"`
	MsgID         uint32        `json:"msg_id,omitempty"`
	TraceID       string        `json:"trace_id,omitempty"`
	SessionID     string        `json:"session_id,omitempty"`
	Status        MessageStatus `json:"status,omitempty"`
	Attempts      int           `json:"attempts,omitempty"`
	LastError     string        `json:"last_error,omitempty"`
	NextRetryAt   *time.Time    `json:"next_retry_at,omitempty"`
	ClaimOwner    string        `json:"claim_owner,omitempty"`
	ClaimUntil    *time.Time    `json:"claim_until,omitempty"`
	CreatedAt     *time.Time    `json:"created_at,omitempty"`
	UpdatedAt     *time.Time    `json:"updated_at,omitempty"`
	SentAt        *time.Time    `json:"sent_at,omitempty"`
	DeliveredAt   *time.Time    `json:"delivered_at,omitempty"`
	AckRequired   bool          `json:"ack_required,omitempty"`
	BodySizeBytes int           `json:"body_size_bytes,omitempty"`
}

type ListMessagesResponse struct {
	Code     string                  `json:"code"`
	Reason   string                  `json:"reason,omitempty"`
	Status   MessageStatus           `json:"status,omitempty"`
	Limit    int                     `json:"limit,omitempty"`
	Total    int                     `json:"total"`
	Messages []MessageStatusResponse `json:"messages,omitempty"`
}

type MessageActionRequest struct {
	MessageID string `json:"message_id"`
	Reason    string `json:"reason,omitempty"`
}

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
