# Z-Courier Protocol

Z-Courier wraps every client packet in a small binary envelope. The gateway uses
the envelope for routing, authentication, connection binding, tracing, and ACKs.
The body is opaque bytes owned by the business system.

## Packet Layout

All integer fields use big-endian byte order.

```text
Version        uint8
Flags          uint8
MsgID          uint32
Seq            uint64
Timestamp      int64
ClientIDLen    uint16
DeviceIDLen    uint16
SessionIDLen   uint16
MessageIDLen   uint16
TraceIDLen     uint16
TokenLen       uint16
BodyLength     uint32
ClientID       bytes
DeviceID       bytes
SessionID      bytes
MessageID      bytes
TraceID        bytes
Token          bytes
Body           bytes
```

The fixed header is 36 bytes before variable-length strings and body bytes.

## Important Fields

- `Version`: protocol version. V1 currently uses `1`.
- `Flags`: bit flags. V1 defines `ack_required`.
- `MsgID`: command or business route identifier.
- `Seq`: per-connection sequence number supplied by the client.
- `Timestamp`: Unix milliseconds from the sender.
- `ClientID`: claimed client identity. It is not trusted before token
  verification.
- `DeviceID`: device identity used for session binding and targeted push.
- `SessionID`: gateway-issued session ID. Clients may leave it empty before
  bind.
- `MessageID`: globally unique message identity for ACK and idempotency.
- `TraceID`: request correlation ID.
- `Token`: client auth token.
- `Body`: opaque business payload.

## Reserved MsgIDs

```text
1  gateway ACK
2  client downlink delivery ACK
1000  AUTH/BIND
```

Application MsgIDs should avoid the reserved range. The sample configs use:

```text
1001-1999  HTTP upstream examples
2000-2999  NSQ upstream examples
```

These ranges are only examples. Real deployments should assign ranges based on
their own business modules.

## AUTH/BIND

The gateway binds a connection only after it receives an explicit AUTH/BIND
packet:

```text
MsgID = 1000
```

The packet must include:

```text
Token
DeviceID
```

The packet may include `ClientID`, but the gateway uses the verified token
principal as the authoritative client identity.

After bind, the gateway records:

```text
client_id + device_id -> connection
```

If durable downlink storage is enabled, binding also triggers a flush of pending
messages for that `client_id` and `device_id`.

AUTH/BIND is a gateway control message. It is ACKed by the gateway and is not
forwarded to upstream backends. Business upstream packets and downlink delivery
ACKs must be sent after AUTH/BIND succeeds.

## Gateway ACK

When a packet asks for an ACK, or when the gateway rejects a packet, the gateway
returns a protocol packet with `MsgID = 1`.

The ACK body is JSON:

```json
{
  "message_id": "message-1",
  "msg_id": 1000,
  "code": "accepted",
  "reason": "",
  "trace_id": "trace-1"
}
```

Common codes:

- `accepted`: gateway accepted the packet.
- `rejected`: gateway rejected the packet.
- `unauthorized`: token verification failed.
- `rate_limited`: the rate limiter rejected the packet.
- `decode_failed`: the binary envelope could not be decoded.

## Downlink Push

Backends push to clients through the internal HTTP API:

```text
POST /internal/push
```

The HTTP JSON request contains the same delivery metadata:

```json
{
  "client_id": "dev-client",
  "device_id": "device-1",
  "msg_id": 2001,
  "message_id": "message-1",
  "trace_id": "trace-1",
  "ack_required": true,
  "body": "aGVsbG8="
}
```

`body` is base64-encoded because JSON encodes byte slices this way and
Z-Courier treats the body as opaque bytes.

If the client is online, the gateway sends a protocol packet with the requested
`MsgID`. If the client is offline and storage is configured, the message is
queued for retry or bind-time flush.

Backends can push multiple downlink messages in one internal request:

```text
POST /internal/push/batch
```

```json
{
  "messages": [
    {
      "client_id": "client-a",
      "device_id": "phone",
      "msg_id": 2001,
      "message_id": "message-1",
      "trace_id": "trace-1",
      "ack_required": true,
      "body": "aGVsbG8="
    },
    {
      "client_id": "client-b",
      "device_id": "phone",
      "msg_id": 2001,
      "message_id": "message-2",
      "trace_id": "trace-2",
      "ack_required": true,
      "body": "d29ybGQ="
    }
  ]
}
```

The batch response contains one result per message. A partially failed batch
returns HTTP `207` with `code = partial_failure`; request-level errors such as
invalid JSON still return `400`.

Backends can query a stored downlink message by `message_id`:

```text
GET /internal/message/status?message_id=message-1
```

The response includes the current reliable delivery status and retry metadata:

```json
{
  "code": "ok",
  "message_id": "message-1",
  "client_id": "dev-client",
  "device_id": "device-1",
  "msg_id": 2001,
  "trace_id": "trace-1",
  "session_id": "zs_...",
  "status": "delivered",
  "attempts": 1,
  "body_size_bytes": 5,
  "created_at": "2026-06-13T12:00:00Z",
  "updated_at": "2026-06-13T12:00:01Z",
  "sent_at": "2026-06-13T12:00:00Z",
  "delivered_at": "2026-06-13T12:00:01Z"
}
```

Backends and operators can list stored messages by delivery status:

```text
GET /internal/messages?status=failed&limit=100
```

Supported statuses are `pending`, `sent`, `delivered`, `failed`, and
`discarded`. When `status` is omitted, the gateway lists `failed` messages.

Failed or stuck messages can be manually requeued:

```text
POST /internal/message/requeue
```

```json
{
  "message_id": "message-1"
}
```

Requeue resets `attempts` to `0`, clears retry metadata, and changes the
message back to `pending`. Delivered and discarded messages cannot be requeued.

Messages that should no longer be processed can be discarded:

```text
POST /internal/message/discard
```

```json
{
  "message_id": "message-1",
  "reason": "handled manually"
}
```

Discard changes the status to `discarded`, clears retry metadata, and keeps the
reason in `last_error`.

Terminal messages are eventually removed by the downlink retention worker.
`delivered`, `failed`, and `discarded` each have independent TTL settings in
`downlink.retention`. The cleanup worker does not delete `pending` or `sent`
messages, because those are still part of the delivery/retry lifecycle.

## Downlink Delivery ACK

Clients confirm downlink delivery by sending a protocol packet with `MsgID = 2`.

The ACK body is JSON:

```json
{
  "message_id": "message-1",
  "code": "delivered"
}
```

The gateway validates this packet through the normal pipeline. When accepted, it
marks the stored downlink message as `delivered` and records ACK metrics.

## Reliability Contract

V1 aims for at-least-once downlink delivery:

- The gateway persists downlink requests before attempting online delivery.
- Offline clients receive pending messages when they bind.
- Failed sends and ACK timeouts are retried until `max_attempts`.
- Clients must de-duplicate by `MessageID` if duplicate delivery is possible.

Z-Courier does not claim exactly-once delivery.
