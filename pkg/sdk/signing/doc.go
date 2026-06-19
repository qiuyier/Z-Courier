// Package signing implements Z-Courier's replay-resistant internal HTTP HMAC
// signature protocol.
//
// Signatures cover the timestamp, nonce, HTTP method, escaped path, canonical
// query string, and SHA-256 request-body digest. The package is transport-only;
// it does not interpret JSON payloads.
package signing
