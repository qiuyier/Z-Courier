# V4 Client SDK Design

V4 turns the stable Z-Courier wire protocol into client libraries that can be
used without importing Zinx or manually managing TCP frames. The public SemVer
target for this phase is `v0.4.0`.

The first cross-language SDK is PHP rather than Java. PHP is a first-class V4
implementation and must pass the same protocol fixtures and gateway E2E
scenarios as the Go client.

## Goals

- Publish a transport-independent, cross-language protocol specification.
- Publish machine-readable golden vectors for valid and invalid V1 packets.
- Add a high-level Go client under `pkg/sdk/client`.
- Add a Composer-compatible PHP SDK under `sdk/php`.
- Hide Zinx framing, TCP stream parsing, AUTH/BIND, ACK correlation, reconnect,
  downlink dispatch, and delivery ACK details behind stable client APIs.
- Verify Go and PHP implementations against the same fixtures and live gateway
  behavior in CI.

## Non-Goals

V4 does not add:

- A web administration console or route hot reload.
- New gateway delivery guarantees or exactly-once delivery.
- A new packet version or changes to the existing V1 field layout.
- Java, Node.js, or Python SDKs. They can reuse the V4 contract later.
- TLS termination inside Z-Courier. SDKs may connect through deployment-provided
  TLS endpoints, proxies, or service meshes in a later milestone.

## Wire Contract

A TCP message has two layers. Cross-language clients must implement both.

### Outer Transport Frame

The current TCP listener uses the default Zinx big-endian frame:

| Field | Size | Encoding |
| --- | ---: | --- |
| MsgID | 4 bytes | unsigned big-endian integer |
| Payload length | 4 bytes | unsigned big-endian integer |
| Payload | variable | one encoded Z-Courier packet |

The payload length does not include the 8-byte outer header. The outer MsgID
must equal the inner packet MsgID. SDK decoders reject a mismatch instead of
routing ambiguous input.

TCP reads are streams rather than messages. Implementations must retain partial
headers and payloads, decode several frames from one read, and apply a maximum
frame size before allocating the payload buffer.

### Inner Z-Courier Packet

The inner V1 packet remains the canonical format implemented by
`pkg/sdk/protocol`:

| Field | Size | Encoding |
| --- | ---: | --- |
| Magic | 2 bytes | `0x5A43` |
| Version | 1 byte | `1` |
| Flags | 2 bytes | unsigned big-endian integer |
| MsgID | 4 bytes | unsigned big-endian integer |
| Seq | 8 bytes | unsigned big-endian integer |
| Timestamp | 8 bytes | signed Unix milliseconds encoded as 64 bits |
| Six string lengths | 12 bytes | six unsigned big-endian 16-bit integers |
| Body length | 4 bytes | unsigned big-endian integer |

The variable fields follow in this exact order:

```text
ClientID
DeviceID
SessionID
MessageID
TraceID
Token
Body
```

Strings are UTF-8 byte sequences. Their lengths count bytes, not characters.
The body is opaque bytes and is never interpreted by the protocol codec.

Reserved message IDs remain:

| MsgID | Direction | Purpose |
| ---: | --- | --- |
| `1` | gateway to client | Gateway processing ACK |
| `2` | client to gateway | Downlink delivery ACK |
| `1000` | client to gateway | AUTH/BIND |

## Shared Protocol Fixtures

Language-neutral fixtures will live under `testdata/protocol/v1`. Byte-sized
valid fixtures contain packet fields, inner packet hex, and complete outer
frame hex. Large boundary fixtures use deterministic expansion recipes plus
length and SHA-256 checks to avoid bloating the repository. Initial cases cover:

- The existing Go golden packet.
- Empty strings and an empty body.
- UTF-8 identifiers whose byte length differs from character count.
- Binary bodies containing zero bytes.
- Every defined flag and reserved MsgID.
- Boundary-sized metadata and configured body-limit behavior.

Invalid fixtures cover bad magic, unsupported version, truncation, inconsistent
lengths, oversized bodies, and outer/inner MsgID mismatch. Go and PHP tests read
the same files; neither implementation may maintain a private copy of expected
wire bytes.

## Client Lifecycle

Both SDKs expose the same observable lifecycle:

```text
disconnected -> connecting -> binding -> ready -> closing -> closed
                         \-> reconnect_wait -> connecting
```

Rules:

1. A TCP connection is not ready until the gateway accepts `MsgID = 1000`.
2. Business messages cannot be sent before the bind ACK is accepted.
3. Every connection and reconnect creates a new bind `MessageID`.
4. Reconnect uses bounded exponential backoff with jitter and is canceled by
   client shutdown.
5. A credential rejection is surfaced to the application and does not retry
   forever. Transport failures and temporary auth-provider failures may retry
   according to policy.
6. Shutdown stops new sends, cancels reconnect, and closes the socket without
   leaking reader or callback workers.

## Send And ACK Semantics

- The SDK creates `MessageID`, `TraceID`, sequence, and timestamp defaults while
  allowing explicit values when applications need idempotency or tracing.
- ACK correlation uses the origin `MessageID`, not receive order.
- Applications can request an ACK and wait with a deadline.
- A pending wait fails deterministically when the connection closes.
- Late or unknown ACKs are reported through diagnostics and do not satisfy a
  different send.

