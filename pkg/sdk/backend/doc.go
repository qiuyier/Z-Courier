// Package backend provides a context-aware client for Z-Courier's internal
// HTTP downlink and message administration APIs.
//
// The package keeps message bodies opaque as []byte. It does not implement the
// client-to-gateway TCP protocol; use pkg/sdk/protocol for packet encoding.
package backend
