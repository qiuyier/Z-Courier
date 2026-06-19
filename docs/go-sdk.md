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
current gateway listener. Until `pkg/sdk/client` is available, callers that
connect directly to the gateway must use a compatible Zinx client transport
and pass the bytes returned by `protocol.Encode` as the Zinx message body.

Backend applications that only push downlink messages do not need a TCP client;
the upcoming `pkg/sdk/backend` package wraps the gateway's internal HTTP API.

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

## Planned Packages

```text
pkg/sdk/backend  internal HTTP push, batch, status, requeue, and discard client
pkg/sdk/client   AUTH/BIND, send, receive, reconnect, and downlink ACK helper
```
