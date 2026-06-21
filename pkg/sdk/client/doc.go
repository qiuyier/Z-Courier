// Package client provides a high-level TCP client for Z-Courier gateways.
//
// The package owns the outer Zinx transport frame and uses pkg/sdk/protocol
// for inner packets. It provides connection, AUTH/BIND, concurrent send, ACK
// correlation, raw receive, handler-based downlink processing, and delivery
// acknowledgement without exposing Zinx types. Reconnect is added in a later
// V4 stage.
package client
