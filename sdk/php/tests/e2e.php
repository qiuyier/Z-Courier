<?php

declare(strict_types=1);

require __DIR__ . '/bootstrap.php';

use ZCourier\Client\CallbackTokenProvider;
use ZCourier\Client\Client;
use ZCourier\Client\Config;
use ZCourier\Client\ReconnectConfig;
use ZCourier\Client\SendRequest;
use ZCourier\Exception\ClientException;
use ZCourier\Exception\DownlinkException;
use ZCourier\Protocol\Ack;
use ZCourier\Protocol\Packet;

try {
    runE2E(readConfiguration());
    fwrite(STDOUT, "PHP SDK e2e passed\n");
} catch (Throwable $error) {
    fwrite(STDERR, "PHP SDK e2e failed: {$error->getMessage()}\n");
    exit(1);
}

/** @return array<string, int|string> */
function readConfiguration(): array
{
    $options = getopt('', [
        'tcp-address:',
        'internal-url:',
        'internal-token:',
        'client-id:',
        'device-id:',
        'token:',
        'upstream-msg-id:',
        'timeout:',
    ]);
    if ($options === false) {
        throw new RuntimeException('cannot parse command-line options');
    }
    $msgId = optionInt($options, 'upstream-msg-id', 2001);
    if ($msgId <= Packet::MSG_ID_BIND || $msgId > 0xFFFFFFFF) {
        throw new RuntimeException('upstream MsgID must be a non-reserved uint32');
    }
    $timeout = optionInt($options, 'timeout', 30);
    if ($timeout <= 0) {
        throw new RuntimeException('timeout must be greater than zero');
    }
    return [
        'tcp_address' => optionString($options, 'tcp-address', '127.0.0.1:9899'),
        'internal_url' => rtrim(optionString($options, 'internal-url', 'http://127.0.0.1:18082'), '/'),
        'internal_token' => optionString($options, 'internal-token', 'dev-internal-token'),
        'client_id' => optionString($options, 'client-id', 'e2e-client'),
        'device_id' => optionString($options, 'device-id', 'php-sdk-e2e-device'),
        'token' => optionString($options, 'token', 'e2e-token'),
        'upstream_msg_id' => $msgId,
        'timeout' => $timeout,
    ];
}

/** @param array<string, int|string> $configuration */
function runE2E(array $configuration): void
{
    $tokenCalls = 0;
    $token = (string) $configuration['token'];
    $client = new Client(new Config(
        address: (string) $configuration['tcp_address'],
        clientId: (string) $configuration['client_id'],
        deviceId: (string) $configuration['device_id'],
        tokenProvider: new CallbackTokenProvider(static function () use (&$tokenCalls, $token): string {
            $tokenCalls++;
            return $token;
        }),
        connectTimeout: 5.0,
        bindTimeout: 5.0,
        writeTimeout: 5.0,
        ackTimeout: 5.0,
        reconnect: new ReconnectConfig(
            initialDelay: 0.1,
            maxDelay: 1.0,
            multiplier: 2.0,
            jitter: 0.1,
            maxAttempts: 10,
        ),
    ));

    try {
        $binding = $client->connect();
        $initialSessionId = $binding->sessionId;
        fwrite(STDOUT, "PHP SDK bound: session_id={$initialSessionId}\n");

        verifyUpstream($client, (int) $configuration['upstream_msg_id'], 'before-reconnect');
        verifyDownlink($client, $configuration, 'before-reconnect');
        verifyReconnect($client, $configuration, $initialSessionId);

        if ($tokenCalls < 2) {
            throw new RuntimeException("token provider calls={$tokenCalls}, want at least 2");
        }
        verifyUpstream($client, (int) $configuration['upstream_msg_id'], 'after-reconnect');
        verifyDownlink($client, $configuration, 'after-reconnect');
    } finally {
        $client->close();
    }
}

function verifyUpstream(Client $client, int $msgId, string $phase): void
{
    $messageId = uniqueMessageId("php-sdk-e2e-upstream-{$phase}");
    $result = $client->send(new SendRequest(
        msgId: $msgId,
        body: "php-sdk-e2e-{$phase}",
        messageId: $messageId,
        traceId: $messageId,
        ackRequired: true,
    ));
    if ($result->ack?->code !== Ack::ACCEPTED) {
        throw new RuntimeException("{$phase} upstream returned an unexpected ACK");
    }
    fwrite(STDOUT, "PHP SDK upstream accepted: phase={$phase} message_id={$messageId}\n");
}

/** @param array<string, int|string> $configuration */
function verifyDownlink(Client $client, array $configuration, string $phase): void
{
    $messageId = uniqueMessageId("php-sdk-e2e-downlink-{$phase}");
    $body = "php-sdk-e2e-downlink-{$phase}";
    $response = internalRequest(
        $configuration,
        'POST',
        '/internal/push',
        [
            'client_id' => $configuration['client_id'],
            'device_id' => $configuration['device_id'],
            'msg_id' => 2001,
            'message_id' => $messageId,
            'trace_id' => $messageId,
            'ack_required' => true,
            'body' => base64_encode($body),
        ],
    );
    if (($response['message_id'] ?? null) !== $messageId) {
        throw new RuntimeException("{$phase} downlink push returned a different MessageID");
    }

    $received = [];
    $errors = [];
    $client->run(
        handler: static function (Packet $packet) use (&$received): void {
            $received[] = $packet;
        },
        onError: static function (DownlinkException $error) use (&$errors): void {
            $errors[] = $error;
        },
        maxMessages: 1,
    );
    if ($errors !== []) {
        throw new RuntimeException("{$phase} downlink callback failed: {$errors[0]->getMessage()}");
    }
    $packet = $received[0] ?? null;
    if (!$packet instanceof Packet || $packet->messageId !== $messageId || $packet->body !== $body) {
        throw new RuntimeException("{$phase} downlink packet does not match the push request");
    }

    waitDelivered($configuration, $messageId);
    fwrite(STDOUT, "PHP SDK downlink delivered: phase={$phase} message_id={$messageId}\n");
}

