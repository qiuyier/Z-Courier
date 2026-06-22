<?php

declare(strict_types=1);

require __DIR__ . '/bootstrap.php';

use ZCourier\Client\Client as GatewayClient;
use ZCourier\Client\Config as ClientConfig;
use ZCourier\Client\Connector;
use ZCourier\Client\State;
use ZCourier\Exception\BindException;
use ZCourier\Exception\ClientException;
use ZCourier\Exception\ProtocolException;
use ZCourier\Protocol\Ack;
use ZCourier\Protocol\Codec;
use ZCourier\Protocol\FrameCodec;
use ZCourier\Protocol\FrameParser;
use ZCourier\Protocol\Packet;

final class PairConnector implements Connector
{
    /** @param resource $stream */
    public function __construct(private mixed $stream)
    {
    }

    public function connect(string $address, float $timeout): mixed
    {
        $stream = $this->stream;
        $this->stream = null;
        return $stream;
    }
}

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

echo "protocol fixtures passed\n";
echo "testing accepted bind...\n";
[$serverPid, $connector] = startBindServer('accepted');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
assertSame(State::Disconnected, $client->state(), 'new client state');
$binding = $client->connect();
assertSame(State::Ready, $client->state(), 'bound client state');
assertSame(true, $client->ready(), 'bound client readiness');
assertSame('canonical-client', $binding->clientId, 'canonical client ID');
assertSame('device-1', $binding->deviceId, 'bound device ID');
assertSame('php-session-1', $binding->sessionId, 'bound session ID');
assertSame($binding, $client->connect(), 'second connect reuses binding');
$client->close();
$client->close();
assertSame(State::Closed, $client->state(), 'closed client state');
assertClientError(ClientException::CLOSED, static fn () => $client->connect(), 'connect after close');
finishBindServer($serverPid, 'accepted bind server');

echo "testing rejected bind...\n";
[$serverPid, $connector] = startBindServer('unauthorized');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
assertClientError(ClientException::AUTHENTICATION_FAILED, static fn () => $client->connect(), 'unauthorized bind');
assertSame(State::Disconnected, $client->state(), 'rejected client state');
assertSame(true, $client->lastError() instanceof BindException, 'rejected bind error type');
finishBindServer($serverPid, 'unauthorized bind server');

echo "testing malformed bind ACK...\n";
[$serverPid, $connector] = startBindServer('missing_session');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
assertClientError(ClientException::UNEXPECTED_BIND_ACK, static fn () => $client->connect(), 'missing session bind');
finishBindServer($serverPid, 'missing session bind server');

echo "testing bind timeout...\n";
[$serverPid, $connector] = startBindServer('timeout');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
    bindTimeout: 0.05,
));
assertClientError(ClientException::BIND_TIMEOUT, static fn () => $client->connect(), 'bind timeout');
finishBindServer($serverPid, 'timeout bind server');

echo "PHP SDK tests passed: {$assertions} assertions\n";

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

function assertClientError(string $expectedKind, callable $callback, string $label): void
{
    global $assertions;
    try {
        $callback();
    } catch (ClientException $exception) {
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
    throw new RuntimeException("{$label}: expected client error {$expectedKind}");
}

/** @return array{int, Connector} */
function startBindServer(string $mode): array
{
    if (!function_exists('pcntl_fork')) {
        throw new RuntimeException('PHP client stream tests require the pcntl extension');
    }
    $pair = stream_socket_pair(STREAM_PF_UNIX, STREAM_SOCK_STREAM, STREAM_IPPROTO_IP);
    if ($pair === false) {
        throw new RuntimeException("cannot create {$mode} socket pair");
    }
    $pid = pcntl_fork();
    if ($pid === -1) {
        throw new RuntimeException("cannot fork {$mode} bind server");
    }
    if ($pid === 0) {
        fclose($pair[0]);
        try {
            serveBind($pair[1], $mode);
            fclose($pair[1]);
            exit(0);
        } catch (Throwable $error) {
            fwrite(STDERR, "{$mode} bind server: {$error->getMessage()}\n");
            exit(1);
        }
    }
    fclose($pair[1]);
    return [$pid, new PairConnector($pair[0])];
}

function finishBindServer(int $pid, string $label): void
{
    $waited = pcntl_waitpid($pid, $status);
    if ($waited !== $pid) {
        throw new RuntimeException("{$label}: cannot wait for child process");
    }
    assertSame(0, pcntl_wexitstatus($status), "{$label} exit code");
}

/** @param resource $connection */
function serveBind(mixed $connection, string $mode): void
{
    stream_set_timeout($connection, 5);
    $parser = new FrameParser();
    $bind = null;
    while ($bind === null) {
        $chunk = fread($connection, 8192);
        if ($chunk === false || $chunk === '') {
            throw new RuntimeException('connection closed before bind');
        }
        $packets = $parser->push($chunk);
        if ($packets !== []) {
            $bind = $packets[0];
        }
    }
    if (
        $bind->msgId !== Packet::MSG_ID_BIND
        || $bind->flags !== Packet::FLAG_ACK_REQUIRED
        || $bind->clientId !== 'claimed-client'
        || $bind->deviceId !== 'device-1'
        || $bind->token !== 'token-1'
        || $bind->messageId === ''
        || $bind->traceId !== $bind->messageId
    ) {
        throw new RuntimeException('invalid bind packet');
    }
    if ($mode === 'timeout') {
        usleep(500_000);
        return;
    }

    $code = $mode === 'unauthorized' ? Ack::UNAUTHORIZED : Ack::ACCEPTED;
    $sessionId = $mode === 'missing_session' ? '' : 'php-session-1';
    $reason = $mode === 'unauthorized' ? 'invalid test token' : '';
    $ack = new Ack($code, Packet::MSG_ID_BIND, $bind->messageId, $reason);
    $packet = new Packet(
        msgId: Packet::MSG_ID_ACK,
        body: $ack->toJson(),
        sequence: $bind->sequence,
        timestamp: (string) (int) floor(microtime(true) * 1000),
        clientId: 'canonical-client',
        deviceId: $bind->deviceId,
        sessionId: $sessionId,
        messageId: $bind->messageId,
        traceId: $bind->traceId,
    );
    $data = FrameCodec::encode($packet);
    while ($data !== '') {
        $written = fwrite($connection, $data);
        if ($written === false || $written === 0) {
            throw new RuntimeException('write bind ACK failed');
        }
        $data = substr($data, $written);
    }
    while (!feof($connection)) {
        $chunk = fread($connection, 1024);
        if ($chunk === false) {
            return;
        }
    }
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
