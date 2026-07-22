<?php

declare(strict_types=1);

require __DIR__ . '/bootstrap.php';

use ZCourier\Client\Client as GatewayClient;
use ZCourier\Client\CallbackTokenProvider;
use ZCourier\Client\Config as ClientConfig;
use ZCourier\Client\Connector;
use ZCourier\Client\MessageDeduper;
use ZCourier\Client\ReconnectConfig;
use ZCourier\Client\SendRequest;
use ZCourier\Client\State;
use ZCourier\Exception\AckException;
use ZCourier\Exception\BindException;
use ZCourier\Exception\ClientException;
use ZCourier\Exception\DownlinkException;
use ZCourier\Exception\ProtocolException;
use ZCourier\Exception\ReconnectException;
use ZCourier\Protocol\Ack;
use ZCourier\Protocol\Codec;
use ZCourier\Protocol\DeliveryAck;
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
        return prepareClientStream($stream);
    }
}

final class SequenceConnector implements Connector
{
    public int $calls = 0;

    /** @param list<mixed> $connections */
    public function __construct(private array $connections)
    {
    }

    public function connect(string $address, float $timeout): mixed
    {
        $index = $this->calls++;
        if (!array_key_exists($index, $this->connections)) {
            throw new RuntimeException('no configured connection remains');
        }
        $connection = $this->connections[$index];
        if ($connection instanceof Throwable) {
            throw $connection;
        }
        return prepareClientStream($connection);
    }
}

function prepareClientStream(mixed $stream): mixed
{
    if (!is_resource($stream)) {
        return $stream;
    }
    if (!stream_set_blocking($stream, true)) {
        throw new RuntimeException('cannot enable blocking test stream mode');
    }
    return $stream;
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

$deliveryAck = new DeliveryAck('downlink-1');
$deliveryAckPacket = new Packet(Packet::MSG_ID_DOWNLINK_ACK, $deliveryAck->toJson());
$decodedDeliveryAck = DeliveryAck::fromPacket($deliveryAckPacket);
assertSame(DeliveryAck::DELIVERED, $decodedDeliveryAck->code, 'delivery ACK code');
assertSame('downlink-1', $decodedDeliveryAck->messageId, 'delivery ACK message ID');

$integerBoundaryPacket = new Packet(
    msgId: 2001,
    sequence: '18446744073709551615',
    timestamp: '-9223372036854775808',
);
$integerBoundaryDecoded = Codec::decode(Codec::encode($integerBoundaryPacket));
assertSame('18446744073709551615', $integerBoundaryDecoded->sequence, 'uint64 maximum');
assertSame('-9223372036854775808', $integerBoundaryDecoded->timestamp, 'int64 minimum');

$deduper = new MessageDeduper(2);
$deduper->mark('oldest');
$deduper->mark('recent');
assertSame(true, $deduper->contains('oldest'), 'deduper refreshes a hit');
$deduper->mark('newest');
assertSame(false, $deduper->contains('recent'), 'deduper evicts least recently used entry');
assertSame(true, $deduper->contains('oldest'), 'deduper keeps refreshed entry');
assertSame(true, $deduper->contains('newest'), 'deduper keeps newest entry');

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

echo "testing business send ACK...\n";
[$serverPid, $connector] = startBindServer('send_accepted');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
assertClientError(
    ClientException::RESERVED_MSG_ID,
    static fn () => $client->send(new SendRequest(Packet::MSG_ID_BIND)),
    'reserved business MsgID',
);
$result = $client->send(new SendRequest(
    msgId: 2001,
    body: 'send_accepted',
    messageId: 'send_accepted',
    flags: Packet::FLAG_ACK_REQUIRED,
));
assertSame('send_accepted', $result->messageId, 'send result message ID');
assertSame(Ack::ACCEPTED, $result->ack?->code, 'send result ACK code');
assertSame(2001, $result->ack?->msgId, 'send result ACK MsgID');
$client->close();
finishBindServer($serverPid, 'business ACK server');

echo "testing send without ACK...\n";
[$serverPid, $connector] = startBindServer('send_no_ack');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
$result = $client->send(new SendRequest(
    msgId: 2001,
    body: 'send_no_ack',
));
assertSame(null, $result->ack, 'send without ACK result');
assertSame(true, str_starts_with($result->messageId, 'zc-msg-'), 'generated message ID prefix');
$client->close();
finishBindServer($serverPid, 'no ACK server');

echo "testing downlink before business ACK...\n";
[$serverPid, $connector] = startBindServer('send_downlink_before_ack');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
$result = $client->send(new SendRequest(
    msgId: 2001,
    body: 'send_downlink_before_ack',
    messageId: 'send_downlink_before_ack',
    ackRequired: true,
));
assertSame(Ack::ACCEPTED, $result->ack?->code, 'ACK after interleaved downlink');
$interleaved = $client->receive(1.0);
assertSame(2002, $interleaved->msgId, 'interleaved downlink MsgID');
assertSame('downlink-before-ack', $interleaved->messageId, 'interleaved downlink message ID');
assertSame('interleaved-downlink', $interleaved->body, 'interleaved downlink body');
$client->close();
finishBindServer($serverPid, 'interleaved downlink server');

echo "testing rejected business ACK...\n";
[$serverPid, $connector] = startBindServer('send_rejected');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
$ackError = assertClientError(
    ClientException::ACK_REJECTED,
    static fn () => $client->send(new SendRequest(
        msgId: 2001,
        body: 'send_rejected',
        messageId: 'send_rejected',
        ackRequired: true,
    )),
    'rejected business ACK',
);
assertSame(true, $ackError instanceof AckException, 'rejected ACK error type');
assertSame('route overloaded', $ackError instanceof AckException ? $ackError->ack->reason : '', 'rejected ACK reason');
assertSame(true, $client->ready(), 'client remains ready after rejected ACK');
assertSame(true, $client->lastError() === null, 'rejected ACK is not a connection failure');
$client->close();
finishBindServer($serverPid, 'rejected business ACK server');

echo "testing business ACK timeout...\n";
[$serverPid, $connector] = startBindServer('send_timeout');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
    ackTimeout: 0.05,
));
$client->connect();
assertClientError(
    ClientException::ACK_TIMEOUT,
    static fn () => $client->send(new SendRequest(
        msgId: 2001,
        body: 'send_timeout',
        messageId: 'send_timeout',
        ackRequired: true,
    )),
    'business ACK timeout',
);
assertSame(true, $client->ready(), 'client remains ready after ACK timeout');
$client->close();
finishBindServer($serverPid, 'business ACK timeout server');