The gateway remains at-least-once. Reusing a `MessageID` is an application-level
idempotency decision; the client SDK does not promise exactly-once upstream
processing.

## Downlink Handling

The SDK dispatches non-reserved gateway packets to an application handler. A
handler result controls delivery acknowledgement:

- Success sends `MsgID = 2` with `{"message_id":"...","code":"delivered"}`.
- Failure or cancellation does not send a delivered ACK, allowing gateway retry
  policy to remain effective.
- Manual ACK mode is available for applications that must commit local state
  before acknowledging delivery.

The SDK offers a de-duplication hook keyed by `MessageID`. The default in-memory
implementation is bounded but does not survive process restart. Durable
de-duplication remains an application responsibility and must happen before a
delivery ACK is sent.

## Go Client

The Go package will be:

```text
pkg/sdk/client
```

Its public surface will include:

- Configuration for endpoint, identity, token provider, timeouts, frame limits,
  reconnect policy, ACK policy, and handlers.
- A concurrency-safe client with `Connect`, `Send`, readiness observation, and
  `Close` operations.
- Typed protocol, bind, timeout, closed-connection, and handler errors.
- Context cancellation on connect, send, ACK wait, and shutdown paths.

The package will use the Go standard library for TCP transport and
`pkg/sdk/protocol` for inner packets. It will not expose Zinx interfaces.

## PHP SDK

The Composer package will live under:

```text
sdk/php
```

It targets PHP 8.2 or newer and uses strict types, PSR-4 autoloading, and
`stream_socket_client` so the core protocol and TCP client do not require a
framework. Its initial namespaces are:

```text
ZCourier\Protocol
ZCourier\Client
```

The PHP SDK provides:

- Immutable packet and ACK value objects.
- Binary-safe packet and outer-frame codecs.
- A blocking client suitable for CLI commands, queue workers, and long-running
  processes.
- Bounded reconnect, bind, send-and-wait, receive-loop, callback, and downlink
  ACK behavior matching the Go SDK.
- Domain exceptions rather than string matching.

PHP-FPM and other short request lifecycles are appropriate for backend HTTP
push calls but are not appropriate for receiving unsolicited downlink messages
over a persistent TCP connection. This limitation will be explicit in examples.

An asynchronous adapter for a specific PHP event-loop framework is not part of
the first release. The protocol and state-machine layers must remain separable
so ReactPHP or Swoole adapters can be added later without replacing the codec.

## Milestones

### V4.1 Cross-Language Contract

- Publish the complete outer and inner protocol specification.
- Add shared valid and invalid fixtures.
- Refactor Go compatibility tests to consume the shared fixtures.

### V4.2 High-Level Go Client

Status: complete.

- Implement framing, lifecycle, bind, send, ACK correlation, and downlink ACK.
- Migrate `cmd/devclient` to the public client package.
- Add reconnect and race tests.
- Exercise bind, upstream, downlink ACK, connection replacement, reconnect, and
  continued traffic against a live gateway in the single-node E2E workflow.

### V4.3 PHP Protocol And Client SDK

Status: complete. The protocol foundation, shared fixture conformance, blocking
TCP AUTH/BIND lifecycle, business sends, ACK correlation, raw downlink receive,
automatic and manual delivery ACK, callback error isolation, bounded
process-local de-duplication, and opt-in synchronous reconnect are complete.
Reconnect refreshes credentials and binding state, supports bounded exponential
backoff with jitter, stops on deterministic bind failures, and never implicitly
replays a failed business send.

- Add Composer metadata, protocol codec, frame parser, and typed exceptions.
- Implement the blocking high-level client and callback model.
- Pass all shared fixtures without language-specific expected bytes.

### V4.4 Cross-Language E2E And Release

Status: in progress. Go and PHP now share the single-node live-gateway E2E
workflow. The PHP verifier covers bind, upstream ACK, reliable downlink and
delivery ACK, connection replacement, reconnect with refreshed credentials and
a new SessionID, and continued bidirectional traffic. PHP 8.2 unit, syntax, and
live E2E checks are wired into GitHub Actions. Maximum-level PHPStan analysis
without an ignore baseline is also enforced. Runnable Go/PHP integration
examples and migration guidance are published. Release verification remains
pending.

- Exercise Go and PHP bind, upstream, downlink, ACK, disconnect, and reconnect
  against the same gateway.
- Add PHP static analysis, unit tests, and SDK E2E to GitHub Actions.
- Publish integration examples and migration guidance.
- Run release verification before creating `v0.4.0`; no release tag is created
  merely because planning or one milestone is complete.

## Completion Criteria

V4 is complete only when:

- The shared fixtures are the source of truth for both languages.
- Neither SDK imports or requires Zinx on the client side.
- Go and PHP clients bind, send upstream messages, receive downlink messages,
  acknowledge delivery, and reconnect successfully in automated E2E tests.
- Failure cases include malformed frames, rejected bind, ACK timeout, socket
  loss, callback failure, and clean shutdown.
- Public APIs and supported PHP runtimes are documented.
- Existing gateway unit, race, single-node E2E, cluster E2E, and load-test smoke
  checks remain green.
