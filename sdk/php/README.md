# Z-Courier PHP SDK

The PHP SDK targets PHP 8.2 or newer. Its protocol layer is binary-safe and
uses the same V1 conformance fixtures as the Go SDK.

## Install From This Repository

Add a local path repository to the consuming application's `composer.json`:

```json
{
  "repositories": [
    {"type": "path", "url": "../Z-Courier/sdk/php"}
  ],
  "require": {
    "z-courier/sdk": "@dev"
  }
}
```

Then run `composer update z-courier/sdk` in the consuming application.

## Protocol Usage

```php
<?php

use ZCourier\Protocol\FrameCodec;
use ZCourier\Protocol\Packet;

$packet = new Packet(
    msgId: 2001,
    body: '{"action":"ping"}',
    flags: Packet::FLAG_ACK_REQUIRED,
    sequence: '1',
    timestamp: (string) (time() * 1000),
    clientId: 'client-1',
    deviceId: 'device-1',
    messageId: 'message-1',
    traceId: 'trace-1',
    token: 'client-token',
);

$frame = FrameCodec::encode($packet);
$decoded = FrameCodec::decode($frame);
```

`Packet::$body` is an opaque binary string. Sequence and timestamp values use
decimal strings so their full 64-bit wire range is preserved independently of
JSON number handling or platform integer width.

Use `FrameParser::push()` for arbitrary TCP chunks. It retains incomplete data
and returns every complete packet found in the stream.

## Blocking Client

The client uses PHP streams, performs AUTH/BIND during `connect()`, and exposes
the canonical identity accepted by the gateway:

```php
<?php

use ZCourier\Client\Client;
use ZCourier\Client\Config;
use ZCourier\Client\SendRequest;

$client = new Client(new Config(
    address: '127.0.0.1:8999',
    clientId: 'client-1',
    deviceId: 'device-1',
    token: getenv('ZCOURIER_CLIENT_TOKEN') ?: '',
));

try {
    $binding = $client->connect();
    printf("connected session=%s\n", $binding->sessionId);

    $result = $client->send(new SendRequest(
        msgId: 2001,
        body: '{"action":"ping"}',
        ackRequired: true,
    ));
    printf("accepted message=%s\n", $result->messageId);
} finally {
    $client->close();
}
```

Use `CallbackTokenProvider` when each connection attempt must fetch or refresh
credentials. `ClientException::$kind` provides stable error categories, while
`BindException` also exposes the gateway ACK code and reason.

`send()` fills the canonical client, device, session, token, sequence,
timestamp, MessageID, and TraceID fields. An empty MessageID is generated with
the `zc-msg-` prefix. Setting either `ackRequired` or
`Packet::FLAG_ACK_REQUIRED` waits for the matching ACK and returns it in
`SendResult`. Rejected ACKs throw `AckException`; timeout and malformed ACK
errors use stable `ClientException::$kind` values.

The blocking client serializes sends. Packets arriving while an ACK is pending
are retained in a bounded inbound queue rather than mistaken for the ACK.

### Downlink Messages

Use `run()` for automatic callback dispatch. A successful callback sends the
`MsgID = 2` delivered ACK. A callback exception is reported through `onError`
and leaves the message unacknowledged so the gateway can retry it:

```php
<?php

use ZCourier\Exception\DownlinkException;
use ZCourier\Protocol\Packet;

$client->run(
    handler: static function (Packet $packet): void {
        processMessage($packet->messageId, $packet->body);
    },
    onError: static function (DownlinkException $error): void {
        error_log($error->getMessage());
    },
);
```

For durable workflows, enable manual ACK and acknowledge only after the local
transaction commits:

```php
$client->run(
    handler: static function (Packet $packet) use ($client): void {
        persistMessage($packet->messageId, $packet->body);
        $client->acknowledgeDownlink($packet);
    },
    manualAck: true,
);
```

`receive($timeout)` is the lower-level alternative for applications that own
their receive loop. It returns one non-ACK packet; call
`acknowledgeDownlink()` for an ACK-required business packet after processing.
A `null` timeout blocks indefinitely, while a positive timeout throws
`ClientException` with kind `receive_timeout` without disconnecting the client.

The callback loop suppresses duplicate ACK-required downlinks with a bounded
process-local LRU keyed by `MessageID` and re-acknowledges the duplicate. Its
default capacity is `10000` and can be changed with
`Config::$downlinkDedupCapacity`. The cache is lost on process restart and does
not provide durable exactly-once processing. Important business handlers must
also enforce a database uniqueness constraint or equivalent persistent
idempotency check on `MessageID` before sending the delivered ACK.

Automatic reconnect is added in the following V4.3 stage.

## Verify

The test runner has no third-party dependency:

```bash
php sdk/php/tests/run.php
```

With Composer installed, the equivalent command is:

```bash
composer --working-dir=sdk/php test
```
