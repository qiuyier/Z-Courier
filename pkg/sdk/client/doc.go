// Package client provides a high-level TCP client for Z-Courier gateways.
//
// The package owns the outer Zinx transport frame and uses pkg/sdk/protocol
// for inner packets. It provides connection, AUTH/BIND, concurrent send, ACK
// correlation, and raw receive without exposing Zinx types. Reconnect and
// downlink delivery acknowledgement are added in later V4 stages.
package client
