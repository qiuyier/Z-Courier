<?php

declare(strict_types=1);

require __DIR__ . '/bootstrap.php';

use ZCourier\Exception\ProtocolException;
use ZCourier\Protocol\Ack;
use ZCourier\Protocol\Codec;
use ZCourier\Protocol\FrameCodec;
use ZCourier\Protocol\FrameParser;
use ZCourier\Protocol\Packet;

$root = dirname(__DIR__, 3);
$valid = loadJson($root . '/testdata/protocol/v1/valid.json');
$invalid = loadJson($root . '/testdata/protocol/v1/invalid.json');
$assertions = 0;

/** @var array<string, array<string, mixed>> $sources */
$sources = [];
foreach ($valid['vectors'] as $vector) {
    $name = requireString($vector, 'name');
    $sources[$name] = $vector;
    $packet = packetFromFixture(requireArray($vector, 'packet'));
    $inner = Codec::encode($packet);
    assertSame(requireString($vector, 'inner_hex'), bin2hex($inner), "{$name} inner bytes");
    $frame = FrameCodec::encode($packet);
    assertSame(requireString($vector, 'frame_hex'), bin2hex($frame), "{$name} frame bytes");
    assertPacket($packet, FrameCodec::decode($frame), "{$name} decoded packet");
}

foreach ($valid['generated_vectors'] as $vector) {
    $name = requireString($vector, 'name');
    $packetData = applyExpansion(
        requireArray($vector, 'packet'),
        requireArray($vector, 'expansion'),
    );
    $packet = packetFromFixture($packetData);
    $inner = Codec::encode($packet);
    $frame = FrameCodec::encode($packet);
    assertSame(requireInt($vector, 'inner_length'), strlen($inner), "{$name} inner length");
    assertSame(requireInt($vector, 'frame_length'), strlen($frame), "{$name} frame length");
    assertSame(requireString($vector, 'inner_sha256'), hash('sha256', $inner), "{$name} inner SHA-256");
    assertSame(requireString($vector, 'frame_sha256'), hash('sha256', $frame), "{$name} frame SHA-256");
}

foreach ($invalid['vectors'] as $vector) {
    $name = requireString($vector, 'name');
    $sourceName = requireString($vector, 'source');
    if (!isset($sources[$sourceName])) {
        throw new RuntimeException("{$name}: unknown source fixture {$sourceName}");
    }
    $scope = requireString($vector, 'scope');
    $hex = $scope === 'frame'
        ? requireString($sources[$sourceName], 'frame_hex')
        : requireString($sources[$sourceName], 'inner_hex');
    $mutation = isset($vector['mutation']) && is_array($vector['mutation'])
        ? $vector['mutation']
        : [];
    $data = mutate(decodeHex($hex), $mutation);
    $maxBodySize = isset($vector['max_body_size'])
        ? requireInt($vector, 'max_body_size')
        : Packet::DEFAULT_MAX_BODY_SIZE;

    assertProtocolError(requireString($vector, 'expected_error'), static function () use ($scope, $data, $maxBodySize): void {
        if ($scope === 'inner') {
            Codec::decode($data, $maxBodySize);
            return;
        }
        if ($scope === 'frame') {
            FrameCodec::decode($data, FrameCodec::DEFAULT_MAX_PAYLOAD_SIZE, $maxBodySize);
            return;
        }
        throw new RuntimeException("unknown fixture scope {$scope}");
    }, $name);
}

foreach ($invalid['generated_vectors'] as $vector) {
    $name = requireString($vector, 'name');
    $packetData = applyExpansion(
        requireArray($vector, 'packet'),
        requireArray($vector, 'expansion'),
    );
    assertProtocolError(
        requireString($vector, 'expected_error'),
        static fn () => Codec::encode(packetFromFixture($packetData)),
        $name,
    );
}

$parser = new FrameParser();
$combinedFrames = decodeHex(requireString($valid['vectors'][0], 'frame_hex'))
    . decodeHex(requireString($valid['vectors'][1], 'frame_hex'));
$parsed = [];
for ($offset = 0; $offset < strlen($combinedFrames); $offset += 3) {
    array_push($parsed, ...$parser->push(substr($combinedFrames, $offset, 3)));
}
assertSame(2, count($parsed), 'fragmented and coalesced stream packet count');
assertSame(0, $parser->bufferedBytes(), 'stream parser remaining bytes');
assertSame(1001, $parsed[0]->msgId, 'stream parser first MsgID');
assertSame(1000, $parsed[1]->msgId, 'stream parser second MsgID');

$gatewayAckPacket = FrameCodec::decode(decodeHex(requireString($sources['gateway_ack'], 'frame_hex')));
$ack = Ack::fromPacket($gatewayAckPacket);
assertSame(Ack::ACCEPTED, $ack->code, 'ACK code');
assertSame(Packet::MSG_ID_BIND, $ack->msgId, 'ACK origin MsgID');
assertSame('bind-1', $ack->messageId, 'ACK message ID');

$integerBoundaryPacket = new Packet(
    msgId: 2001,
    sequence: '18446744073709551615',
    timestamp: '-9223372036854775808',
);
$integerBoundaryDecoded = Codec::decode(Codec::encode($integerBoundaryPacket));
assertSame('18446744073709551615', $integerBoundaryDecoded->sequence, 'uint64 maximum');
assertSame('-9223372036854775808', $integerBoundaryDecoded->timestamp, 'int64 minimum');

