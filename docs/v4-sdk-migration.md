# V4 Client SDK Integration And Migration

This guide describes how a client application should adopt the public Go or
PHP client SDK without depending on Zinx or reimplementing the Z-Courier wire
protocol. It also defines the application responsibilities that remain outside
the gateway and SDK.

## Choose The Correct Integration Surface

Use `pkg/sdk/client` or `sdk/php` for a process that keeps a persistent TCP
connection, sends upstream messages, and receives unsolicited downlink
messages. Use `pkg/sdk/backend` for a backend service that pushes downlink
messages to the gateway over internal HTTP.

Do not create one TCP client for every request. A client instance owns one
device connection, its accepted binding, ACK correlation, receive loop, and
reconnect state.

The PHP client is intended for a long-running CLI, Supervisor, systemd, Docker,
or Kubernetes worker. PHP-FPM request workers are suitable for backend HTTP
calls but not for a persistent connection that must receive unsolicited
downlink traffic.

## Field Ownership

After migration, the application supplies only business-level fields:

| Field | Owner | Notes |
| --- | --- | --- |
| `MsgID` | application | Must be a configured business route, not `1`, `2`, or `1000`. |
| `Body` | application | Opaque bytes; Z-Courier does not inspect the payload. |
| `MessageID` | application or SDK | Supply a stable value when an operation may be retried. |
| `TraceID` | application or SDK | Defaults to `MessageID`. |
| `ClientID`, `DeviceID` | application, then SDK | Claimed in configuration; packets use the accepted binding. |
| `SessionID` | gateway and SDK | Issued by AUTH/BIND and attached automatically. |
| `Token`, sequence, timestamp | SDK | Refreshed and generated for each connection or packet. |
| outer frame and delivery ACK | SDK | Never construct these in application code. |

The token verifier may canonicalize `ClientID`. Always use the `Binding`
returned by the SDK as the active identity rather than assuming the claimed
configuration was accepted unchanged.

## Go Migration

Replace direct Zinx connections, manual frame encoding, and custom ACK maps with
one reusable `client.Client`:

```go
gateway, err := client.New(client.Config{
    Address:       "gateway:8999",
    ClientID:      "client-1",
    DeviceID:      "worker-1",
    TokenProvider: loadCurrentToken,
    DownlinkHandler: func(ctx context.Context, packet *protocol.Packet) error {
        return storeIdempotently(ctx, packet.MessageID, packet.Body)
    },
    Reconnect: &client.ReconnectConfig{MaxAttempts: 0},
})
if err != nil {
    return err
}
defer gateway.Close()

if err := gateway.Connect(ctx); err != nil {
    return err
}
```

Use `Send` for business upstream packets. The SDK serializes socket writes and
matches requested ACKs by `MessageID`:

```go
result, err := gateway.Send(ctx, client.SendRequest{
    MsgID:       2001,
    Body:        payload,
    MessageID:   operationID,
    AckRequired: true,
})
```

The complete runnable example is in
[`examples/go-client`](../examples/go-client/main.go).

## PHP Integration

The PHP package currently installs from a local Composer path repository. Run
it as one long-lived worker per device identity:

```php
$client = new Client(new Config(
    address: 'gateway:8999',
    clientId: 'client-1',
    deviceId: 'php-worker-1',
    tokenProvider: new CallbackTokenProvider($loadCurrentToken),
    reconnect: new ReconnectConfig(maxAttempts: 0),
));

$client->connect();
$client->run(
    handler: static function (Packet $packet): void {
        storeIdempotently($packet->messageId, $packet->body);
    },
);
```

`run()` is blocking. Run independent business work in another process or use a
queue between the SDK worker and the rest of the application. An asynchronous
ReactPHP or Swoole adapter is not part of `v0.4.0`.

The complete runnable example is in
[`sdk/php/examples/client.php`](../sdk/php/examples/client.php).

## Delivery, ACK, And De-Duplication

Z-Courier provides at-least-once delivery. For an ACK-required downlink:

1. Validate the business message.
2. Start the application transaction.
3. Insert or update data using `MessageID` as an idempotency key.
4. Commit the transaction.
5. Return successfully and let automatic ACK run, or send a manual delivery ACK.

The SDK's bounded LRU suppresses duplicates only inside one client process. It
is lost on restart and is not an exactly-once guarantee. Important operations
must enforce a database unique constraint or equivalent durable de-duplication.

When a socket fails during `Send`, the SDK does not replay the message because
the gateway result may be unknown. The application may retry only when its
business operation is safe, and it must reuse the same `MessageID`.

## Reconnect And Token Refresh

Automatic reconnect is opt-in. Prefer a token provider over a static token when
credentials expire. Every reconnect obtains a token, opens a new socket, and
performs a fresh AUTH/BIND that produces a new `SessionID`.

Credential rejection and malformed bind responses stop reconnecting. Temporary
network and authentication-provider failures follow bounded exponential
backoff. Monitor the client process and expose its last error through the host
application's logging or health checks.

## Safe Rollout

1. Confirm the gateway route for every business `MsgID` before migrating.
2. Deploy the SDK client with a new test `DeviceID` and verify bind, upstream,
   downlink, delivery ACK, and reconnect.
3. Add a durable uniqueness rule for `MessageID` before enabling real downlink
   traffic.
4. Stop the old connection before starting the SDK with the same
   `ClientID + DeviceID`; the gateway intentionally replaces the older session.
5. Canary a small identity set and watch ACK, retry, online-session, and
   reconnect logs before completing the rollout.

The shared wire fixtures and E2E suites allow old and new client implementations
to coexist during migration, provided each physical connection uses a distinct
device identity.

## Verification

From the repository root:

```bash
export ZCOURIER_CLIENT_TOKEN=e2e-token

go run ./examples/go-client \
  -address 127.0.0.1:8999 \
  -client-id e2e-client \
  -device-id go-example

composer --working-dir=sdk/php install
php sdk/php/examples/client.php
```

Both commands read the client token from `ZCOURIER_CLIENT_TOKEN`. The PHP
example also accepts `ZCOURIER_GATEWAY_ADDRESS`, `ZCOURIER_CLIENT_ID`, and
`ZCOURIER_DEVICE_ID`.

For automated verification, run `bash scripts/e2e.sh`. It checks Go and PHP
against the same live gateway, including connection replacement and traffic
after reconnect.