echo "testing receive timeout...\n";
[$serverPid, $connector] = startBindServer('receive_timeout');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
assertClientError(
    ClientException::RECEIVE_TIMEOUT,
    static fn () => $client->receive(0.05),
    'downlink receive timeout',
);
assertSame(true, $client->ready(), 'client remains ready after receive timeout');
$client->close();
finishBindServer($serverPid, 'receive timeout server');

echo "testing manual downlink ACK...\n";
[$serverPid, $connector] = startBindServer('downlink_manual');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
$downlink = $client->receive(1.0);
assertSame(2001, $downlink->msgId, 'manual downlink MsgID');
assertSame('manual-downlink', $downlink->body, 'manual downlink body');
$deliveryResult = $client->acknowledgeDownlink($downlink);
assertSame(Ack::ACCEPTED, $deliveryResult->code, 'manual delivery ACK accepted');
assertSame(Packet::MSG_ID_DOWNLINK_ACK, $deliveryResult->msgId, 'manual delivery ACK MsgID');
$client->close();
finishBindServer($serverPid, 'manual downlink ACK server');

echo "testing automatic downlink ACK...\n";
[$serverPid, $connector] = startBindServer('downlink_auto');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
$handled = [];
$client->run(
    static function (Packet $packet) use (&$handled): void {
        $handled[] = $packet->messageId;
    },
    maxMessages: 1,
);
assertSame(['auto-1'], $handled, 'automatic downlink handler calls');
$client->close();
finishBindServer($serverPid, 'automatic downlink ACK server');

echo "testing rejected automatic downlink ACK...\n";
[$serverPid, $connector] = startBindServer('downlink_ack_rejected');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
$reported = [];
$client->run(
    static function (): void {
    },
    onError: static function (DownlinkException $error) use (&$reported): void {
        $reported[] = $error;
    },
    maxMessages: 1,
);
assertSame(1, count($reported), 'rejected automatic ACK error count');
assertSame(ClientException::AUTO_ACK_FAILED, $reported[0]->kind, 'rejected automatic ACK error kind');
assertSame('ack', $reported[0]->operation, 'rejected automatic ACK operation');
assertSame(true, $client->ready(), 'client remains ready after rejected delivery ACK');
$client->close();
finishBindServer($serverPid, 'rejected automatic downlink ACK server');