echo "PHP protocol conformance passed: {$assertions} assertions\n";

/** @return array<string, mixed> */
function loadJson(string $path): array
{
    $contents = file_get_contents($path);
    if ($contents === false) {
        throw new RuntimeException("cannot read {$path}");
    }
    try {
        $decoded = json_decode($contents, true, 512, JSON_THROW_ON_ERROR);
    } catch (JsonException $exception) {
        throw new RuntimeException("cannot decode {$path}: {$exception->getMessage()}", 0, $exception);
    }
    if (!is_array($decoded)) {
        throw new RuntimeException("fixture {$path} is not an object");
    }
    return $decoded;
}

/** @param array<string, mixed> $data */
function packetFromFixture(array $data): Packet
{
    return new Packet(
        msgId: requireInt($data, 'msg_id'),
        body: decodeHex(requireString($data, 'body_hex')),
        version: requireInt($data, 'version'),
        flags: requireInt($data, 'flags'),
        sequence: requireString($data, 'seq'),
        timestamp: requireString($data, 'timestamp'),
        clientId: requireString($data, 'client_id'),
        deviceId: requireString($data, 'device_id'),
        sessionId: requireString($data, 'session_id'),
        messageId: requireString($data, 'message_id'),
        traceId: requireString($data, 'trace_id'),
        token: requireString($data, 'token'),
    );
}

/**
 * @param array<string, mixed> $packet
 * @param array<string, mixed> $expansion
 * @return array<string, mixed>
 */
function applyExpansion(array $packet, array $expansion): array
{
    $field = requireString($expansion, 'field');
    $unit = decodeHex(requireString($expansion, 'byte_hex'));
    $packet[$field] = str_repeat($unit, requireInt($expansion, 'count'));
    return $packet;
}

/** @param array<string, mixed> $mutation */
function mutate(string $source, array $mutation): string
{
    $type = $mutation === [] ? '' : requireString($mutation, 'type');
    return match ($type) {
        '' => $source,
        'truncate_to' => substr($source, 0, requireInt($mutation, 'count')),
        'truncate_tail' => substr($source, 0, strlen($source) - requireInt($mutation, 'count')),
        'append_hex' => $source . decodeHex(requireString($mutation, 'hex')),
        'replace_hex' => replaceBytes(
            $source,
            requireInt($mutation, 'offset'),
            decodeHex(requireString($mutation, 'hex')),
        ),
        default => throw new RuntimeException("unknown mutation {$type}"),
    };
}

function replaceBytes(string $source, int $offset, string $replacement): string
{
    return substr($source, 0, $offset)
        . $replacement
        . substr($source, $offset + strlen($replacement));
}

function decodeHex(string $value): string
{
    $decoded = hex2bin($value);
    if ($decoded === false) {
        throw new RuntimeException("invalid hex: {$value}");
    }
    return $decoded;
}

function assertPacket(Packet $expected, Packet $actual, string $label): void
{
    assertSame($expected->version, $actual->version, "{$label} version");
    assertSame($expected->flags, $actual->flags, "{$label} flags");
    assertSame($expected->msgId, $actual->msgId, "{$label} MsgID");
    assertSame($expected->sequence, $actual->sequence, "{$label} sequence");
    assertSame($expected->timestamp, $actual->timestamp, "{$label} timestamp");
    assertSame($expected->clientId, $actual->clientId, "{$label} client ID");
    assertSame($expected->deviceId, $actual->deviceId, "{$label} device ID");
    assertSame($expected->sessionId, $actual->sessionId, "{$label} session ID");
    assertSame($expected->messageId, $actual->messageId, "{$label} message ID");
    assertSame($expected->traceId, $actual->traceId, "{$label} trace ID");
    assertSame($expected->token, $actual->token, "{$label} token");
    assertSame($expected->body, $actual->body, "{$label} body");
}

function assertProtocolError(string $expectedKind, callable $callback, string $label): void
{
    global $assertions;
    try {
        $callback();
    } catch (ProtocolException $exception) {
        $assertions++;
        if ($exception->kind !== $expectedKind) {
            throw new RuntimeException(
                "{$label}: error kind is {$exception->kind}; expected {$expectedKind}",
                0,
                $exception,
            );
        }
        return;
    } catch (Throwable $throwable) {
        throw new RuntimeException("{$label}: unexpected exception " . $throwable::class, 0, $throwable);
    }
    throw new RuntimeException("{$label}: expected protocol error {$expectedKind}");
}

function assertSame(mixed $expected, mixed $actual, string $label): void
{
    global $assertions;
    $assertions++;
    if ($actual !== $expected) {
        throw new RuntimeException(
            "{$label}: actual " . var_export($actual, true) . '; expected ' . var_export($expected, true),
        );
    }
}

/** @param array<string, mixed> $data */
function requireArray(array $data, string $key): array
{
    $value = $data[$key] ?? null;
    if (!is_array($value)) {
        throw new RuntimeException("fixture field {$key} must be an object");
    }
    return $value;
}

/** @param array<string, mixed> $data */
function requireString(array $data, string $key): string
{
    $value = $data[$key] ?? null;
    if (!is_string($value)) {
        throw new RuntimeException("fixture field {$key} must be a string");
    }
    return $value;
}

/** @param array<string, mixed> $data */
function requireInt(array $data, string $key): int
{
    $value = $data[$key] ?? null;
    if (!is_int($value)) {
        throw new RuntimeException("fixture field {$key} must be an integer");
    }
    return $value;
}
