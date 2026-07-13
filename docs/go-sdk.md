# Go SDK

Z-Courier's public Go SDK lives under `pkg/sdk`. Public packages do not expose
Zinx interfaces and keep application message bodies as opaque `[]byte` values.

## Protocol Package

Import the stable protocol package:

```go
import "github.com/qiuyier/Z-Courier/pkg/sdk/protocol"
```

Create and encode a packet:

```go
packet := protocol.NewPacket(1001, []byte(`{"action":"ping"}`))
packet.ClientID = "client-1"
packet.DeviceID = "device-1"
packet.Token = "client-token"
packet.MessageID = "message-1"
packet.TraceID = "trace-1"
packet.Flags = protocol.FlagAckRequired

data, err := protocol.Encode(packet)
if err != nil {
    return err
}
```

Decode bytes received from a gateway:

```go
packet, err := protocol.Decode(data)
if err != nil {
    return err
}

if packet.MsgID == protocol.MsgIDAck {
    ack, err := protocol.DecodeAck(packet)
    if err != nil {
        return err
    }
    // Match ack.MessageID with the sent packet.
}
```

`NewPacket` clones the provided body. `Decode` accepts bodies up to 4 MiB by
default; use `DecodeWithMaxBodySize` when the application needs a different
limit. Decoder errors support `errors.Is` with the exported `Err*` values.

### Transport Boundary

The protocol package encodes the inner Z-Courier packet only. It does not open
TCP connections and does not encode the outer Zinx message frame used by the
current gateway listener. Use `pkg/sdk/client` for a high-level TCP client, or
implement the documented outer frame when integrating another language.

Backend applications that only push downlink messages do not need a TCP client;
use the `pkg/sdk/backend` package described below.

### Reserved Message IDs

| Constant | Value | Direction | Purpose |
| --- | ---: | --- | --- |
| `MsgIDAck` | 1 | gateway to client | Gateway processing result |
| `MsgIDDownlinkAck` | 2 | client to gateway | Client delivery confirmation |
| `MsgIDBind` | 1000 | client to gateway | AUTH/BIND connection control packet |

Application MsgIDs must not reuse reserved values. Use
`protocol.IsReservedMsgID(msgID)` when validating application route
configuration.

### Wire Format

All integers use big-endian byte order. The fixed header is 41 bytes:

| Field | Size |
| --- | ---: |
| Magic | 2 bytes |
| Version | 1 byte |
| Flags | 2 bytes |
| MsgID | 4 bytes |
| Seq | 8 bytes |
| Timestamp | 8 bytes |
| Six string lengths | 12 bytes |
| Body length | 4 bytes |

The variable section follows in this order:

```text
ClientID
DeviceID
SessionID
MessageID
TraceID
Token
Body
```

Each string is limited to 65,535 encoded bytes. The body length is represented
by a `uint32`, while applications should enforce a smaller operational limit.
The SDK includes a golden byte-level test so accidental wire-format changes are
detected even if encoder and decoder are modified together.

## Compatibility Policy

- `pkg/sdk/protocol` is the canonical wire implementation.
- The gateway's existing `internal/protocol` package is a compatibility facade
  over the public package, not a second codec.
- Additive fields require a protocol-version decision because the current
  binary layout is positional.
- Existing constants, field order, byte order, and error identities must remain
  covered by compatibility tests.

## Backend Package

Import the backend HTTP client:

```go
import "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
```

Create one reusable client per gateway endpoint:

```go
client, err := backend.NewClient(backend.Config{
    BaseURL:       "http://gateway:18182",
    InternalToken: os.Getenv("ZCOURIER_INTERNAL_TOKEN"),
    Timeout:       3 * time.Second,
})
if err != nil {
    return err
}
```

For replay-resistant HMAC mode, configure credentials instead of
`InternalToken`:

```go
client, err := backend.NewClient(backend.Config{
    BaseURL: "https://gateway.internal:18182",
    HMAC: &backend.HMACConfig{
        KeyID:  "backend-2026-01",
        Secret: []byte(os.Getenv("ZCOURIER_INTERNAL_HMAC_SECRET")),
    },
    Timeout: 3 * time.Second,
})
```

The client generates a cryptographically random nonce and signs the exact JSON
bytes sent on every request. Token and HMAC configuration are mutually
exclusive. See [internal-http-signing.md](internal-http-signing.md) for the
canonical cross-language contract and key-rotation procedure.

Push an opaque downlink message:

```go
response, err := client.Push(ctx, backend.PushRequest{
    ClientID:    "client-1",
    DeviceID:    "device-1",
    MsgID:       2001,
    MessageID:   "order-event-42",
    TraceID:     "trace-42",
    AckRequired: true,
    Body:        payload,
})
if err != nil {
    return err
}

switch response.DeliveryState {
case backend.DeliveryStateSent:
    // The message was written to an online client connection.
case backend.DeliveryStateQueued:
    // The reliable store accepted it for later delivery.
}
```