echo "testing duplicate downlink suppression...\n";
[$serverPid, $connector] = startBindServer('downlink_duplicate');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
    downlinkDedupCapacity: 2,
));
$client->connect();
$handledCount = 0;
$client->run(
    static function () use (&$handledCount): void {
        $handledCount++;
    },
    maxMessages: 2,
);
assertSame(1, $handledCount, 'duplicate downlink handler call count');
$client->close();
finishBindServer($serverPid, 'duplicate downlink server');

echo "testing failed downlink handler...\n";
[$serverPid, $connector] = startBindServer('downlink_handler_failed');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
$reported = [];
$client->run(
    static function (): void {
        throw new RuntimeException('database write failed');
    },
    onError: static function (DownlinkException $error) use (&$reported): void {
        $reported[] = $error;
    },
    maxMessages: 1,
);
assertSame(1, count($reported), 'failed handler error count');
assertSame(ClientException::HANDLER_FAILED, $reported[0]->kind, 'failed handler error kind');
assertSame('handle', $reported[0]->operation, 'failed handler operation');
assertSame('handler-failed-1', $reported[0]->messageId, 'failed handler message ID');
$client->close();
finishBindServer($serverPid, 'failed downlink handler server');

echo "testing invalid manual downlink ACK...\n";
[$serverPid, $connector] = startBindServer('downlink_invalid');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-pair',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
));
$client->connect();
$invalidDownlink = $client->receive(1.0);
assertClientError(
    ClientException::INVALID_DOWNLINK,
    static fn () => $client->acknowledgeDownlink($invalidDownlink),
    'invalid downlink ACK target',
);
$client->close();
finishBindServer($serverPid, 'invalid downlink server');

assertClientError(
    ClientException::INVALID_CONFIG,
    static fn () => new ReconnectConfig(initialDelay: 2.0, maxDelay: 1.0),
    'invalid reconnect delay range',
);

echo "testing receive loop reconnect...\n";
[$serverPid, $connector] = startReconnectServer('receive_resume');
$tokenCalls = 0;
$client = new GatewayClient(new ClientConfig(
    address: 'socket-sequence',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    tokenProvider: new CallbackTokenProvider(static function () use (&$tokenCalls): string {
        $tokenCalls++;
        return "token-{$tokenCalls}";
    }),
    connector: $connector,
    reconnect: new ReconnectConfig(
        initialDelay: 0.001,
        maxDelay: 0.002,
        multiplier: 2.0,
        jitter: 0.0,
        maxAttempts: 3,
    ),
));
$firstBinding = $client->connect();
assertSame('php-reconnect-session-1', $firstBinding->sessionId, 'initial reconnect test session');
$reconnectedMessages = [];
$client->run(
    static function (Packet $packet) use (&$reconnectedMessages): void {
        $reconnectedMessages[] = $packet->messageId;
    },
    maxMessages: 2,
);
assertSame(['reconnect-1', 'reconnect-2'], $reconnectedMessages, 'messages across reconnect');
assertSame('php-reconnect-session-2', $client->binding()?->sessionId, 'binding after reconnect');
assertSame(2, $tokenCalls, 'token provider calls across reconnect');
assertSame(2, $connector->calls, 'connector calls across reconnect');
assertSame(true, $client->ready(), 'client ready after receive reconnect');
$client->close();
finishBindServer($serverPid, 'receive reconnect server');

