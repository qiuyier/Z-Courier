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

## Verify

The test runner has no third-party dependency:

```bash
php sdk/php/tests/run.php
```

With Composer installed, the equivalent command is:

```bash
composer --working-dir=sdk/php test
```
