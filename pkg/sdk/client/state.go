package client

// State describes the client connection lifecycle.
type State uint8

const (
	StateDisconnected State = iota
	StateConnecting
	StateBinding
	StateReady
	StateReconnectWait
	StateClosing
	StateClosed
)

func (state State) String() string {
	switch state {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateBinding:
		return "binding"
	case StateReady:
		return "ready"
	case StateReconnectWait:
		return "reconnect_wait"
	case StateClosing:
		return "closing"
	case StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// Binding is the identity accepted by the gateway for the active connection.
// ClientID can differ from the claimed configuration when the verifier
// canonicalizes identity from the token principal.
type Binding struct {
	ClientID  string
	DeviceID  string
	SessionID string
}