echo "testing failed send is not replayed...\n";
[$serverPid, $connector] = startReconnectServer('send_no_replay');
$tokenCalls = 0;
$client = new GatewayClient(new ClientConfig(
    address: 'socket-sequence',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    tokenProvider: new CallbackTokenProvider(static function () use (&$tokenCalls): string {
        $tokenCalls++;
        return "token-{$tokenCalls}";
    }),
    connector: $connector,
    ackTimeout: 0.5,
    reconnect: new ReconnectConfig(
        initialDelay: 0.001,
        maxDelay: 0.001,
        jitter: 0.0,
        maxAttempts: 2,
    ),
));
$client->connect();
assertClientError(
    ClientException::IO_ERROR,
    static fn () => $client->send(new SendRequest(
        msgId: 2001,
        body: 'send_no_replay',
        messageId: 'send-no-replay',
        ackRequired: true,
    )),
    'failed send after successful reconnect',
);
assertSame(true, $client->ready(), 'client ready after failed send reconnect');
assertSame('php-reconnect-session-2', $client->binding()?->sessionId, 'send reconnect binding');
assertSame(2, $tokenCalls, 'send reconnect token calls');
assertSame(2, $connector->calls, 'send reconnect connector calls');
$client->close();
finishBindServer($serverPid, 'send no replay server');

echo "testing reconnect exhaustion...\n";
[$serverPid, $connector] = startReconnectServer('exhausted');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-sequence',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
    reconnect: new ReconnectConfig(
        initialDelay: 0.001,
        maxDelay: 0.002,
        jitter: 0.0,
        maxAttempts: 2,
    ),
));
$client->connect();
$reconnectError = assertClientError(
    ClientException::RECONNECT_EXHAUSTED,
    static fn () => $client->receive(),
    'reconnect attempts exhausted',
);
assertSame(true, $reconnectError instanceof ReconnectException, 'reconnect exhaustion error type');
assertSame(2, $reconnectError instanceof ReconnectException ? $reconnectError->attempts : 0, 'reconnect attempts');
assertSame(State::Disconnected, $client->state(), 'state after reconnect exhaustion');
assertSame($reconnectError, $client->lastError(), 'last error after reconnect exhaustion');
assertSame(3, $connector->calls, 'connector calls after reconnect exhaustion');
$client->close();
finishBindServer($serverPid, 'reconnect exhaustion server');

echo "testing reconnect stops after authentication failure...\n";
[$serverPid, $connector] = startReconnectServer('authentication_failed');
$tokenCalls = 0;
$client = new GatewayClient(new ClientConfig(
    address: 'socket-sequence',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    tokenProvider: new CallbackTokenProvider(static function () use (&$tokenCalls): string {
        $tokenCalls++;
        return "token-{$tokenCalls}";
    }),
    connector: $connector,
    reconnect: new ReconnectConfig(
        initialDelay: 0.001,
        maxDelay: 0.002,
        jitter: 0.0,
        maxAttempts: 5,
    ),
));
$client->connect();
assertClientError(
    ClientException::AUTHENTICATION_FAILED,
    static fn () => $client->receive(),
    'authentication failure stops reconnect',
);
assertSame(2, $connector->calls, 'connector stops after authentication failure');
assertSame(2, $tokenCalls, 'token provider stops after authentication failure');
assertSame(State::Disconnected, $client->state(), 'state after reconnect authentication failure');
$client->close();
finishBindServer($serverPid, 'reconnect authentication failure server');

echo "testing close interrupts reconnect wait...\n";
[$serverPid, $connector] = startReconnectServer('close_interrupt');
$client = new GatewayClient(new ClientConfig(
    address: 'socket-sequence',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
    connector: $connector,
    reconnect: new ReconnectConfig(
        initialDelay: 5.0,
        maxDelay: 5.0,
        jitter: 0.0,
        maxAttempts: 1,
    ),
));
$client->connect();
$previousAsyncSignals = pcntl_async_signals(true);
$previousAlarmHandler = pcntl_signal_get_handler(SIGALRM);
pcntl_signal(SIGALRM, static function () use ($client): void {
    $client->close();
});
pcntl_alarm(1);
assertClientError(
    ClientException::CLOSED,
    static fn () => $client->receive(),
    'close interrupts reconnect wait',
);
pcntl_alarm(0);
pcntl_signal(SIGALRM, $previousAlarmHandler);
pcntl_async_signals($previousAsyncSignals);
assertSame(State::Closed, $client->state(), 'state after interrupted reconnect');
assertSame(1, $connector->calls, 'close prevents another reconnect dial');
finishBindServer($serverPid, 'close interrupt reconnect server');

$client = new GatewayClient(new ClientConfig(
    address: 'not-connected',
    clientId: 'claimed-client',
    deviceId: 'device-1',
    token: 'token-1',
));
assertClientError(
    ClientException::NOT_READY,
    static fn () => $client->send(new SendRequest(2001)),
    'send before connect',
);
$client->close();

