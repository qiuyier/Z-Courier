package backend

import "time"

const (
	// InternalTokenHeader authenticates calls to the gateway internal API.
	InternalTokenHeader = "X-ZCourier-Internal-Token"

	// DeliveryStateSent means the gateway wrote the message to an online client.
	DeliveryStateSent = "sent"
	// DeliveryStateQueued means the gateway stored the message for later delivery.
	DeliveryStateQueued = "queued"
)

// PushRequest describes one opaque downlink message.
type PushRequest struct {
	ClientID    string `json:"client_id"`
	DeviceID    string `json:"device_id"`
	MsgID       uint32 `json:"msg_id"`
	MessageID   string `json:"message_id,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	AckRequired bool   `json:"ack_required,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

// PushResponse reports whether a downlink message was sent or queued.
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

// BatchPushRequest describes a group of downlink messages.
type BatchPushRequest struct {
	Messages []PushRequest `json:"messages"`
}

// BatchPushResponse contains one result for every submitted message.
// A partial failure is returned with HTTP 207 and does not make PushBatch
// return an error; inspect Failed and Results instead.
type BatchPushResponse struct {
	Code    string         `json:"code"`
	Reason  string         `json:"reason,omitempty"`
	Total   int            `json:"total"`
	Success int            `json:"success"`
	Failed  int            `json:"failed"`
	Results []PushResponse `json:"results"`
}

// MessageStatus is the persisted delivery state of a reliable message.
type MessageStatus string

const (
	MessageStatusPending   MessageStatus = "pending"
	MessageStatusSent      MessageStatus = "sent"
	MessageStatusDelivered MessageStatus = "delivered"
	MessageStatusFailed    MessageStatus = "failed"
	MessageStatusDiscarded MessageStatus = "discarded"
)

// Valid reports whether status is a known persisted delivery state.
func (status MessageStatus) Valid() bool {
	switch status {
	case MessageStatusPending, MessageStatusSent, MessageStatusDelivered, MessageStatusFailed, MessageStatusDiscarded:
		return true
	default:
		return false
	}
}

// MessageStatusRequest is the JSON form accepted by the status endpoint.
// Client.GetMessage uses the equivalent GET query form.
type MessageStatusRequest struct {
	MessageID string `json:"message_id"`
}

// MessageStatusResponse describes persisted delivery and retry metadata.
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

// ListMessagesRequest filters persisted messages. An empty Status asks the
// gateway to use its default failed-message filter. A zero Limit asks the
// gateway to use its default limit.
type ListMessagesRequest struct {
	Status MessageStatus
	Limit  int
}

// ListMessagesResponse contains persisted messages matching a status.
type ListMessagesResponse struct {
	Code     string                  `json:"code"`
	Reason   string                  `json:"reason,omitempty"`
	Status   MessageStatus           `json:"status,omitempty"`
	Limit    int                     `json:"limit,omitempty"`
	Total    int                     `json:"total"`
	Messages []MessageStatusResponse `json:"messages,omitempty"`
}

// MessageActionRequest is used by requeue and discard operations.
type MessageActionRequest struct {
	MessageID string `json:"message_id"`
	Reason    string `json:"reason,omitempty"`
}