With reliable gateway storage, inspect `SubmissionState` to distinguish a new
submission from an idempotent replay:

```go
switch response.SubmissionState {
case backend.SubmissionStateCreated:
    // This request created the durable message.
case backend.SubmissionStateExisting:
    // A compatible MessageID already exists. MessageStatus is its persisted state.
}
```

The gateway returns HTTP `409` with `code = message_id_conflict` when the same
`MessageID` is reused with different client/device, MsgID, ACK requirement, or
body. `Client.Push` returns that response as `*backend.APIError` and does not
overwrite the existing message.

The client also provides:

```go
batch, err := client.PushBatch(ctx, backend.BatchPushRequest{Messages: messages})
message, err := client.GetMessage(ctx, "order-event-42")
failed, err := client.ListMessages(ctx, backend.ListMessagesRequest{
    Status: backend.MessageStatusFailed,
    Limit:  100,
})
message, err := client.Requeue(ctx, "order-event-42")
bulkRequeue, err := client.RequeueBatch(ctx, []string{"order-event-42", "order-event-43"})
message, err := client.Discard(ctx, "order-event-42", "operator decision")
```

`MessageStatusResponse.PolicyName` reports the persisted delivery policy used
by a V12.2.2-or-later message. The value remains stable when operators change
policy rules for newly accepted messages. Pre-V12.2.2 rows without a snapshot
use the current MsgID policy as a compatibility fallback.

For terminal messages, `MessageStatusResponse` also exposes
`TerminalReason`, `TerminalAt`, `TerminalPublishStatus`,
`TerminalPublishAttempts`, `TerminalNextPublishAt`,
`TerminalPublishError`, and `TerminalPublishedAt`. These fields describe the
gateway's asynchronous terminal-event outbox; they do not indicate another
client delivery attempt. The backend SDK does not consume NSQ events itself.

`PushBatch` treats HTTP `207 Multi-Status` as a decoded result, not as a method
error. Check `batch.Failed` and each item in `batch.Results` for partial or total
item failure.

`RequeueBatch` has the same HTTP `207` decoding rule. It accepts at most
`backend.MaxBulkRequeueMessages` unique non-empty IDs. The gateway only accepts
messages currently in `failed` state and processes each item independently
under its recorded delivery policy and the current queue-capacity limits.

Non-2xx responses return `*backend.APIError`. Use `errors.Is(err,
backend.ErrAPI)`, `errors.As`, and `APIError.Retryable` instead of parsing error
strings:

```go
var apiError *backend.APIError
if errors.As(err, &apiError) && apiError.Retryable() {
    // Apply bounded backoff. Reuse MessageID to preserve idempotency.
}
```

Transport and context failures return `*backend.RequestError` while preserving
their cause, so `errors.Is(err, context.DeadlineExceeded)` and
`errors.Is(err, context.Canceled)` continue to work. The client defaults to a
10-second request timeout and a 1 MiB response limit; both are configurable.
Redirects are rejected to prevent forwarding `X-ZCourier-Internal-Token` to a
different address or changing the signed request target.

Queue-capacity rejection is returned as a retryable `*backend.APIError` with
HTTP `429` and `Code == "queue_capacity_exceeded"`. Inspect
`CapacityScope`, `CapacityLimit`, and `CapacityPending` to distinguish a shared
global backlog from one saturated device. Reuse the original `MessageID` after
bounded backoff; the rejected request was not persisted.

The public backend request and response types are canonical. Gateway handlers
reuse them through internal aliases, which keeps SDK JSON fields synchronized
with the server contract.

## Client Package

The high-level TCP client under `pkg/sdk/client` owns the Zinx outer frame,
validates configuration, opens a native Go TCP connection, completes AUTH/BIND,
and keeps a persistent read loop without exposing Zinx interfaces:

```go
gateway, err := client.New(client.Config{
    Address:  "gateway:8999",
    ClientID: "client-1",
    DeviceID: "device-1",
    Token:    os.Getenv("ZCOURIER_CLIENT_TOKEN"),
})
if err != nil {
    return err
}
defer gateway.Close()

if err := gateway.Connect(ctx); err != nil {
    return err
}

binding := gateway.Binding()
// binding.ClientID is the canonical identity accepted from the token.
```

Use `TokenProvider` instead of `Token` when credentials must be refreshed for
each connection attempt. The two options are mutually exclusive.

Send an opaque business message and wait for its correlated gateway ACK:

```go
result, err := gateway.Send(ctx, client.SendRequest{
    MsgID:       2001,
    Body:        payload,
    AckRequired: true,
})
if err != nil {
    return err
}

// result.MessageID is generated when the request leaves it empty.
// result.Ack is non-nil because AckRequired was true.
```

`Send` is safe for concurrent callers. Socket writes are serialized while ACK
waits remain concurrent and are correlated by `MessageID`, so ACK arrival order
does not need to match send order. `WriteTimeout` and `AckTimeout` default to
five seconds and can be configured independently. Explicit `MessageID` values
must be unique among in-flight ACK waits. Setting either `AckRequired` or
`protocol.FlagAckRequired` requests and waits for an ACK.