require __DIR__ . '/tls.php';

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

function assertClientError(string $expectedKind, callable $callback, string $label): ClientException
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
        return $exception;
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

/** @return array{int, SequenceConnector} */
function startReconnectServer(string $mode): array
{
    if (!function_exists('pcntl_fork')) {
        throw new RuntimeException('PHP reconnect tests require the pcntl extension');
    }
    $connectionCount = in_array($mode, ['exhausted', 'close_interrupt'], true) ? 1 : 2;
    $pairs = [];
    for ($index = 0; $index < $connectionCount; $index++) {
        $pair = stream_socket_pair(STREAM_PF_UNIX, STREAM_SOCK_STREAM, STREAM_IPPROTO_IP);
        if ($pair === false) {
            throw new RuntimeException("cannot create {$mode} reconnect socket pair");
        }
        $pairs[] = $pair;
    }

    $pid = pcntl_fork();
    if ($pid === -1) {
        throw new RuntimeException("cannot fork {$mode} reconnect server");
    }
    if ($pid === 0) {
        foreach ($pairs as $pair) {
            fclose($pair[0]);
        }
        try {
            serveReconnect(array_map(static fn (array $pair): mixed => $pair[1], $pairs), $mode);
            foreach ($pairs as $pair) {
                if (is_resource($pair[1])) {
                    fclose($pair[1]);
                }
            }
            exit(0);
        } catch (Throwable $error) {
            fwrite(STDERR, "{$mode} reconnect server: {$error->getMessage()}\n");
            exit(1);
        }
    }

    $connections = [];
    foreach ($pairs as $pair) {
        fclose($pair[1]);
        $connections[] = $pair[0];
    }
    if ($mode === 'exhausted') {
        $connections[] = new RuntimeException('reconnect dial failed once');
        $connections[] = new RuntimeException('reconnect dial failed twice');
    }
    return [$pid, new SequenceConnector($connections)];
}

/** @param list<mixed> $connections */
function serveReconnect(array $connections, string $mode): void
{
    foreach ($connections as $connection) {
        stream_set_timeout($connection, 5);
    }

    if ($mode === 'receive_resume') {
        $firstParser = acceptReconnectBind(
            $connections[0],
            'token-1',
            'php-reconnect-session-1',
        );
        $firstDownlink = testDownlink('reconnect-1', true, 'php-reconnect-session-1');
        writeServerPacket($connections[0], $firstDownlink);
        readAndRespondToDeliveryAck($connections[0], $firstParser, $firstDownlink, token: 'token-1');
        fclose($connections[0]);

        $secondParser = acceptReconnectBind(
            $connections[1],
            'token-2',
            'php-reconnect-session-2',
        );
        $secondDownlink = testDownlink('reconnect-2', true, 'php-reconnect-session-2');
        writeServerPacket($connections[1], $secondDownlink);
        readAndRespondToDeliveryAck($connections[1], $secondParser, $secondDownlink, token: 'token-2');
        waitForClientClose($connections[1]);
        return;
    }

    if ($mode === 'send_no_replay') {
        $firstParser = acceptReconnectBind(
            $connections[0],
            'token-1',
            'php-reconnect-session-1',
        );
        $business = readServerPacket($connections[0], $firstParser);
        if (
            $business->msgId !== 2001
            || $business->body !== 'send_no_replay'
            || $business->messageId !== 'send-no-replay'
            || $business->sessionId !== 'php-reconnect-session-1'
            || $business->token !== 'token-1'
        ) {
            throw new RuntimeException('invalid business packet before reconnect');
        }
        fclose($connections[0]);

        acceptReconnectBind(
            $connections[1],
            'token-2',
            'php-reconnect-session-2',
        );
        assertNoServerPacket($connections[1]);
        waitForClientClose($connections[1]);
        return;
    }

    if ($mode === 'exhausted') {
        acceptReconnectBind($connections[0], 'token-1', 'php-reconnect-session-1');
        fclose($connections[0]);
        return;
    }

    if ($mode === 'authentication_failed') {
        acceptReconnectBind($connections[0], 'token-1', 'php-reconnect-session-1');
        fclose($connections[0]);
        acceptReconnectBind(
            $connections[1],
            'token-2',
            '',
            Ack::UNAUTHORIZED,
        );
        waitForClientClose($connections[1]);
        return;
    }

    if ($mode === 'close_interrupt') {
        acceptReconnectBind($connections[0], 'token-1', 'php-reconnect-session-1');
        fclose($connections[0]);
        return;
    }

    throw new RuntimeException("unknown reconnect mode {$mode}");
}

