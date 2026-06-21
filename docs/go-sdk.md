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

The client also provides:

```go
batch, err := client.PushBatch(ctx, backend.BatchPushRequest{Messages: messages})
message, err := client.GetMessage(ctx, "order-event-42")
failed, err := client.ListMessages(ctx, backend.ListMessagesRequest{
    Status: backend.MessageStatusFailed,
    Limit:  100,
})
message, err := client.Requeue(ctx, "order-event-42")
message, err := client.Discard(ctx, "order-event-42", "operator decision")
```

`PushBatch` treats HTTP `207 Multi-Status` as a decoded result, not as a method
error. Check `batch.Failed` and each item in `batch.Results` for partial or total
item failure.

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

The public backend request and response types are canonical. Gateway handlers
reuse them through internal aliases, which keeps SDK JSON fields synchronized
with the server contract.

## Client Package (V4 In Progress)

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

Automatic reconnect is still pending. It will be added before the client
package is declared complete for `v0.4.0`.