Receive the next non-ACK gateway packet:

```go
packet, err := gateway.Receive(ctx)
if err != nil {
    return err
}
// packet.Body remains opaque application data.

if packet.Flags&protocol.FlagAckRequired != 0 {
    if _, err := gateway.AcknowledgeDownlink(ctx, packet); err != nil {
        return err
    }
}
```

The bounded `InboundBuffer` prevents an application that stops receiving from
growing memory without limit. Overflow closes the active connection and is
reported through `LastError`, supporting `errors.Is` with both
`client.ErrInboundOverflow` and `client.ErrConnectionClosed`.

For callback-based processing, configure `DownlinkHandler` before connecting:

```go
gateway, err := client.New(client.Config{
    Address:  "gateway:8999",
    ClientID: "client-1",
    DeviceID: "device-1",
    Token:    os.Getenv("ZCOURIER_CLIENT_TOKEN"),
    DownlinkHandler: func(ctx context.Context, packet *protocol.Packet) error {
        return processDownlink(ctx, packet.MessageID, packet.Body)
    },
    OnDownlinkError: func(err error) {
        log.Printf("downlink failed: %v", err)
    },
})
```

When the handler succeeds, ACK-required packets are confirmed with `MsgID = 2`.
When it returns an error or panics, no delivery ACK is sent and
`OnDownlinkError` is called. A configured handler owns packet consumption, so
`Receive` returns `ErrReceiveUnavailable` instead of racing the callback.

Set `ManualDownlinkAck` when business state must be committed before delivery
is confirmed. The handler can then call `AcknowledgeDownlink` itself. The
client's bounded LRU de-duplication table records successfully handled
ACK-required `MessageID` values; duplicate deliveries skip the handler and
re-send the delivery ACK. `DownlinkDedupCapacity` defaults to 10,000. This table
is process-local and is not a substitute for durable application de-duplication.

### Automatic Reconnect

Automatic reconnect is opt-in. Configure a policy before calling `Connect`:

```go
gateway, err := client.New(client.Config{
    Address:  "gateway:8999",
    ClientID: "client-1",
    DeviceID: "device-1",
    TokenProvider: refreshClientToken,
    Reconnect: &client.ReconnectConfig{
        InitialDelay: 500 * time.Millisecond,
        MaxDelay:     30 * time.Second,
        Multiplier:   2,
        Jitter:       0.2,
        MaxAttempts:  10,
    },
})
```

When an established socket is lost, the client enters `StateReconnectWait`,
applies bounded exponential backoff, obtains a fresh token, and performs a new
AUTH/BIND. `MaxAttempts = 0` retries without an attempt limit. A nil `Reconnect`
configuration keeps the previous disconnected behavior.

Use `WaitReady` when work must pause until the current Connect or reconnect
finishes:

```go
if err := gateway.WaitReady(ctx); err != nil {
    return err
}
```

Network failures, timeouts, token-provider failures, and temporary auth-service
failures are retryable. Credential rejection, bind rejection, malformed bind
ACKs, and `Close` stop the loop. Exhausting `MaxAttempts` returns a
`*client.ReconnectError` supporting `errors.Is(err,
client.ErrReconnectExhausted)` and preserving the final cause.

Reconnect restores transport and identity only. Pending sends from the lost
connection fail deterministically and are never replayed automatically. The
application must decide whether retrying a business message is safe and should
reuse its `MessageID` when idempotency is required. An initial `Connect` failure
is returned directly; automatic reconnect starts only after a previously ready
connection is lost.

### Live Gateway Verification

Run the single-node verifier from the repository root:

```bash
bash scripts/e2e.sh
```

In addition to the gateway's original queue, NSQ, and metrics checks, the script
runs `cmd/sdke2e` entirely through the public `pkg/sdk/client` and
`pkg/sdk/backend` APIs. It verifies:

- AUTH/BIND and canonical session identity
- upstream send with correlated accepted ACK
- backend downlink push and automatic `MsgID = 2` delivery ACK
- gateway-side replacement of an existing device connection
- automatic reconnect with a new `SessionID`
- successful upstream and downlink traffic after reconnect

`cmd/devclient` also uses `pkg/sdk/client` and remains the interactive tool for
manual connection, upstream, and downlink testing. The automated verifier is
part of the existing GitHub Actions E2E job through `scripts/e2e.sh`.

### Runnable Client Example

The repository includes a long-running client with dynamic token lookup,
automatic downlink ACK, reconnect, and one ACK-required upstream send:

```bash
export ZCOURIER_CLIENT_TOKEN=e2e-token
go run ./examples/go-client \
  -address 127.0.0.1:8999 \
  -client-id e2e-client \
  -device-id go-example
```

See [v4-sdk-migration.md](v4-sdk-migration.md) for field ownership, durable
de-duplication, same-identity connection replacement, and rollout guidance.