/** @param resource $connection */
function acceptReconnectBind(
    mixed $connection,
    string $token,
    string $sessionId,
    string $code = Ack::ACCEPTED,
): FrameParser {
    $parser = new FrameParser();
    $bind = readServerPacket($connection, $parser);
    if (
        $bind->msgId !== Packet::MSG_ID_BIND
        || $bind->flags !== Packet::FLAG_ACK_REQUIRED
        || $bind->clientId !== 'claimed-client'
        || $bind->deviceId !== 'device-1'
        || $bind->token !== $token
        || $bind->messageId === ''
        || $bind->traceId !== $bind->messageId
    ) {
        throw new RuntimeException('invalid reconnect bind packet');
    }
    $reason = $code === Ack::ACCEPTED ? '' : 'reconnect authentication rejected for test';
    writeServerAck($connection, $bind, $code, $reason, $sessionId);
    return $parser;
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
    $bind = readServerPacket($connection, $parser);
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
    writeServerAck($connection, $bind, $code, $reason, $sessionId);

    if (str_starts_with($mode, 'send_')) {
        $business = readServerPacket($connection, $parser);
        if (
            $business->msgId !== 2001
            || $business->body !== $mode
            || $business->clientId !== 'canonical-client'
            || $business->deviceId !== 'device-1'
            || $business->sessionId !== 'php-session-1'
            || $business->token !== 'token-1'
            || $business->sequence !== '2'
        ) {
            throw new RuntimeException('invalid business packet');
        }
        if ($mode === 'send_no_ack') {
            if (!str_starts_with($business->messageId, 'zc-msg-') || $business->traceId !== $business->messageId) {
                throw new RuntimeException('invalid generated message identity');
            }
        } elseif ($business->messageId !== $mode || $business->traceId !== $mode) {
            throw new RuntimeException('invalid explicit message identity');
        }
        $ackRequired = $mode !== 'send_no_ack';
        if ((($business->flags & Packet::FLAG_ACK_REQUIRED) !== 0) !== $ackRequired) {
            throw new RuntimeException('invalid business ACK-required flag');
        }

        if ($mode === 'send_timeout') {
            usleep(500_000);
            return;
        }
        if ($mode === 'send_downlink_before_ack') {
            $downlink = new Packet(
                msgId: 2002,
                body: 'interleaved-downlink',
                flags: Packet::FLAG_ACK_REQUIRED,
                sequence: '99',
                timestamp: (string) (int) floor(microtime(true) * 1000),
                clientId: 'canonical-client',
                deviceId: 'device-1',
                sessionId: 'php-session-1',
                messageId: 'downlink-before-ack',
                traceId: 'downlink-before-ack',
            );
            writeServerPacket($connection, $downlink);
        }
        if ($mode === 'send_rejected') {
            writeServerAck($connection, $business, Ack::REJECTED, 'route overloaded', 'php-session-1');
        } elseif ($ackRequired) {
            writeServerAck($connection, $business, Ack::ACCEPTED, '', 'php-session-1');
        }
    }

    if ($mode === 'receive_timeout') {
        usleep(200_000);
    }

    if (str_starts_with($mode, 'downlink_')) {
        $messageId = match ($mode) {
            'downlink_manual' => 'manual-1',
            'downlink_auto' => 'auto-1',
            'downlink_ack_rejected' => 'ack-rejected-1',
            'downlink_duplicate' => 'duplicate-1',
            'downlink_handler_failed' => 'handler-failed-1',
            'downlink_invalid' => 'invalid-1',
            default => throw new RuntimeException("unknown downlink mode {$mode}"),
        };
        $ackRequired = $mode !== 'downlink_invalid';
        $downlink = testDownlink($messageId, $ackRequired);
        writeServerPacket($connection, $downlink);

        if ($mode === 'downlink_handler_failed') {
            assertNoServerPacket($connection);
        } elseif ($mode !== 'downlink_invalid') {
            readAndRespondToDeliveryAck(
                $connection,
                $parser,
                $downlink,
                $mode === 'downlink_ack_rejected' ? Ack::REJECTED : Ack::ACCEPTED,
            );
            if ($mode === 'downlink_duplicate') {
                writeServerPacket($connection, $downlink);
                readAndRespondToDeliveryAck($connection, $parser, $downlink);
            }
        }
    }

    waitForClientClose($connection);
}

