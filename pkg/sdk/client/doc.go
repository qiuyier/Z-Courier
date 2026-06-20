// Package client provides a high-level TCP client for Z-Courier gateways.
//
// The package owns the outer Zinx transport frame and uses pkg/sdk/protocol
// for inner packets. Connection, AUTH/BIND, reconnect, ACK correlation, and
// downlink handling are added incrementally without exposing Zinx types.
package client
