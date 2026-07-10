package downlink

import "testing"

func TestMessageIdentityFingerprintIgnoresMutableMetadata(t *testing.T) {
	left := Message{
		MessageID:   "message-1",
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		AckRequired: true,
		Body:        []byte("hello"),
		TraceID:     "trace-1",
		Status:      MessageStatusPending,
	}
	right := left.Clone()
	right.TraceID = "trace-2"
	right.SessionID = "session-2"
	right.Status = MessageStatusDelivered
	right.Attempts = 9

	if !messagesHaveSameIdentity(left, right) {
		t.Fatal("mutable delivery metadata changed immutable identity")
	}
}

func TestMessageIdentityFingerprintDetectsImmutableChanges(t *testing.T) {
	base := Message{
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		AckRequired: true,
		Body:        []byte("hello"),
	}
	tests := []struct {
		name   string
		mutate func(*Message)
	}{
		{name: "client", mutate: func(message *Message) { message.ClientID = "client-2" }},
		{name: "device", mutate: func(message *Message) { message.DeviceID = "device-2" }},
		{name: "msg id", mutate: func(message *Message) { message.MsgID = 2002 }},
		{name: "ack required", mutate: func(message *Message) { message.AckRequired = false }},
		{name: "body", mutate: func(message *Message) { message.Body = []byte("world") }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base.Clone()
			test.mutate(&changed)
			if messagesHaveSameIdentity(base, changed) {
				t.Fatalf("%s change did not change immutable identity", test.name)
			}
		})
	}
}