function testDownlink(
    string $messageId,
    bool $ackRequired = true,
    string $sessionId = 'php-session-1',
): Packet
{
    return new Packet(
        msgId: 2001,
        body: match ($messageId) {
            'manual-1' => 'manual-downlink',
            default => "body-{$messageId}",
        },
        flags: $ackRequired ? Packet::FLAG_ACK_REQUIRED : 0,
        sequence: '99',
        timestamp: (string) (int) floor(microtime(true) * 1000),
        clientId: 'canonical-client',
        deviceId: 'device-1',
        sessionId: $sessionId,
        messageId: $messageId,
        traceId: "trace-{$messageId}",
    );
}

/** @param resource $connection */
function readAndRespondToDeliveryAck(
    mixed $connection,
    FrameParser $parser,
    Packet $downlink,
    string $code = Ack::ACCEPTED,
    string $token = 'token-1',
): void
{
    $packet = readServerPacket($connection, $parser);
    if (
        $packet->msgId !== Packet::MSG_ID_DOWNLINK_ACK
        || ($packet->flags & Packet::FLAG_ACK_REQUIRED) === 0
        || !str_starts_with($packet->messageId, 'zc-dack-')
        || $packet->messageId === $downlink->messageId
        || $packet->traceId !== $downlink->traceId
        || $packet->clientId !== 'canonical-client'
        || $packet->deviceId !== 'device-1'
        || $packet->sessionId !== $downlink->sessionId
        || $packet->token !== $token
    ) {
        throw new RuntimeException('invalid delivery ACK packet');
    }
    $deliveryAck = DeliveryAck::fromPacket($packet);
    if ($deliveryAck->code !== DeliveryAck::DELIVERED || $deliveryAck->messageId !== $downlink->messageId) {
        throw new RuntimeException('invalid delivery ACK body');
    }
    $reason = $code === Ack::ACCEPTED ? '' : 'delivery ACK rejected for test';
    writeServerAck($connection, $packet, $code, $reason, $downlink->sessionId);
}

/** @param resource $connection */
function assertNoServerPacket(mixed $connection): void
{
    stream_set_timeout($connection, 0, 200_000);
    $chunk = fread($connection, 8192);
    if (is_string($chunk) && $chunk !== '') {
        throw new RuntimeException('handler failure unexpectedly sent a delivery ACK');
    }
}

/** @param resource $connection */
function readServerPacket(mixed $connection, FrameParser $parser): Packet
{
    while (true) {
        $chunk = fread($connection, 8192);
        if ($chunk === false || $chunk === '') {
            throw new RuntimeException('connection closed before packet arrived');
        }
        $packets = $parser->push($chunk);
        if ($packets !== []) {
            return $packets[0];
        }
    }
}

/** @param resource $connection */
function writeServerAck(
    mixed $connection,
    Packet $origin,
    string $code,
    string $reason,
    string $sessionId,
): void {
    $ack = new Ack($code, $origin->msgId, $origin->messageId, $reason);
    writeServerPacket($connection, new Packet(
        msgId: Packet::MSG_ID_ACK,
        body: $ack->toJson(),
        sequence: $origin->sequence,
        timestamp: (string) (int) floor(microtime(true) * 1000),
        clientId: $origin->msgId === Packet::MSG_ID_BIND ? 'canonical-client' : $origin->clientId,
        deviceId: $origin->deviceId,
        sessionId: $sessionId,
        messageId: $origin->messageId,
        traceId: $origin->traceId,
    ));
}

/** @param resource $connection */
function writeServerPacket(mixed $connection, Packet $packet): void
{
    $data = FrameCodec::encode($packet);
    while ($data !== '') {
        $written = fwrite($connection, $data);
        if ($written === false || $written === 0) {
            throw new RuntimeException('write packet failed');
        }
        $data = substr($data, $written);
    }
}

/** @param resource $connection */
function waitForClientClose(mixed $connection): void
{
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
