// Package protocol defines the stable Z-Courier wire packet format.
//
// It is transport-agnostic and does not expose Zinx types. Applications can
// use it to encode packets sent to a gateway and decode packets received from
// one while keeping business payloads as opaque byte slices.
package protocol