/** @param array<string, int|string> $configuration */
function verifyReconnect(Client $client, array $configuration, string $initialSessionId): void
{
    $replacement = new Client(new Config(
        address: (string) $configuration['tcp_address'],
        clientId: (string) $configuration['client_id'],
        deviceId: (string) $configuration['device_id'],
        token: (string) $configuration['token'],
    ));
    try {
        $replacement->connect();
    } finally {
        $replacement->close();
    }

    try {
        $client->receive(3.0);
        throw new RuntimeException('replaced connection unexpectedly received a packet');
    } catch (ClientException $error) {
        if ($error->kind !== ClientException::IO_ERROR) {
            throw $error;
        }
    }
    if (!$client->ready()) {
        throw new RuntimeException('client did not become ready after reconnect');
    }
    $sessionId = $client->binding()?->sessionId ?? '';
    if ($sessionId === '' || $sessionId === $initialSessionId) {
        throw new RuntimeException(
            "reconnect session_id={$sessionId}, want a value different from {$initialSessionId}",
        );
    }
    fwrite(
        STDOUT,
        "PHP SDK reconnected: old_session_id={$initialSessionId} new_session_id={$sessionId}\n",
    );
}

/** @param array<string, int|string> $configuration */
function waitDelivered(array $configuration, string $messageId): void
{
    $deadline = microtime(true) + (int) $configuration['timeout'];
    do {
        $response = internalRequest(
            $configuration,
            'GET',
            '/internal/message/status?message_id=' . rawurlencode($messageId),
        );
        $status = $response['status'] ?? '';
        if ($status === 'delivered') {
            return;
        }
        if ($status === 'failed' || $status === 'discarded') {
            throw new RuntimeException("message entered terminal status {$status}");
        }
        usleep(50_000);
    } while (microtime(true) < $deadline);

    throw new RuntimeException("timed out waiting for delivered status for {$messageId}");
}

/**
 * @param array<string, int|string> $configuration
 * @param null|array<string, mixed> $requestBody
 * @return array<string, mixed>
 */
function internalRequest(
    array $configuration,
    string $method,
    string $path,
    ?array $requestBody = null,
): array {
    $content = $requestBody === null
        ? ''
        : json_encode($requestBody, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
    $headers = [
        'Accept: application/json',
        'X-ZCourier-Internal-Token: ' . $configuration['internal_token'],
    ];
    if ($requestBody !== null) {
        $headers[] = 'Content-Type: application/json';
        $headers[] = 'Content-Length: ' . strlen($content);
    }
    $context = stream_context_create(['http' => [
        'method' => $method,
        'header' => implode("\r\n", $headers),
        'content' => $content,
        'ignore_errors' => true,
        'timeout' => min(10, (int) $configuration['timeout']),
    ]]);
    $responseBody = @file_get_contents(
        (string) $configuration['internal_url'] . $path,
        false,
        $context,
    );
    $responseHeaders = $http_response_header ?? [];
    $status = httpStatus($responseHeaders);
    if ($responseBody === false) {
        $lastError = error_get_last();
        throw new RuntimeException('internal HTTP request failed: ' . ($lastError['message'] ?? 'unknown error'));
    }
    if ($status < 200 || $status >= 300) {
        throw new RuntimeException("internal HTTP status={$status} body={$responseBody}");
    }
    $decoded = json_decode($responseBody, true, 512, JSON_THROW_ON_ERROR);
    if (!is_array($decoded)) {
        throw new RuntimeException('internal HTTP response is not a JSON object');
    }
    return $decoded;
}

/** @param list<string> $headers */
function httpStatus(array $headers): int
{
    foreach ($headers as $header) {
        if (preg_match('/^HTTP\/\S+\s+(\d{3})\b/', $header, $matches) === 1) {
            return (int) $matches[1];
        }
    }
    return 0;
}

/** @param array<string, mixed> $options */
function optionString(array $options, string $name, string $default): string
{
    $value = $options[$name] ?? $default;
    if (!is_string($value) || trim($value) === '') {
        throw new RuntimeException("option --{$name} must be a non-empty string");
    }
    return $value;
}

/** @param array<string, mixed> $options */
function optionInt(array $options, string $name, int $default): int
{
    $value = $options[$name] ?? (string) $default;
    if (!is_string($value) || filter_var($value, FILTER_VALIDATE_INT) === false) {
        throw new RuntimeException("option --{$name} must be an integer");
    }
    return (int) $value;
}

function uniqueMessageId(string $prefix): string
{
    return $prefix . '-' . (string) hrtime(true) . '-' . bin2hex(random_bytes(4));
}
